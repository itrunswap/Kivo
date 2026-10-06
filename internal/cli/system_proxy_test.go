package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/client"
	"github.com/itrunswap/Kivo/internal/platform"
)

func TestConnectionOptionsAreExplicitAndStrict(t *testing.T) {
	for _, args := range [][]string{{"--force"}, {"--adopt", "--replace"}, {"extra"}} {
		if _, err := parseConnectOptions(args, true); err == nil {
			t.Fatal(args)
		}
	}
	if _, err := parseConnectOptions([]string{"--no-check"}, false); err == nil {
		t.Fatal("on accepted no-check")
	}
	options, err := parseConnectOptions([]string{"--adopt", "--no-check"}, true)
	if err != nil || !options.Adopt || !options.SkipCheck || options.Replace {
		t.Fatal(options, err)
	}
}

func TestConnectionCommandsUseTypedAPI(t *testing.T) {
	var paths []string
	var bodies []map[string]bool
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" || r.URL.Path == "/api/v1/session/verify" {
			writeTestEnvelope(t, w, map[string]bool{"valid": true})
			return
		}
		paths = append(paths, r.URL.Path)
		if r.Method == "GET" {
			writeTestEnvelope(t, w, platform.SystemProxyStatus{State: "this_app", Supported: true, Managed: true, Message: "自动接入"})
			return
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing JSON content type")
		}
		var body map[string]bool
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		writeTestEnvelope(t, w, app.ProxyOperationResult{Message: "操作完成"})
	}))
	defer controller.Close()
	var out bytes.Buffer
	s := NewShell(client.New(strings.TrimPrefix(controller.URL, "http://"), ""), strings.NewReader(""), &out, controller.URL, "")
	for _, args := range [][]string{{"connect", "--replace", "--no-check"}, {"disconnect"}, {"system-proxy", "status"}, {"system-proxy", "on", "--adopt"}, {"system-proxy", "off"}, {"system-proxy", "recover", "--force"}} {
		if err := s.Execute(context.Background(), args); err != nil {
			t.Fatal(args, err)
		}
	}
	if len(paths) != 6 || paths[0] != "/api/v1/connection/connect" || paths[5] != "/api/v1/system-proxy/recover" || !bodies[0]["replace"] || !bodies[0]["skipCheck"] || !bodies[2]["adopt"] || !bodies[4]["force"] {
		t.Fatal(paths, bodies)
	}
	count := len(paths)
	for _, args := range [][]string{{"disconnect", "now"}, {"system-proxy", "status", "--force"}, {"system-proxy", "off", "--replace"}, {"system-proxy", "recover", "--replace"}} {
		if err := s.Execute(context.Background(), args); err == nil {
			t.Fatal("invalid arguments accepted", args)
		}
	}
	if len(paths) != count {
		t.Fatal("invalid arguments caused API mutation")
	}
}

func TestOfflineRecoveryDoesNotStartWeb(t *testing.T) {
	var out bytes.Buffer
	s := NewShell(client.New("127.0.0.1:1", ""), strings.NewReader(""), &out, "http://127.0.0.1:1", "")
	started, calls := 0, 0
	s.SetWebStarter(func(context.Context) error { started++; return nil })
	s.SetOfflineSystemProxy(func(_ context.Context, action string, force bool) (app.ProxyOperationResult, error) {
		calls++
		return app.ProxyOperationResult{Message: action}, nil
	})
	for _, args := range [][]string{{"system-proxy", "status"}, {"system-proxy", "off"}, {"system-proxy", "recover", "--force"}, {"disconnect"}} {
		if err := s.Execute(context.Background(), args); err != nil {
			t.Fatal(args, err)
		}
	}
	if calls != 4 || started != 0 {
		t.Fatal(calls, started)
	}
}
