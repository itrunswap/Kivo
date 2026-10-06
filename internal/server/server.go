// Package server 提供嵌入式 Web 控制台和版本化 HTTP API。
package server

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/itrunswap/Kivo/internal/app"
	"github.com/itrunswap/Kivo/internal/config"
	"github.com/itrunswap/Kivo/internal/core"
)

//go:embed assets/*
var assets embed.FS

// EmbeddedAssets 返回只读 Web 资源，桌面端复用同一份离线字体与许可文件。
func EmbeddedAssets() fs.FS {
	result, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	} // 嵌入路径由编译期保证，不依赖外部文件。
	return result
}

// Server 是 Kivo 本地控制服务器。
type Server struct {
	service   *app.Service
	store     *config.Store
	logger    *slog.Logger
	http      *http.Server
	listen    string
	ready     chan struct{}
	shutdown  chan struct{}
	installMu sync.Mutex // 同一安装缓存不允许 CLI/Web 并发覆盖。
}

// New 创建控制服务器。
func New(service *app.Service, store *config.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{service: service, store: store, logger: logger, ready: make(chan struct{}), shutdown: make(chan struct{}, 1)}
}

// Ready 在 HTTP 监听器建立后关闭。调用方可据此保证依赖本地 API 的 Mihomo provider
// 不会早于控制服务启动，避免系统开机恢复时出现瞬时连接失败。
func (s *Server) Ready() <-chan struct{} { return s.ready }

// ListenAddress 返回本次进程实际使用的监听地址，包含 serve --listen 的临时覆盖值。
func (s *Server) ListenAddress() string { return s.listen }

// Run 启动服务器并在 context 取消时优雅退出。
func (s *Server) Run(ctx context.Context, listenOverride string) error {
	cfg := s.store.Snapshot()
	listen := cfg.Web.Listen
	if strings.TrimSpace(listenOverride) != "" {
		listen = listenOverride
	}
	if err := validateListen(listen); err != nil {
		return err
	}
	if !listenIsLoopback(listen) && strings.TrimSpace(cfg.Web.Secret) == "" {
		s.logger.Warn("Web 控制台正在无 Token 模式下监听非回环地址，请仅在可信网络使用", "address", listen)
	}
	// 保存本次真正使用的监听地址。serve --listen 只覆盖当前进程，不污染持久配置，
	// 但概览页仍应显示可以实际访问的地址。
	s.listen = listen
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", s.handleAPI)
	mux.HandleFunc("/", s.handleAsset)

	s.http = &http.Server{
		Addr:              listen,
		Handler:           s.securityHeaders(s.requestLog(mux)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      20 * time.Minute, // 内核下载请求允许较长时间。
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	listener, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("监听控制台 %s: %w", listen, err)
	}
	s.logger.Info("Kivo 控制台已启动", "address", "http://"+listen)
	close(s.ready)

	errCh := make(chan error, 1)
	go func() {
		if err := s.http.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
	case <-s.shutdown:
	case err := <-errCh:
		if err != nil {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return s.http.Shutdown(shutdownCtx)
}

func validateListen(address string) error {
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("控制台监听地址无效: %w", err)
	}
	return nil
}

func listenIsLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/v1/health" {
		writeData(w, http.StatusOK, map[string]any{"status": "ok", "time": time.Now().UTC()})
		return
	}
	if r.URL.Path == "/api/v1/internal/subscription-content" {
		s.handleInternalSubscriptionContent(w, r)
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "API 密钥无效")
		return
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodDelete {
		if !sameOriginMutation(w, r) {
			return
		}
	}
	if r.URL.Path == "/api/v1/session/verify" && r.Method == http.MethodPost {
		writeData(w, http.StatusOK, map[string]bool{"valid": true})
		return
	}

	switch {
	case r.URL.Path == "/api/v1/system-proxy" && r.Method == http.MethodGet:
		writeData(w, http.StatusOK, s.service.SystemProxyStatus(r.Context()))
	case (strings.HasPrefix(r.URL.Path, "/api/v1/connection/") || strings.HasPrefix(r.URL.Path, "/api/v1/system-proxy/")) && r.Method == http.MethodPost:
		s.handleSystemProxyAction(w, r)
	case r.URL.Path == "/api/v1/connectivity/check" && r.Method == http.MethodPost:
		report, err := s.service.CheckConnectivity(r.Context())
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeData(w, http.StatusOK, report)
	case r.URL.Path == "/api/v1/proxy/setup" && r.Method == http.MethodGet:
		writeData(w, http.StatusOK, s.service.ProxySetup())
	case r.URL.Path == "/api/v1/overview" && r.Method == http.MethodGet:
		overview := s.service.Overview(r.Context())
		if s.listen != "" {
			overview.WebAddress = s.listen
		}
		writeData(w, http.StatusOK, overview)
	case r.URL.Path == "/api/v1/subscriptions" && r.Method == http.MethodGet:
		writeData(w, http.StatusOK, s.service.ListSubscriptions())
	case r.URL.Path == "/api/v1/subscriptions" && r.Method == http.MethodPost:
		s.handleAddSubscription(w, r)
	case r.URL.Path == "/api/v1/subscriptions" && r.Method == http.MethodPatch:
		s.handlePatchSubscription(w, r)
	case r.URL.Path == "/api/v1/subscriptions" && r.Method == http.MethodDelete:
		s.handleRemoveSubscription(w, r)
	case r.URL.Path == "/api/v1/subscriptions/update" && r.Method == http.MethodPost:
		s.handleUpdateSubscription(w, r)
	case r.URL.Path == "/api/v1/subscriptions/test" && r.Method == http.MethodPost:
		s.handleTestSubscription(w, r)
	case r.URL.Path == "/api/v1/nodes" && r.Method == http.MethodGet:
		nodes, err := s.service.ListNodes(r.Context())
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeData(w, http.StatusOK, nodes)
	case r.URL.Path == "/api/v1/nodes/test" && r.Method == http.MethodPost:
		result, err := s.service.TestNodes(r.Context())
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeData(w, http.StatusOK, result)
	case r.URL.Path == "/api/v1/nodes/select" && r.Method == http.MethodPost:
		s.handleSelectNode(w, r)
	case r.URL.Path == "/api/v1/core/install" && r.Method == http.MethodPost:
		s.handleInstall(w, r)
	case r.URL.Path == "/api/v1/core/status" && r.Method == http.MethodGet:
		writeData(w, http.StatusOK, s.service.CoreStatus(r.Context()))
	case r.URL.Path == "/api/v1/core/installations" && r.Method == http.MethodGet:
		items, err := s.service.CoreInstallations()
		if err != nil {
			writeAppError(w, err)
			return
		}
		writeData(w, http.StatusOK, items)
	case r.URL.Path == "/api/v1/core/installations" && r.Method == http.MethodDelete:
		s.handleRemoveCore(w, r)
	case r.URL.Path == "/api/v1/core/import" && r.Method == http.MethodPost:
		s.handleCoreReferenceAction(w, r, "import")
	case r.URL.Path == "/api/v1/core/use" && r.Method == http.MethodPost:
		s.handleCoreReferenceAction(w, r, "use")
	case strings.HasPrefix(r.URL.Path, "/api/v1/core/") && r.Method == http.MethodPost:
		s.handleCoreAction(w, r)
	case r.URL.Path == "/api/v1/subscription-groups" && r.Method == http.MethodGet:
		writeData(w, http.StatusOK, s.service.SubscriptionGroups())
	case r.URL.Path == "/api/v1/subscription-groups" && r.Method == http.MethodPost:
		s.handleSubscriptionGroup(w, r, "create")
	case r.URL.Path == "/api/v1/subscription-groups" && r.Method == http.MethodDelete:
		if err := s.service.RemoveSubscriptionGroup(r.Context(), r.URL.Query().Get("name")); err != nil {
			writeAppError(w, err)
			return
		}
		writeData(w, http.StatusOK, map[string]bool{"removed": true})
	case r.URL.Path == "/api/v1/subscription-groups/action" && r.Method == http.MethodPost:
		s.handleSubscriptionGroup(w, r, "action")
	case r.URL.Path == "/api/v1/routing" && r.Method == http.MethodGet:
		writeData(w, http.StatusOK, s.service.Routing())
	case r.URL.Path == "/api/v1/routing/profiles/use" && r.Method == http.MethodPost:
		s.handleRouting(w, r, "use-profile")
	case r.URL.Path == "/api/v1/routing/profiles" && r.Method == http.MethodPost:
		s.handleRouting(w, r, "create-profile")
	case r.URL.Path == "/api/v1/routing/profiles" && r.Method == http.MethodPatch:
		s.handleRouting(w, r, "attach-profile-group")
	case r.URL.Path == "/api/v1/routing/profiles" && r.Method == http.MethodDelete:
		if err := s.service.DeleteRouteProfile(r.URL.Query().Get("name")); err != nil {
			writeAppError(w, err)
			return
		}
		writeData(w, http.StatusOK, map[string]bool{"removed": true})
	case r.URL.Path == "/api/v1/routing/groups" && r.Method == http.MethodPost:
		s.handleRouting(w, r, "create-group")
	case r.URL.Path == "/api/v1/routing/groups" && r.Method == http.MethodDelete:
		if err := s.service.DeleteRuleGroup(r.URL.Query().Get("name")); err != nil {
			writeAppError(w, err)
			return
		}
		writeData(w, http.StatusOK, map[string]bool{"removed": true})
	case r.URL.Path == "/api/v1/routing/rules" && r.Method == http.MethodPost:
		s.handleRouting(w, r, "add-rule")
	case r.URL.Path == "/api/v1/routing/rules" && r.Method == http.MethodDelete:
		index, _ := strconv.Atoi(r.URL.Query().Get("index"))
		if err := s.service.RemoveRouteRule(r.Context(), r.URL.Query().Get("group"), index); err != nil {
			writeAppError(w, err)
			return
		}
		writeData(w, http.StatusOK, map[string]bool{"removed": true})
	case r.URL.Path == "/api/v1/settings" && r.Method == http.MethodGet:
		writeData(w, http.StatusOK, s.service.GetSettings())
	case r.URL.Path == "/api/v1/settings" && r.Method == http.MethodPatch:
		s.handleSettings(w, r)
	case r.URL.Path == "/api/v1/web/security" && r.Method == http.MethodGet:
		writeData(w, http.StatusOK, s.service.GetWebSecurity())
	case r.URL.Path == "/api/v1/web/security" && r.Method == http.MethodPatch:
		s.handleWebSecurity(w, r)
	case r.URL.Path == "/api/v1/logs" && r.Method == http.MethodGet:
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit <= 0 || limit > 2000 {
			limit = 300
		}
		writeData(w, http.StatusOK, s.service.Logs(limit))
	case r.URL.Path == "/api/v1/doctor" && r.Method == http.MethodGet:
		writeData(w, http.StatusOK, s.service.Doctor(r.Context()))
	case r.URL.Path == "/api/v1/controller/shutdown" && r.Method == http.MethodPost:
		if !safeProxyRequest(w, r) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := s.service.SafeShutdown(ctx); err != nil {
			writeAppError(w, err)
			return
		}
		status := s.service.SystemProxyStatus(ctx)
		warning := status.RecoveryPending || status.State == "this_app"
		message := "后台正在关闭，受管代理设置已处理"
		if status.RecoveryPending {
			message = "后台正在关闭；存在未恢复代理备份，保留外部修改，请稍后 /system-proxy recover"
		} else if status.State == "this_app" {
			message = "后台正在关闭；未接管的手动代理仍指向本程序，请手动恢复，避免断网"
		}
		writeData(w, http.StatusAccepted, map[string]any{"status": "shutting_down", "message": message, "warning": warning, "systemProxy": status})
		select {
		case s.shutdown <- struct{}{}:
		default:
		}
	default:
		writeError(w, http.StatusNotFound, "not_found", "接口不存在")
	}
}

// 系统设置写入只接受 JSON 和同源浏览器请求。Token 可以为空，但不能允许恶意网页
// 借浏览器向本地服务提交表单，悄悄改写用户代理；无 Origin 的本地 CLI 请求仍可使用。
func safeProxyRequest(w http.ResponseWriter, r *http.Request) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "系统代理操作必须提交 application/json")
		return false
	}
	return sameOriginMutation(w, r)
}

func sameOriginMutation(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
		writeError(w, http.StatusForbidden, "invalid_origin", "拒绝跨站系统代理操作")
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		writeError(w, http.StatusForbidden, "invalid_origin", "拒绝跨站系统代理操作")
		return false
	}
	return true
}

func (s *Server) handleSystemProxyAction(w http.ResponseWriter, r *http.Request) {
	if !safeProxyRequest(w, r) {
		return
	}
	var input struct {
		Replace   bool `json:"replace"`
		Adopt     bool `json:"adopt"`
		SkipCheck bool `json:"skipCheck"`
		Force     bool `json:"force"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeAppError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	options := app.ConnectOptions{Replace: input.Replace, Adopt: input.Adopt, SkipCheck: input.SkipCheck}
	var result app.ProxyOperationResult
	var err error
	switch r.URL.Path {
	case "/api/v1/connection/connect":
		if input.Force {
			writeAppError(w, errors.New("连接不接受 force"))
			return
		}
		result, err = s.service.Connect(ctx, options)
	case "/api/v1/system-proxy/on":
		if input.Force || input.SkipCheck {
			writeAppError(w, errors.New("接入系统代理不接受 force/skipCheck"))
			return
		}
		result, err = s.service.EnableSystemProxy(ctx, options)
	case "/api/v1/connection/disconnect", "/api/v1/system-proxy/off", "/api/v1/system-proxy/recover":
		if input.Adopt || input.Replace || input.SkipCheck || (input.Force && r.URL.Path != "/api/v1/system-proxy/recover") {
			writeAppError(w, errors.New("操作参数不适用"))
			return
		}
		if r.URL.Path == "/api/v1/connection/disconnect" {
			result, err = s.service.Disconnect(ctx)
		} else {
			result, err = s.service.RestoreSystemProxy(ctx, input.Force)
		}
	default:
		writeError(w, http.StatusNotFound, "not_found", "接口不存在")
		return
	}
	if err != nil {
		writeResultError(w, result, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

// handleInternalSubscriptionContent 为 Mihomo 提供已经解密的订阅内容。该接口
// 使用独立的 Controller 密钥，不受用户是否关闭 Web Token 鉴权的影响。
func (s *Server) handleInternalSubscriptionContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "仅支持 GET")
		return
	}
	if !s.authorizedInternal(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "内部订阅密钥无效")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	content, err := s.service.DecryptedSubscriptionContent(ctx, r.URL.Query().Get("name"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *Server) handleAddSubscription(w http.ResponseWriter, r *http.Request) {
	var input app.SubscriptionInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.service.AddSubscription(ctx, input); err != nil {
		writeAppError(w, err)
		return
	}
	writeData(w, http.StatusCreated, map[string]string{"name": input.Name})
}

func (s *Server) handlePatchSubscription(w http.ResponseWriter, r *http.Request) {
	var input app.SubscriptionPatch
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.service.PatchSubscription(ctx, input); err != nil {
		writeAppError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]bool{"updated": true})
}

func (s *Server) handleRemoveCore(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var err error
	if r.URL.Query().Get("all") == "true" {
		err = s.service.PurgeCores(ctx)
	} else {
		err = s.service.RemoveCore(ctx, r.URL.Query().Get("reference"))
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]bool{"removed": true})
}

func (s *Server) handleCoreReferenceAction(w http.ResponseWriter, r *http.Request, action string) {
	var input map[string]string
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	var err error
	if action == "import" {
		err = s.service.ImportCore(ctx, input["path"])
	} else {
		err = s.service.UseCore(ctx, input["reference"])
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeData(w, http.StatusOK, s.service.CoreStatus(ctx))
}

func (s *Server) handleSubscriptionGroup(w http.ResponseWriter, r *http.Request, operation string) {
	var input map[string]string
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var err error
	if operation == "create" {
		err = s.service.CreateSubscriptionGroup(input["name"])
	} else {
		switch input["action"] {
		case "use":
			err = s.service.SetSubscriptionGroup(r.Context(), input["name"], true, true)
		case "enable":
			err = s.service.SetSubscriptionGroup(r.Context(), input["name"], true, false)
		case "disable":
			err = s.service.SetSubscriptionGroup(r.Context(), input["name"], false, false)
		default:
			err = errors.New("分组操作必须是 use、enable 或 disable")
		}
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeData(w, http.StatusOK, s.service.SubscriptionGroups())
}

func (s *Server) handleRouting(w http.ResponseWriter, r *http.Request, operation string) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var err error
	switch operation {
	case "use-profile":
		var input map[string]string
		if decodeErr := decodeJSON(r, &input); decodeErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		err = s.service.UseRouteProfile(ctx, input["name"])
	case "create-profile":
		var input config.RouteProfile
		if decodeErr := decodeJSON(r, &input); decodeErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		err = s.service.CreateRouteProfile(input.Name, input.DefaultAction, input.Groups)
	case "attach-profile-group":
		var input struct {
			Profile  string `json:"profile"`
			Group    string `json:"group"`
			Attached bool   `json:"attached"`
		}
		if decodeErr := decodeJSON(r, &input); decodeErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		err = s.service.SetRouteProfileGroup(ctx, input.Profile, input.Group, input.Attached)
	case "create-group":
		var input map[string]string
		if decodeErr := decodeJSON(r, &input); decodeErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		err = s.service.CreateRuleGroup(input["name"])
	case "add-rule":
		var input struct {
			Group string           `json:"group"`
			Rule  config.RouteRule `json:"rule"`
		}
		if decodeErr := decodeJSON(r, &input); decodeErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		err = s.service.AddRouteRule(ctx, input.Group, input.Rule)
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeData(w, http.StatusOK, s.service.Routing())
}

func (s *Server) handleRemoveSubscription(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "缺少订阅名称")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.service.RemoveSubscriptionReference(ctx, name); err != nil {
		writeAppError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"removed": name})
}

func (s *Server) handleUpdateSubscription(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name      string `json:"name,omitempty"` // 兼容旧客户端。
		Reference string `json:"reference,omitempty"`
		Group     string `json:"group,omitempty"`
		Via       string `json:"via,omitempty"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.Reference == "" {
		input.Reference = input.Name
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	result, err := s.service.UpdateSubscriptions(ctx, app.SubscriptionUpdateInput{
		Reference: input.Reference, Group: input.Group, Via: input.Via,
	})
	if err != nil {
		writeResultError(w, result, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

func (s *Server) handleTestSubscription(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name      string `json:"name,omitempty"` // 兼容旧客户端。
		Reference string `json:"reference,omitempty"`
		Group     string `json:"group,omitempty"`
		Via       string `json:"via,omitempty"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.Reference == "" {
		input.Reference = input.Name
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	result, err := s.service.TestSubscriptions(ctx, app.SubscriptionTestInput{
		Reference: input.Reference, Group: input.Group, Via: input.Via,
	})
	if err != nil {
		writeResultError(w, result, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

func (s *Server) handleSelectNode(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(r, &input); err != nil || input.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "缺少节点名称")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.service.SelectNode(ctx, input.Name); err != nil {
		writeAppError(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]string{"selected": input.Name})
}

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Version string `json:"version"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !s.installMu.TryLock() {
		writeError(w, http.StatusConflict, "install_busy", "另一个内核安装正在进行，请等待完成")
		return
	}
	defer s.installMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
	defer cancel()
	if strings.Contains(r.Header.Get("Accept"), "application/x-ndjson") {
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Accel-Buffering", "no")
		encoder := json.NewEncoder(w)
		controller := http.NewResponseController(w)
		// 回调同步写入并 flush；断开客户端会取消下载，保留 .part 供下次续传。
		emit := func(event core.InstallEvent) {
			if err := encoder.Encode(event); err != nil {
				cancel()
				return
			}
			if err := controller.Flush(); err != nil {
				cancel()
			}
		}
		emit(core.InstallEvent{Stage: "prepare", Message: "正在准备官方内核安装"})
		if err := s.service.InstallCore(ctx, input.Version, emit); err != nil {
			emit(core.InstallEvent{Stage: "error", Error: err.Error(), Message: "安装失败"})
		} else {
			// 终止标记由业务层返回成功后发送，不能把下载完毕当成安装成功。
			emit(core.InstallEvent{Stage: "complete", Message: "安装完成"})
		}
		return
	}
	events := []core.InstallEvent{}
	err := s.service.InstallCore(ctx, input.Version, func(event core.InstallEvent) { events = append(events, event) })
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeData(w, http.StatusOK, events)
}

func (s *Server) handleCoreAction(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimPrefix(r.URL.Path, "/api/v1/core/")
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var err error
	switch action {
	case "start":
		err = s.service.StartCore(ctx)
	case "stop":
		err = s.service.StopCore(ctx)
	case "restart":
		err = s.service.RestartCore(ctx)
	default:
		writeError(w, http.StatusNotFound, "not_found", "未知内核操作")
		return
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeData(w, http.StatusOK, s.service.CoreStatus(ctx))
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var settings app.Settings
	if err := decodeJSON(r, &settings); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := s.service.UpdateSettings(ctx, settings); err != nil {
		writeAppError(w, err)
		return
	}
	writeData(w, http.StatusOK, s.service.GetSettings())
}

func (s *Server) handleWebSecurity(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	security, err := s.service.UpdateWebToken(input.Token)
	if err != nil {
		writeAppError(w, err)
		return
	}
	if !security.AuthEnabled && s.listen != "" && !listenIsLoopback(s.listen) {
		s.logger.Warn("Web Token 鉴权已关闭，当前监听地址可被网络中的其他设备访问", "address", s.listen)
	}
	writeData(w, http.StatusOK, security)
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	assetPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if assetPath == "" || assetPath == "." {
		assetPath = "index.html"
	}
	data, err := fs.ReadFile(assets, "assets/"+assetPath)
	if err != nil {
		// SPA 路由回退到首页。
		data, err = fs.ReadFile(assets, "assets/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		assetPath = "index.html"
	}
	contentType := mime.TypeByExtension(path.Ext(assetPath))
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (s *Server) authorized(r *http.Request) bool {
	expected := strings.TrimSpace(s.store.Snapshot().Web.Secret)
	if expected == "" {
		return true
	}
	scheme, provided, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	provided = strings.TrimSpace(provided)
	if len(expected) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

func (s *Server) authorizedInternal(r *http.Request) bool {
	expected := strings.TrimSpace(s.store.Snapshot().Mihomo.ControllerKey)
	provided := strings.TrimSpace(r.Header.Get("X-Kivo-Internal"))
	// 升级期间可能仍有旧版生成的 provider 配置，只兼容头名称，不放宽内部鉴权。
	if provided == "" {
		provided = strings.TrimSpace(r.Header.Get("X-ProxyPilot-Internal"))
	}
	if expected == "" || len(expected) != len(provided) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.logger.Debug("HTTP", "method", r.Method, "path", r.URL.Path, "elapsed", time.Since(start))
		}
	})
}

func decodeJSON(r *http.Request, out any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("JSON 参数错误: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON 参数必须为单个对象，不能追加其他内容")
	}
	return nil
}

func writeData(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": value})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeAppError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
	}
	writeError(w, status, "operation_failed", err.Error())
}

// writeResultError 保留每项成功/失败及节点数量，避免一次失败抹掉整批更新结果。
func writeResultError(w http.ResponseWriter, result any, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, context.DeadlineExceeded) {
		status = http.StatusGatewayTimeout
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": result, "error": map[string]string{"code": "operation_failed", "message": err.Error()}})
}
