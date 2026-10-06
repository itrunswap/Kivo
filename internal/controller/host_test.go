package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itrunswap/Kivo/internal/client"
	"github.com/itrunswap/Kivo/internal/config"
)

func TestEnsureReusesVerifiedBackend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.WriteHeader(200)
			return
		}
		if r.URL.Path != "/api/v1/session/verify" {
			t.Error(r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":{"valid":true}}`))
	}))
	defer srv.Close()
	if err := Ensure(context.Background(), client.New(strings.TrimPrefix(srv.URL, "http://"), ""), config.Paths{}); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureRejectsOccupiedUnauthenticatedBackend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/health" {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"wrong token"}}`))
	}))
	defer srv.Close()
	if err := Ensure(context.Background(), client.New(strings.TrimPrefix(srv.URL, "http://"), ""), config.Paths{}); err == nil {
		t.Fatal("accepted foreign backend")
	}
}
