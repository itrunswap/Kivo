package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/core"
)

func TestInstallStreamsBeforeRequestCompletes(t *testing.T) {
	received := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/x-ndjson" || r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing headers")
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		fmt.Fprintln(w, `{"stage":"download","downloaded":10,"total":100}`)
		w.(http.Flusher).Flush()
		select {
		case <-received:
		case <-r.Context().Done():
			return
		}
		fmt.Fprintln(w, `{"stage":"complete"}`)
	}))
	defer server.Close()
	c := New(strings.TrimPrefix(server.URL, "http://"), "test-token")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := c.InstallCoreProgress(ctx, "latest", func(e core.InstallEvent) {
		if e.Stage == "download" {
			close(received)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.http.Timeout != 30*time.Second {
		t.Fatal("installation must not alter shared client timeout")
	}
}

func TestInstallNeverTreatsTruncationOrErrorsAsSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, body, contentType string
		status                  int
		wantErr                 bool
	}{
		{"truncated", `{"stage":"download"}`, "application/x-ndjson", 200, true},
		{"activation-not-complete", `{"stage":"done"}`, "application/x-ndjson", 200, true},
		{"failed", `{"stage":"error","error":"checksum failed"}`, "application/x-ndjson", 200, true},
		{"bad-json", `oops`, "application/x-ndjson", 200, true},
		{"unauthorized", `{"error":{"message":"unauthorized"}}`, "application/json", 401, true},
		{"legacy-success", `{"data":[]}`, "application/json", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer s.Close()
			c := New(strings.TrimPrefix(s.URL, "http://"), "")
			if err := c.InstallCoreProgress(context.Background(), "latest", nil); (err != nil) != tc.wantErr {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
