package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
)

func TestSubscriptionEditorHTTPRoundTrip(t *testing.T) {
	controller, key := newTestServer(t)
	request := func(method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1/subscriptions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		controller.handleAPI(w, req)
		return w
	}
	if w := request("POST", `{"url":"https://example.com/sub?token=private-address","downloadAuth":{"type":"basic","username":"alice","secret":"http-private"},"decryption":{"type":"aes","secret":"aes-private"},"options":{"udp":"off","filter":"香港|日本"}}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request("GET", "")
	var result struct {
		Data []app.PublicSubscription `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 {
		t.Fatal("subscription missing")
	}
	sub := result.Data[0]
	if sub.Name != "example.com" || sub.DownloadAuthType != "basic" || sub.DecryptionType != "aes" || sub.Options.UDP != "off" {
		t.Fatal("layered metadata lost")
	}
	for _, private := range []string{"private-address", "http-private", "aes-private", "alice"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("public API leaked credential")
		}
	}
	if w = request("PATCH", `{"reference":"example.com","revision":"outdated","name":"overwrite"}`); w.Code < 400 {
		t.Fatal("stale revision accepted")
	}
	if w = request("PATCH", `{"reference":"example.com","downloadAuth":{"type":"none"}}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	auth, decrypt := controller.store.Snapshot().Subscriptions[0].Credentials()
	if auth.Secret != "" || decrypt.Secret != "aes-private" {
		t.Fatal("HTTP PATCH affected wrong credential layer")
	}
}

type failedSubscriptionReloadCore struct{ stubCore }

func (failedSubscriptionReloadCore) Status(context.Context) core.Status {
	return core.Status{State: core.StateRunning}
}
func (failedSubscriptionReloadCore) Restart(context.Context) error {
	return errors.New("reload failed")
}

func TestSavedSubscriptionReloadFailureIsExplicitHTTPWarning(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewService(store, failedSubscriptionReloadCore{})
	controller := New(svc, store, nil)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions", strings.NewReader(`{"name":"saved","url":"https://example.com/sub"}`))
	r.Header.Set("Authorization", "Bearer "+store.Snapshot().Web.Secret)
	w := httptest.NewRecorder()
	controller.handleAPI(w, r)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), `"warning":true`) || len(store.Snapshot().Subscriptions) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
}
