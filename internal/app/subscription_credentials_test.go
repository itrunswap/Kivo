package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itrunswap/Kivo/internal/config"
)

func TestLayeredSubscriptionPreservesSecretsAndRejectsConflicts(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(store, &fakeCore{})
	ctx := context.Background()
	err = s.AddSubscription(ctx, SubscriptionInput{URL: "https://example.com/private?token=url-secret",
		DownloadAuth: &config.SubscriptionAuth{Type: "basic", Username: "alice", Secret: " download-password "},
		Decryption:   &config.SubscriptionDecryption{Type: "aes", Secret: " decrypt-password "},
		Options:      config.SubscriptionOptions{UDP: "off", Filter: "香港|日本"},
	})
	if err != nil {
		t.Fatal(err)
	}
	public := s.ListSubscriptions()[0]
	data, _ := json.Marshal(public)
	for _, secret := range []string{"url-secret", "download-password", "decrypt-password", "alice"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("public subscription exposed credential")
		}
	}
	if public.DownloadAuthType != "basic" || public.DecryptionType != "aes" || public.Revision == "" {
		t.Fatal("missing separate credential metadata")
	}
	typeBasic, typeAES, typeNone := "basic", "aes", "none"
	patch := SubscriptionPatch{Reference: public.Name, Revision: public.Revision,
		DownloadAuth: &SubscriptionCredentialPatch{Type: &typeBasic}, Decryption: &SubscriptionCredentialPatch{Type: &typeAES}}
	if err = s.PatchSubscription(ctx, patch); err != nil {
		t.Fatal(err)
	}
	auth, decrypt := store.Snapshot().Subscriptions[0].Credentials()
	if auth.Secret != " download-password " || decrypt.Secret != " decrypt-password " {
		t.Fatal("blank edit changed secrets")
	}
	if err = s.PatchSubscription(ctx, SubscriptionPatch{Reference: public.Name, AuthType: &typeNone}); err == nil {
		t.Fatal("legacy write silently discarded a credential layer")
	}
	name := "renamed"
	if err = s.PatchSubscription(ctx, SubscriptionPatch{Reference: public.Name, Name: &name}); err != nil {
		t.Fatal(err)
	}
	patch.Reference = name
	if err = s.PatchSubscription(ctx, patch); err == nil {
		t.Fatal("stale revision accepted")
	}
	if err = s.PatchSubscription(ctx, SubscriptionPatch{Reference: name, DownloadAuth: &SubscriptionCredentialPatch{Type: &typeNone}}); err != nil {
		t.Fatal(err)
	}
	auth, decrypt = store.Snapshot().Subscriptions[0].Credentials()
	if auth.Secret != "" || decrypt.Secret != " decrypt-password " {
		t.Fatal("disabling HTTP auth changed decryption")
	}
	if err = s.PatchSubscription(ctx, SubscriptionPatch{Reference: name, Decryption: &SubscriptionCredentialPatch{Type: &typeNone}}); err != nil {
		t.Fatal(err)
	}
	_, decrypt = store.Snapshot().Subscriptions[0].Credentials()
	if decrypt.Secret != "" {
		t.Fatal("disabled decryption retained its secret")
	}
}

func TestLayeredAESDownloadUsesOnlyHTTPAuthAndCustomUserAgent(t *testing.T) {
	const password = " decrypt password "
	plain := []byte("proxies: []\n")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, secret, ok := r.BasicAuth()
		if !ok || user != "alice" || secret != "http-password" {
			t.Error("HTTP authentication missing")
		}
		if r.UserAgent() != "custom-client" {
			t.Error("custom User-Agent missing")
		}
		if strings.Contains(r.Header.Get("Authorization"), password) || r.Header.Get("X-Kivo-Internal") != "" {
			t.Error("internal/decryption credential was transmitted")
		}
		w.Write(encryptAESFixture(t, plain, password))
	}))
	defer backend.Close()
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(store, &fakeCore{})
	if err = s.AddSubscription(context.Background(), SubscriptionInput{Name: "layered", URL: backend.URL,
		DownloadAuth: &config.SubscriptionAuth{Type: "basic", Username: "alice", Secret: "http-password"},
		Decryption:   &config.SubscriptionDecryption{Type: "aes", Secret: password}, Options: config.SubscriptionOptions{UserAgent: "custom-client"},
	}); err != nil {
		t.Fatal(err)
	}
	content, err := s.DecryptedSubscriptionContent(context.Background(), "layered")
	if err != nil || string(content) != string(plain) {
		t.Fatalf("download/decode failed: %v", err)
	}
}

func TestAuthenticatedAESRedirectDoesNotForwardCredentialsToAnotherOrigin(t *testing.T) {
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer origin.Close()
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(store, &fakeCore{})
	if err = s.AddSubscription(context.Background(), SubscriptionInput{Name: "redirect", URL: origin.URL,
		DownloadAuth: &config.SubscriptionAuth{Type: "bearer", Secret: "private"}, Decryption: &config.SubscriptionDecryption{Type: "aes", Secret: "password"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DecryptedSubscriptionContent(context.Background(), "redirect"); err == nil || reached {
		t.Fatal("credential redirect was not blocked")
	}
}

func TestSubscriptionOptionValidationIsAtomic(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(store, &fakeCore{})
	for _, opts := range []config.SubscriptionOptions{{Filter: "("}, {ExcludeFilter: "(?=x)"}, {UserAgent: "client\r\nInjected: value"}, {UDP: "yes"}, {SkipCertVerify: "invalid"}} {
		if err = s.AddSubscription(context.Background(), SubscriptionInput{URL: "https://example.com/sub", Options: opts}); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	if len(store.Snapshot().Subscriptions) != 0 {
		t.Fatal("invalid operation persisted configuration")
	}
}
