package mihomo

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/core"
)

func TestDownloadRecoversCachedArchives(t *testing.T) {
	for _, scenario := range []string{"complete", "corrupt", "oversized", "no-digest", "416", "bad-range", "ignored-range"} {
		t.Run(scenario, func(t *testing.T) {
			content := "official archive content"
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
			cached := content
			switch scenario {
			case "corrupt":
				cached = strings.Repeat("x", len(content))
			case "oversized":
				cached += "stale"
			case "416", "bad-range", "ignored-range":
				cached = content[:5]
			}
			path := filepath.Join(t.TempDir(), "download.part")
			if err := os.WriteFile(path, []byte(cached), 0600); err != nil {
				t.Fatal(err)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if scenario == "complete" {
					t.Error("complete verified cache must not access network")
				}
				if r.Header.Get("Range") != "" {
					switch scenario {
					case "416":
						w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
						return
					case "bad-range":
						w.Header().Set("Content-Range", "bytes 0-2/3")
						w.WriteHeader(206)
						return
					case "ignored-range": // 服务器可合法忽略 Range，返回完整内容。
					default:
						t.Errorf("unexpected Range: %s", r.Header.Get("Range"))
					}
				}
				fmt.Fprint(w, content)
			}))
			defer server.Close()
			item := asset{BrowserDownloadURL: server.URL, Size: int64(len(content)), Digest: "sha256:" + digest}
			if scenario == "no-digest" {
				item.Digest = ""
			}
			got, err := NewInstaller(t.TempDir(), t.TempDir()).download(context.Background(), item, path, nil)
			if err != nil || got != digest {
				t.Fatalf("digest=%s err=%v", got, err)
			}
			expected := 1
			if scenario == "complete" {
				expected = 0
			}
			if scenario == "416" || scenario == "bad-range" {
				expected = 2
			}
			if requests != expected {
				t.Fatalf("requests=%d, want %d", requests, expected)
			}
		})
	}
}

func TestZipOfficialNamesAndAmbiguity(t *testing.T) {
	for _, names := range [][]string{{"mihomo-windows-amd64-v1.exe"}, {"mihomo-windows-arm64.exe"}, {"mihomo.exe"}, {"nested/mihomo.exe"}, {"README.txt"}, {"mihomo.exe", "mihomo-windows-amd64-v1.exe"}} {
		t.Run(strings.Join(names, "+"), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "core.zip")
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			writer := zip.NewWriter(f)
			for _, name := range names {
				entry, err := writer.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(entry, "binary"); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			f.Close()
			output := filepath.Join(dir, "installed")
			err = extractBinary(path, "core.zip", output)
			wantErr := names[0] == "README.txt" || len(names) > 1
			if (err != nil) != wantErr {
				t.Fatalf("extract err=%v", err)
			}
			if !wantErr {
				data, err := os.ReadFile(output)
				if err != nil || string(data) != "binary" {
					t.Fatalf("wrong binary %q: %v", data, err)
				}
			}
		})
	}
}

func TestDownloadReporterStreamsBeforeCompletion(t *testing.T) {
	var events []core.InstallEvent
	r := downloadReporter{progress: func(e core.InstallEvent) { events = append(events, e) }, offset: 10, total: 100, started: time.Now().Add(-time.Second)}
	r.Write([]byte("chunk"))
	if len(events) != 1 || events[0].Downloaded != 15 || events[0].BytesPerSecond <= 0 {
		t.Fatalf("events=%+v", events)
	}
	r.Write([]byte("next"))
	if len(events) != 1 {
		t.Fatal("fast writes should be throttled")
	}
	r.emit(true)
	if events[1].Downloaded != 19 {
		t.Fatal("final bytes missing")
	}
}

// TestOfficialCachedWindowsArchive 仅在显式传入官方缓存时执行。用户原件只读，
// 解压和版本探测均在测试临时目录中，不安装到用户数据目录，不启动代理服务。
func TestOfficialCachedWindowsArchive(t *testing.T) {
	path := os.Getenv("KIVO_TEST_ARCHIVE")
	if path == "" || runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		t.Skip("requires official Windows amd64 archive")
	}
	const digest = "d89c9bd746e8aacff89b2edf674813e25e8bd2dc565f4e12dc3b4526dd2b3177"
	// 只对临时副本执行下载恢复，任何失败都不得重置或删掉用户的原始缓存。
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	path = filepath.Join(t.TempDir(), "official.part")
	copy, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(copy, source); err != nil {
		copy.Close()
		t.Fatal(err)
	}
	copy.Close()
	i := NewInstaller(t.TempDir(), t.TempDir())
	got, err := i.download(context.Background(), asset{Size: 22460787, Digest: "sha256:" + digest, BrowserDownloadURL: "http://127.0.0.1:1/no-network-expected"}, path, nil)
	if err != nil || got != digest {
		t.Fatalf("official cache verification: %s %v", got, err)
	}
	binary := filepath.Join(t.TempDir(), "mihomo.exe")
	if err := extractBinary(path, "mihomo-windows-amd64-v1-v1.19.31.zip", binary); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-v")
	cmd.Dir = filepath.Dir(binary)
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "v1.19.31") {
		t.Fatalf("version: %s %v", output, err)
	}
	t.Log(strings.TrimSpace(string(output)))
}
