package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
)

func TestSubscriptionPartialResponseAndTimeoutIsolation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"data":{"updated":["good"],"results":[{"name":"good","nodeCount":219},{"name":"bad","error":"unavailable"}]},"error":{"message":"partial failure"}}`))
	}))
	defer server.Close()
	c := New(strings.TrimPrefix(server.URL, "http://"), "")
	result, err := c.UpdateSubscriptions(context.Background(), app.SubscriptionUpdateInput{})
	if err == nil || len(result.Updated) != 1 || len(result.Results) != 2 || *result.Results[0].NodeCount != 219 {
		t.Fatalf("lost partial result: %+v %v", result, err)
	}
	if c.http.Timeout != 30*time.Second {
		t.Fatal("shared HTTP client timeout mutated")
	}
}
