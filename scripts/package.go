// 发布打包工具只收录固定白名单文件，避免把订阅、密钥或运行缓存带入发行包。
// 用法：go run ./scripts/package.go -input dist -output dist/releases
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var releaseTargets = []string{
	"windows-amd64", "windows-arm64", "darwin-amd64",
	"darwin-arm64", "linux-amd64", "linux-arm64",
}

type packageEntry struct {
	name string
	data []byte
	mode int64
}

// 固定归档时间，保证同一组输入重复打包时得到相同校验值。
var archiveTime = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

func main() {
	input := flag.String("input", "dist", "六个平台原始程序所在目录")
	output := flag.String("output", "dist/releases", "输出目录；不覆盖已有文件")
	source := flag.String("source", ".", "中文文档所在的项目根目录")
	flag.Parse()
	if err := packageRelease(*input, *output, *source); err != nil {
		fmt.Fprintln(os.Stderr, "打包失败：", err)
		os.Exit(1)
	}
	fmt.Println("六个平台打包完成；SHA256SUMS 校验的是发行压缩包。")
}

func packageRelease(input, output, source string) error {
	// 提前读取全部输入，缺失文件时不生成貌似完整的发布目录。
	documents := []packageEntry{}
	for _, item := range []struct{ source, name string }{
		{"LICENSE", "LICENSE"},
		{"README.md", "README.md"},
		{"docs/INSTALL.md", "docs/INSTALL.md"},
		{"docs/USER_MANUAL.md", "docs/USER_MANUAL.md"},
		{"docs/COMMANDS.md", "docs/COMMANDS.md"},
		{"docs/API.md", "docs/API.md"},
		{"docs/ARCHITECTURE.md", "docs/ARCHITECTURE.md"},
		{"docs/CODE_STYLE.md", "docs/CODE_STYLE.md"},
		{"docs/WEB_DESIGN.md", "docs/WEB_DESIGN.md"},
		{"docs/RELEASE.md", "docs/RELEASE.md"},
		{"docs/TEST_REPORT_2026-10-06.md", "docs/TEST_REPORT_2026-10-06.md"},
		{"CHANGELOG.md", "CHANGELOG.md"},
		{"THIRD_PARTY_NOTICES.md", "THIRD_PARTY_NOTICES.md"},
	} {
		data, err := os.ReadFile(filepath.Join(source, item.source))
		if err != nil {
			return fmt.Errorf("读取文档 %s: %w", item.source, err)
		}
		if len(data) == 0 {
			return fmt.Errorf("文档 %s 不能为空", item.source)
		}
		documents = append(documents, packageEntry{item.name, data, 0644})
	}
	programs := make(map[string][]byte, len(releaseTargets))
	for _, target := range releaseTargets {
		ext := ""
		if strings.HasPrefix(target, "windows-") {
			ext = ".exe"
		}
		name := "kivo-" + target + ext
		data, err := os.ReadFile(filepath.Join(input, name))
		if err != nil {
			return fmt.Errorf("读取程序 %s: %w", name, err)
		}
		if len(data) == 0 {
			return fmt.Errorf("程序 %s 不能为空", name)
		}
		programs[target] = data
	}
	if err := os.MkdirAll(output, 0755); err != nil {
		return err
	}
	// 拒绝覆盖旧资产；每次本地发布应使用新的输出目录。
	names := []string{"SHA256SUMS"}
	for _, target := range releaseTargets {
		names = append(names, assetName(target))
	}
	for _, name := range names {
		if _, err := os.Lstat(filepath.Join(output, name)); !os.IsNotExist(err) {
			if err != nil {
				return err
			}
			return fmt.Errorf("文件 %s 已存在，请使用新的输出目录", name)
		}
	}
	var sums strings.Builder
	for _, target := range releaseTargets {
		program := "kivo"
		if strings.HasPrefix(target, "windows-") {
			program += ".exe"
		}
		entries := append([]packageEntry{{program, programs[target], 0755}}, documents...)
		var archive bytes.Buffer
		if err := writeArchive(&archive, strings.HasPrefix(target, "windows-"), entries); err != nil {
			return fmt.Errorf("打包 %s: %w", target, err)
		}
		name := assetName(target)
		if err := writeNew(filepath.Join(output, name), archive.Bytes()); err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(archive.Bytes()), name)
		fmt.Println("已生成", name)
	}
	return writeNew(filepath.Join(output, "SHA256SUMS"), []byte(sums.String()))
}

func assetName(target string) string {
	if strings.HasPrefix(target, "windows-") {
		return "kivo-" + target + ".zip"
	}
	return "kivo-" + target + ".tar.gz"
}

func writeArchive(output io.Writer, windows bool, entries []packageEntry) error {
	if windows {
		writer := zip.NewWriter(output)
		for _, entry := range entries {
			header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
			header.SetModTime(archiveTime)
			header.SetMode(os.FileMode(entry.mode))
			file, err := writer.CreateHeader(header)
			if err != nil {
				_ = writer.Close()
				return err
			}
			if _, err := file.Write(entry.data); err != nil {
				_ = writer.Close()
				return err
			}
		}
		return writer.Close()
	}
	compressor := gzip.NewWriter(output)
	writer := tar.NewWriter(compressor)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.data)), ModTime: archiveTime}
		if err := writer.WriteHeader(header); err != nil {
			_ = writer.Close()
			_ = compressor.Close()
			return err
		}
		if _, err := writer.Write(entry.data); err != nil {
			_ = writer.Close()
			_ = compressor.Close()
			return err
		}
	}
	if err := writer.Close(); err != nil {
		_ = compressor.Close()
		return err
	}
	return compressor.Close()
}

func writeNew(path string, data []byte) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			// 只清理由本次调用新建且写入失败的单个文件，不删除其他发行资产。
			_ = os.Remove(path)
		}
	}()
	_, err = file.Write(data)
	return err
}
