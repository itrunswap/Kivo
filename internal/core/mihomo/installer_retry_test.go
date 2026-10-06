package mihomo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/itrunswap/Kivo/internal/core"
)

func TestDownloadRetriesAndResumesPartialFile(t *testing.T) {
	t.Parallel()
	content := []byte(strings.Repeat("mihomo-release-data", 2048))
	digest := sha256.Sum256(content)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := requests.Add(1)
		if attempt == 1 {
			w.Header().Set("Content-Length", fmt.Sprint(len(content)))
			_, _ = w.Write(content[:len(content)/2])
			return
		}
		expectedRange := fmt.Sprintf("bytes=%d-", len(content)/2)
		if r.Header.Get("Range") != expectedRange {
			t.Errorf("Range = %q, want %q", r.Header.Get("Range"), expectedRange)
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(content)-len(content)/2))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", len(content)/2, len(content)-1, len(content)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(content[len(content)/2:])
	}))
	defer server.Close()

	installer := NewInstaller(t.TempDir(), t.TempDir())
	installer.retries = 2
	path := filepath.Join(t.TempDir(), "mihomo.part")
	events := []core.InstallEvent{}
	got, err := installer.downloadWithRetry(context.Background(), asset{BrowserDownloadURL: server.URL, Size: int64(len(content))}, path, func(event core.InstallEvent) { events = append(events, event) })
	if err != nil {
		t.Fatal(err)
	}
	if got != hex.EncodeToString(digest[:]) {
		t.Fatalf("digest = %s", got)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
	if data, readErr := os.ReadFile(path); readErr != nil || string(data) != string(content) {
		t.Fatalf("resumed file mismatch: %v", readErr)
	}
	if !slices.ContainsFunc(events, func(e core.InstallEvent) bool { return e.Stage == "retry" }) {
		t.Fatalf("expected retry progress event: %#v", events)
	}
}
