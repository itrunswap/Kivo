package mihomo

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/itrunswap/Kivo/internal/core"
)

const releasesAPI = "https://api.github.com/repos/MetaCubeX/mihomo/releases"

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
}

// Installer 安全下载并原子安装官方 Mihomo Release。
type Installer struct {
	coreDir     string
	downloadDir string
	http        *http.Client
	retries     int
}

// NewInstaller 创建安装器。
func NewInstaller(coreDir, downloadDir string) *Installer {
	return &Installer{
		coreDir: coreDir, downloadDir: downloadDir,
		http: &http.Client{Timeout: 20 * time.Minute}, retries: 4,
	}
}

// ConfigureDownload 设置下载重试次数和可选 HTTP/HTTPS 代理。
// 空代理沿用 Go 的系统环境代理（HTTP_PROXY/HTTPS_PROXY/NO_PROXY）。
func (i *Installer) ConfigureDownload(retries int, proxyAddress string) error {
	if retries < 1 {
		retries = 1
	}
	i.retries = retries
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if strings.TrimSpace(proxyAddress) != "" && !strings.EqualFold(proxyAddress, "system") {
		parsed, err := url.Parse(proxyAddress)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return errors.New("下载代理必须是有效的 http:// 或 https:// 地址")
		}
		transport.Proxy = http.ProxyURL(parsed)
	}
	i.http = &http.Client{Timeout: 20 * time.Minute, Transport: transport}
	return nil
}

// Install 安装指定版本；version 为空或 latest 时安装最新稳定版。
func (i *Installer) Install(ctx context.Context, version string, progress func(core.InstallEvent)) (string, string, error) {
	return i.install(ctx, version, progress, nil)
}

// install 仅在下载和校验完成后调用 beforeReplace。Windows 不允许替换正在
// 执行的 exe，不能等安装结束后才停止内核，也不应在整个下载期间中断代理。
func (i *Installer) install(ctx context.Context, version string, progress func(core.InstallEvent), beforeReplace func(string) error) (string, string, error) {
	if progress == nil {
		progress = func(core.InstallEvent) {}
	}
	progress(core.InstallEvent{Stage: "resolve", Message: "正在查询 Mihomo 官方版本"})
	rel, err := i.resolveRelease(ctx, version)
	if err != nil {
		return "", "", err
	}
	selected, err := selectAsset(rel.Assets)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(i.downloadDir, 0o700); err != nil {
		return "", "", fmt.Errorf("创建下载目录: %w", err)
	}
	downloadPath := filepath.Join(i.downloadDir, selected.Name+".part")
	progress(core.InstallEvent{Stage: "download", Message: "正在下载 " + selected.Name, Total: selected.Size})
	actualDigest, err := i.downloadWithRetry(ctx, selected, downloadPath, progress)
	if err != nil {
		return "", "", err
	}
	progress(core.InstallEvent{Stage: "verify", Message: "正在校验安装包"})
	if selected.Digest != "" {
		expected := strings.TrimPrefix(selected.Digest, "sha256:")
		if !strings.EqualFold(expected, actualDigest) {
			_ = os.Remove(downloadPath)
			return "", "", errors.New("Mihomo 安装包 SHA256 校验失败")
		}
	}

	versionDir := filepath.Join(i.coreDir, "mihomo", strings.TrimPrefix(rel.TagName, "v"))
	if err := os.MkdirAll(versionDir, 0o700); err != nil {
		return "", "", fmt.Errorf("创建版本目录: %w", err)
	}
	binaryName := "mihomo"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(versionDir, binaryName)
	if beforeReplace != nil {
		if err := beforeReplace(binaryPath); err != nil {
			return "", "", err
		}
	}
	progress(core.InstallEvent{Stage: "extract", Message: "正在解压并安装"})
	if err := extractBinary(downloadPath, selected.Name, binaryPath); err != nil {
		return "", "", err
	}
	_ = os.Remove(downloadPath)
	progress(core.InstallEvent{Stage: "activate", Message: "文件已安装，正在切换活动版本"})
	return binaryPath, rel.TagName, nil
}

func (i *Installer) resolveRelease(ctx context.Context, version string) (release, error) {
	endpoint := releasesAPI + "/latest"
	if version != "" && version != "latest" {
		if !strings.HasPrefix(version, "v") {
			version = "v" + version
		}
		endpoint = releasesAPI + "/tags/" + version
	}
	var result release
	if err := i.getJSON(ctx, endpoint, &result); err != nil {
		return release{}, err
	}
	return result, nil
}

func (i *Installer) getJSON(ctx context.Context, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Kivo")
	resp, err := i.http.Do(req)
	if err != nil {
		return fmt.Errorf("查询 GitHub Release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub Release API 返回 HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out); err != nil {
		return fmt.Errorf("解析 GitHub Release: %w", err)
	}
	return nil
}

func selectAsset(assets []asset) (asset, error) {
	osName := runtime.GOOS
	arch := runtime.GOARCH
	if arch == "386" {
		arch = "386"
	}
	ext := "gz"
	if osName == "windows" {
		ext = "zip"
	}
	pattern := regexp.MustCompile(fmt.Sprintf(`^mihomo-%s-%s-v[0-9].*\.%s$`, regexp.QuoteMeta(osName), regexp.QuoteMeta(arch), ext))
	for _, item := range assets {
		if pattern.MatchString(item.Name) && !strings.Contains(item.Name, "compatible") && !strings.Contains(item.Name, "go1") {
			return item, nil
		}
	}
	return asset{}, fmt.Errorf("没有找到适用于 %s/%s 的 Mihomo 安装包", osName, arch)
}

func (i *Installer) downloadWithRetry(ctx context.Context, item asset, path string, progress func(core.InstallEvent)) (string, error) {
	if progress == nil {
		progress = func(core.InstallEvent) {}
	}
	var lastErr error
	for attempt := 1; attempt <= i.retries; attempt++ {
		digest, err := i.download(ctx, item, path, progress)
		if err == nil {
			return digest, nil
		}
		lastErr = err
		if attempt == i.retries || ctx.Err() != nil {
			break
		}
		wait := time.Duration(1<<(attempt-1)) * time.Second
		progress(core.InstallEvent{Stage: "retry", Message: fmt.Sprintf("下载中断，%s 后进行第 %d/%d 次重试：%v", wait, attempt+1, i.retries, err)})
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	return "", fmt.Errorf("下载 Mihomo（最多尝试 %d 次）: %w", i.retries, lastErr)
}

func (i *Installer) download(ctx context.Context, item asset, path string, progress func(core.InstallEvent)) (string, error) {
	if progress == nil {
		progress = func(core.InstallEvent) {}
	}
	offset := int64(0)
	if info, err := os.Stat(path); err == nil {
		offset = info.Size()
		// 上次可能已下载完成，只在解压或切换版本时失败。完整包不再发送末尾 Range。
		if item.Size > 0 && offset == item.Size && item.Digest != "" {
			digest, hashErr := hashFile(path)
			if hashErr != nil {
				return "", hashErr
			}
			if strings.EqualFold(digest, strings.TrimPrefix(item.Digest, "sha256:")) {
				progress(core.InstallEvent{Stage: "verify", Message: "完整缓存校验通过，跳过下载", Downloaded: offset, Total: item.Size})
				return digest, nil
			}
		}
		if item.Size > 0 && offset >= item.Size {
			if err := os.Truncate(path, 0); err != nil {
				return "", fmt.Errorf("重置无效缓存: %w", err)
			}
			offset = 0
		}
	}
	return i.downloadRange(ctx, item, path, offset, progress)
}

// downloadRange 在 Range 不可满足时仅回退一次完整下载，避免重复请求同一个失效偏移。
func (i *Installer) downloadRange(ctx context.Context, item asset, path string, offset int64, progress func(core.InstallEvent)) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.BrowserDownloadURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Kivo")
	req.Header.Set("Accept-Encoding", "identity")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := i.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0 {
		resp.Body.Close()
		progress(core.InstallEvent{Stage: "retry", Message: "续传范围已失效，自动重新下载"})
		return i.downloadRange(ctx, item, path, 0, progress)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return "", fmt.Errorf("下载 Mihomo 返回 HTTP %d", resp.StatusCode)
	}
	flags := os.O_CREATE | os.O_WRONLY
	if resp.StatusCode == http.StatusPartialContent {
		var start, end, total int64
		if _, err := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); err != nil || start != offset || end < start || total <= end || (item.Size > 0 && total != item.Size) {
			resp.Body.Close()
			if offset > 0 {
				progress(core.InstallEvent{Stage: "retry", Message: "续传响应不匹配，自动重新下载"})
				return i.downloadRange(ctx, item, path, 0, progress)
			}
			return "", errors.New("服务器返回了无效的 Content-Range")
		}
	}
	if offset > 0 && resp.StatusCode == http.StatusPartialContent {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
		offset = 0
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return "", fmt.Errorf("创建下载文件: %w", err)
	}
	totalSize := item.Size
	if totalSize <= 0 && resp.ContentLength > 0 {
		totalSize = offset + resp.ContentLength
	}
	reporter := &downloadReporter{progress: progress, offset: offset, total: totalSize, started: time.Now()}
	reporter.emit(true)
	written, err := io.Copy(io.MultiWriter(file, reporter), io.LimitReader(resp.Body, (256<<20)+1))
	reporter.emit(true)
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	total := offset + written
	if total > 256<<20 {
		return "", errors.New("安装包超过 256 MiB 限制")
	}
	if item.Size > 0 && total != item.Size {
		return "", fmt.Errorf("下载大小不完整：得到 %d 字节，期望 %d 字节", total, item.Size)
	}
	digest, err := hashFile(path)
	if err != nil {
		return "", err
	}
	return digest, nil
}

// downloadReporter 由同一下载协程调用；每 200ms 推送字节进度，避免终端刷屏。
type downloadReporter struct {
	progress                func(core.InstallEvent)
	offset, total, received int64
	started, last           time.Time
}

func (p *downloadReporter) Write(data []byte) (int, error) {
	p.received += int64(len(data))
	p.emit(false)
	return len(data), nil
}

func (p *downloadReporter) emit(force bool) {
	if !force && time.Since(p.last) < 200*time.Millisecond {
		return
	}
	p.last = time.Now()
	speed := int64(0)
	if elapsed := time.Since(p.started).Seconds(); elapsed > 0 {
		speed = int64(float64(p.received) / elapsed)
	}
	p.progress(core.InstallEvent{Stage: "download", Message: "正在下载", Downloaded: p.offset + p.received, Total: p.total, BytesPerSecond: speed})
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extractBinary(archivePath, assetName, outputPath string) error {
	tmp := outputPath + ".tmp"
	_ = os.Remove(tmp)
	var err error
	if strings.HasSuffix(assetName, ".zip") {
		err = extractZip(archivePath, tmp)
	} else {
		err = extractGzip(archivePath, tmp)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return fmt.Errorf("设置 Mihomo 执行权限: %w", err)
	}
	if err := os.Rename(tmp, outputPath); err != nil {
		return fmt.Errorf("安装 Mihomo: %w", err)
	}
	return nil
}

func extractGzip(archivePath, outputPath string) error {
	source, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer source.Close()
	reader, err := gzip.NewReader(source)
	if err != nil {
		return fmt.Errorf("打开 gzip: %w", err)
	}
	defer reader.Close()
	return copyLimited(reader, outputPath)
}

func extractZip(archivePath, outputPath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("打开 zip: %w", err)
	}
	defer reader.Close()
	var binary *zip.File
	for _, file := range reader.File {
		name := strings.ToLower(filepath.Base(file.Name))
		// 官方 Windows 压缩包中的名称带 OS/架构后缀；始终写入固定目标，绝不使用归档路径。
		if file.FileInfo().IsDir() || (name != "mihomo.exe" && name != "mihomo" && !strings.HasPrefix(name, "mihomo-windows-")) {
			continue
		}
		if name != "mihomo" && !strings.HasSuffix(name, ".exe") {
			continue
		}
		if binary != nil {
			return errors.New("zip 中包含多个 Mihomo 可执行文件，无法确定安装目标")
		}
		binary = file
	}
	if binary != nil {
		entry, err := binary.Open()
		if err != nil {
			return err
		}
		err = copyLimited(entry, outputPath)
		_ = entry.Close()
		return err
	}
	return errors.New("zip 中未找到 Mihomo 可执行文件")
}

func copyLimited(reader io.Reader, outputPath string) error {
	target, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	defer target.Close()
	written, err := io.Copy(target, io.LimitReader(reader, 256<<20))
	if err != nil {
		return err
	}
	if written == 256<<20 {
		return errors.New("解压后的 Mihomo 文件异常过大")
	}
	return nil
}
