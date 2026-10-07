package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutingAndGroupEditHTTP(t *testing.T) {
	controller, secret := newTestServer(t)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		controller.handleAPI(response, request)
		return response
	}
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPost, "/api/v1/subscription-groups", `{"name":"work"}`, 200},
		{http.MethodPatch, "/api/v1/subscription-groups", `{"name":"work","newName":"office"}`, 200},
		{http.MethodDelete, "/api/v1/subscription-groups?name=default", "", 400},
		{http.MethodDelete, "/api/v1/routing/profiles?name=global", "", 400},
		{http.MethodPost, "/api/v1/routing/groups", `{"name":"custom"}`, 200},
		{http.MethodPost, "/api/v1/routing/rules", `{"group":"custom","rule":{"type":"domain","value":"one.example","action":"proxy"}}`, 200},
		{http.MethodPatch, "/api/v1/routing/rules", `{"group":"custom","index":1,"expected":{"type":"domain","value":"one.example","action":"proxy"},"rule":{"type":"domain","value":"two.example","action":"direct"}}`, 200},
		{http.MethodDelete, "/api/v1/routing/rules?group=custom&index=1", `{"expected":{"type":"domain","value":"one.example","action":"proxy"}}`, 400},
		{http.MethodDelete, "/api/v1/routing/rules?group=custom&index=1", `{"expected":{"type":"domain","value":"two.example","action":"direct"}}`, 200},
	} {
		response := call(tc.method, tc.path, tc.body)
		if response.Code != tc.want {
			t.Fatalf("%s %s = %d, want %d: %s", tc.method, tc.path, response.Code, tc.want, response.Body.String())
		}
	}
}
