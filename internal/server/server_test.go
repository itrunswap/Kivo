package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
)

// stubCore 只为 HTTP 边界测试提供稳定状态，避免测试依赖真实 Mihomo 进程。
type stubCore struct{}

func (stubCore) Install(context.Context, string, func(core.InstallEvent)) error { return nil }
func (stubCore) Start(context.Context) error                                    { return nil }
func (stubCore) Stop(context.Context) error                                     { return nil }
func (stubCore) Restart(context.Context) error                                  { return nil }
func (stubCore) Status(context.Context) core.Status {
	return core.Status{Name: "mihomo", State: core.StateStopped, MixedPort: 17890, Mode: "rule"}
}
func (stubCore) ListNodes(context.Context) ([]core.Node, error)    { return nil, nil }
func (stubCore) TestNodes(context.Context) (map[string]int, error) { return nil, nil }
func (stubCore) SelectNode(context.Context, string) error          { return nil }
func (stubCore) UpdateSubscription(context.Context, string) error  { return nil }
func (stubCore) TestSubscription(context.Context, string) error    { return nil }
func (stubCore) SetMode(context.Context, string) error             { return nil }
func (stubCore) Logs(int) []string                                 { return nil }

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	paths, err := config.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	secret := store.Snapshot().Web.Secret
	service := app.NewService(store, stubCore{})
	return New(service, store, nil), secret
}

func TestHealthDoesNotRequireAuthorization(t *testing.T) {
	controller, _ := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	response := httptest.NewRecorder()

	controller.securityHeaders(http.HandlerFunc(controller.handleAPI)).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Security-Policy"); got == "" {
		t.Fatal("missing Content-Security-Policy header")
	}
}

func TestConnectivityAPIsRequireAuthAndGuideIsReadOnly(t *testing.T) {
	controller, secret := newTestServer(t)
	for _, tc := range []struct{ method, path string }{{http.MethodPost, "/api/v1/connectivity/check"}, {http.MethodGet, "/api/v1/proxy/setup"}} {
		response := httptest.NewRecorder()
		controller.handleAPI(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s = %d", tc.path, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/proxy/setup", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	controller.handleAPI(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "127.0.0.1") || !strings.Contains(response.Body.String(), "不会自动还原") {
		t.Fatal(response.Code, response.Body.String())
	}
	// GET 不能触发联网检测；鉴权后的错误方法也只返回路由错误。
	request = httptest.NewRequest(http.MethodGet, "/api/v1/connectivity/check", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response = httptest.NewRecorder()
	controller.handleAPI(response, request)
	if response.Code == http.StatusOK {
		t.Fatal("GET must not trigger connectivity requests")
	}
}

func TestOverviewRequiresBearerTokenAndUsesRuntimeListen(t *testing.T) {
	controller, secret := newTestServer(t)
	controller.listen = "0.0.0.0:19099"

	unauthorized := httptest.NewRecorder()
	controller.handleAPI(unauthorized, httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.Code, http.StatusUnauthorized)
	}
	malformed := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	malformed.Header.Set("Authorization", "Bearer"+secret)
	malformedResponse := httptest.NewRecorder()
	controller.handleAPI(malformedResponse, malformed)
	if malformedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("malformed authorization status = %d, want %d", malformedResponse.Code, http.StatusUnauthorized)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	controller.handleAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", response.Code, http.StatusOK)
	}
	var body struct {
		Data app.Overview `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Data.WebAddress != controller.listen {
		t.Fatalf("webAddress = %q, want %q", body.Data.WebAddress, controller.listen)
	}
}

func TestInternalSubscriptionEndpointRequiresControllerKey(t *testing.T) {
	controller, webSecret := newTestServer(t)
	controllerKey := controller.store.Snapshot().Mihomo.ControllerKey

	withoutKey := httptest.NewRequest(http.MethodGet, "/api/v1/internal/subscription-content?name=missing", nil)
	withoutKey.Header.Set("Authorization", "Bearer "+webSecret)
	withoutKeyResponse := httptest.NewRecorder()
	controller.handleAPI(withoutKeyResponse, withoutKey)
	if withoutKeyResponse.Code != http.StatusUnauthorized {
		t.Fatalf("Web token must not authorize internal endpoint: status=%d", withoutKeyResponse.Code)
	}

	withKey := httptest.NewRequest(http.MethodGet, "/api/v1/internal/subscription-content?name=missing", nil)
	withKey.Header.Set("X-Kivo-Internal", controllerKey)
	withKeyResponse := httptest.NewRecorder()
	controller.handleAPI(withKeyResponse, withKey)
	if withKeyResponse.Code == http.StatusUnauthorized {
		t.Fatal("controller key should authorize internal endpoint")
	}
	legacy := httptest.NewRequest(http.MethodGet, "/api/v1/internal/subscription-content?name=missing", nil)
	legacy.Header.Set("X-ProxyPilot-Internal", controllerKey)
	legacyResponse := httptest.NewRecorder()
	controller.handleAPI(legacyResponse, legacy)
	if legacyResponse.Code == http.StatusUnauthorized {
		t.Fatal("rename must not reject an existing provider's internal header")
	}
	// 同时提供两个头时，以新头为准，不能借旧头绕过错误的新凭据。
	legacy.Header.Set("X-Kivo-Internal", "wrong-key")
	conflict := httptest.NewRecorder()
	controller.handleAPI(conflict, legacy)
	if conflict.Code != http.StatusUnauthorized {
		t.Fatal("legacy header must not override invalid new authorization")
	}
}

func TestUpdateSubscriptionsDefaultsToAllAndAcceptsRoute(t *testing.T) {
	controller, secret := newTestServer(t)
	if err := controller.service.AddSubscription(context.Background(), app.SubscriptionInput{
		Name: "primary", URL: "https://example.com/sub",
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/update", strings.NewReader(`{"via":"direct"}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	controller.handleAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Data app.SubscriptionUpdateResult `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Updated) != 1 || body.Data.Updated[0] != "primary" || !body.Data.TemporarilyStartedCore {
		t.Fatalf("unexpected update result: %#v", body.Data)
	}
}

func TestSubscriptionHealthCheckAcceptsDirectRouteAndDefaultsToAll(t *testing.T) {
	controller, secret := newTestServer(t)
	if err := controller.service.AddSubscription(context.Background(), app.SubscriptionInput{
		Name: "primary", URL: "https://example.com/sub", UpdateVia: "proxy",
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/subscriptions/test", strings.NewReader(`{"via":"direct"}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	controller.handleAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("test status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Data app.SubscriptionTestResult `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Tested) != 1 || body.Data.Tested[0] != "primary" || !body.Data.TemporarilyStartedCore {
		t.Fatalf("unexpected test result: %#v", body.Data)
	}
}

func TestValidateListenAcceptsOptionalAuthentication(t *testing.T) {
	if err := validateListen("127.0.0.1:9099"); err != nil {
		t.Fatalf("loopback listen should be valid: %v", err)
	}
	if err := validateListen("0.0.0.0:9099"); err != nil {
		t.Fatalf("non-loopback listen should be valid: %v", err)
	}
}

func TestWebTokenRotationAndDisableTakeEffectImmediately(t *testing.T) {
	controller, originalSecret := newTestServer(t)

	rotate := httptest.NewRequest(http.MethodPatch, "/api/v1/web/security", strings.NewReader(`{"token":"replacement-token"}`))
	rotate.Header.Set("Authorization", "Bearer "+originalSecret)
	rotateResponse := httptest.NewRecorder()
	controller.handleAPI(rotateResponse, rotate)
	if rotateResponse.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, body = %s", rotateResponse.Code, rotateResponse.Body.String())
	}

	oldRequest := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	oldRequest.Header.Set("Authorization", "Bearer "+originalSecret)
	oldResponse := httptest.NewRecorder()
	controller.handleAPI(oldResponse, oldRequest)
	if oldResponse.Code != http.StatusUnauthorized {
		t.Fatalf("old token status = %d, want %d", oldResponse.Code, http.StatusUnauthorized)
	}

	disable := httptest.NewRequest(http.MethodPatch, "/api/v1/web/security", strings.NewReader(`{"token":""}`))
	disable.Header.Set("Authorization", "Bearer replacement-token")
	disableResponse := httptest.NewRecorder()
	controller.handleAPI(disableResponse, disable)
	if disableResponse.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %s", disableResponse.Code, disableResponse.Body.String())
	}

	openRequest := httptest.NewRequest(http.MethodGet, "/api/v1/overview", nil)
	openResponse := httptest.NewRecorder()
	controller.handleAPI(openResponse, openRequest)
	if openResponse.Code != http.StatusOK {
		t.Fatalf("tokenless status = %d, want %d", openResponse.Code, http.StatusOK)
	}
}

func TestSubscriptionGroupsAndRoutingEndpoints(t *testing.T) {
	controller, secret := newTestServer(t)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		controller.handleAPI(response, request)
		return response
	}
	if response := call(http.MethodPost, "/api/v1/subscription-groups", `{"name":"work"}`); response.Code != http.StatusOK {
		t.Fatalf("create group status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/routing/groups", `{"name":"work-rules"}`); response.Code != http.StatusOK {
		t.Fatalf("create rule group status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/routing/rules", `{"group":"work-rules","rule":{"type":"domain-suffix","value":"github.com","action":"proxy"}}`); response.Code != http.StatusOK {
		t.Fatalf("add rule status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/routing/profiles", `{"name":"work","defaultAction":"direct","groups":["work-rules"]}`); response.Code != http.StatusOK {
		t.Fatalf("create profile status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/api/v1/routing/profiles/use", `{"name":"work"}`); response.Code != http.StatusOK {
		t.Fatalf("use profile status=%d body=%s", response.Code, response.Body.String())
	}
}
