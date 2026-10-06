package app

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5" //nolint:gosec // 测试兼容格式，与生产解码器保持一致。
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/itrunswap/Kivo/internal/config"
)

func TestDecryptedSubscriptionContentDownloadsAndDecryptsAES(t *testing.T) {
	const password = "compatibility-password"
	plaintext := []byte("vless://example-one\nss://example-two\n")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(encryptAESFixture(t, plaintext, password))
	}))
	defer backend.Close()

	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	if err := service.AddSubscription(context.Background(), SubscriptionInput{
		Name: "encrypted", URL: backend.URL, AuthType: "aes", Secret: password,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := service.DecryptedSubscriptionContent(context.Background(), "encrypted")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("decrypted content = %q, want %q", got, plaintext)
	}
}

func TestDecryptedSubscriptionContentCanUseMihomoProxy(t *testing.T) {
	const password = "compatibility-password"
	plaintext := []byte("vless://proxied-example\n")
	proxyReached := false
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyReached = true
		_, _ = w.Write(encryptAESFixture(t, plaintext, password))
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}

	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fixedProxyCore{fakeCore: &fakeCore{}, endpoint: proxyURL})
	if err := service.AddSubscription(context.Background(), SubscriptionInput{
		Name: "encrypted", URL: "http://subscription.invalid/private", AuthType: "aes", Secret: password, UpdateVia: "proxy",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := service.DecryptedSubscriptionContent(context.Background(), "encrypted")
	if err != nil {
		t.Fatal(err)
	}
	if !proxyReached || !bytes.Equal(got, plaintext) {
		t.Fatalf("proxyReached=%v decrypted=%q", proxyReached, got)
	}
}

type fixedProxyCore struct {
	*fakeCore
	endpoint *url.URL
}

func (c *fixedProxyCore) SubscriptionProxyURL(context.Context) (*url.URL, error) {
	return c.endpoint, nil
}

func TestAESProxyWithoutDedicatedCapabilityDoesNotFallBack(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	if err := service.AddSubscription(context.Background(), SubscriptionInput{
		Name: "encrypted", URL: "https://subscription.invalid/private", AuthType: "aes", Secret: "test", UpdateVia: "proxy",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.DecryptedSubscriptionContent(context.Background(), "encrypted"); err == nil || !strings.Contains(err.Error(), "不支持固定代理下载") {
		t.Fatalf("proxy must fail closed: %v", err)
	}
}

func TestDecryptedSubscriptionContentDoesNotLeakSourceURLInNetworkErrors(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	const marker = "sensitive-token-value"
	if err := service.AddSubscription(context.Background(), SubscriptionInput{
		Name: "encrypted", URL: "http://127.0.0.1:1/sub?token=" + marker, AuthType: "aes", Secret: "password",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.DecryptedSubscriptionContent(context.Background(), "encrypted")
	if err == nil {
		t.Fatal("expected network error")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("network error leaked source URL: %v", err)
	}
}

func encryptAESFixture(t *testing.T, plaintext []byte, password string) []byte {
	t.Helper()
	key := md5.Sum([]byte(password))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	padding := aes.BlockSize - len(plaintext)%aes.BlockSize
	padded := append(bytes.Clone(plaintext), bytes.Repeat([]byte{byte(padding)}, padding)...)
	iv := []byte("0123456789abcdef")
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	packed := append(bytes.Clone(iv), ciphertext...)
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(packed)))
	base64.StdEncoding.Encode(encoded, packed)
	return encoded
}
