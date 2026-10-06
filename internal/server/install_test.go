package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/core"
)

type streamingCore struct {
	stubCore
	resume <-chan struct{}
	fail   bool
}

func (c streamingCore) Install(ctx context.Context, _ string, emit func(core.InstallEvent)) error {
	emit(core.InstallEvent{Stage: "download", Downloaded: 10, Total: 100})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.resume:
	}
	if c.fail {
		return errors.New("test install error")
	}
	return nil
}

func TestInstallEndpointFlushesRejectsConcurrencyAndReportsErrors(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			controller, secret := newTestServer(t)
			resume := make(chan struct{})
			controller.service = app.NewService(controller.store, streamingCore{resume: resume, fail: fail})
			server := httptest.NewServer(controller.securityHeaders(controller.requestLog(http.HandlerFunc(controller.handleAPI))))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			request := func() *http.Request {
				r, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/v1/core/install", strings.NewReader(`{"version":"latest"}`))
				if err != nil {
					t.Fatal(err)
				}
				r.Header.Set("Authorization", "Bearer "+secret)
				r.Header.Set("Accept", "application/x-ndjson")
				return r
			}
			response, err := server.Client().Do(request())
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			decoder := json.NewDecoder(response.Body)
			for _, stage := range []string{"prepare", "download"} {
				var e core.InstallEvent
				if err := decoder.Decode(&e); err != nil {
					t.Fatal(err)
				}
				if e.Stage != stage {
					t.Fatalf("stage=%s", e.Stage)
				}
			}
			// 首次安装尚未完成，另一个入口必须明确报忙，不能同时写同一个 .part。
			second, err := server.Client().Do(request())
			if err != nil {
				t.Fatal(err)
			}
			second.Body.Close()
			if second.StatusCode != http.StatusConflict {
				t.Fatalf("concurrent status=%d", second.StatusCode)
			}
			close(resume)
			var last core.InstallEvent
			if err := decoder.Decode(&last); err != nil {
				t.Fatal(err)
			}
			if fail {
				if last.Error == "" || last.Stage != "error" {
					t.Fatalf("event=%+v", last)
				}
			} else if last.Stage != "complete" {
				t.Fatalf("event=%+v", last)
			}
		})
	}
}
