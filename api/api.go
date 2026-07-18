// Copyright 2025 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "github.com/blinklabs-io/adder/docs"
	"github.com/blinklabs-io/adder/internal/logging"
	httpSwagger "github.com/swaggo/http-swagger/v2"
)

// HealthChecker is an interface for components that can report their health status
type HealthChecker interface {
	IsRunning() bool
}

type API interface {
	Start() error
	Shutdown(context.Context) error
	AddRoute(method, path string, handler http.HandlerFunc)
}

type APIv1 struct {
	mux      *http.ServeMux
	handler  http.Handler
	basePath string
	Host     string
	Port     uint
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
}

type APIRouteRegistrar interface {
	RegisterRoutes()
}

type APIOption func(*APIv1)

func WithGroup(group string) APIOption {
	// Expects '/v1' as the group
	return func(a *APIv1) {
		a.basePath = group
	}
}

func WithHost(host string) APIOption {
	return func(a *APIv1) {
		a.Host = host
	}
}

func WithPort(port uint) APIOption {
	return func(a *APIv1) {
		a.Port = port
	}
}

// newAPIv1 builds an APIv1 with its router and middleware-wrapped handler.
func newAPIv1() *APIv1 {
	mux := ConfigureRouter()
	return &APIv1{
		mux:     mux,
		handler: withMiddleware(mux),
		Host:    "0.0.0.0",
		Port:    8080,
	}
}

// Initialize singleton API instance.
var apiInstance = newAPIv1()

var once sync.Once

// healthCheckers holds registered health checkers
var (
	healthCheckers   []HealthChecker
	healthCheckersMu sync.RWMutex
)

// RegisterHealthChecker adds a health checker to be queried during health checks
func RegisterHealthChecker(hc HealthChecker) {
	healthCheckersMu.Lock()
	defer healthCheckersMu.Unlock()
	healthCheckers = append(healthCheckers, hc)
}

// ResetHealthCheckers clears all registered health checkers.
// This is intended for use in tests to prevent state leakage between tests.
func ResetHealthCheckers() {
	healthCheckersMu.Lock()
	defer healthCheckersMu.Unlock()
	healthCheckers = nil
}

// New initializes the singleton API instance. The debug parameter is retained
// for backward compatibility; access-log verbosity is now governed by the
// configured logging level (LOGGING_LEVEL), not by this flag.
func New(debug bool, options ...APIOption) *APIv1 {
	_ = debug
	once.Do(func() {
		apiInstance = newAPIv1()
		for _, opt := range options {
			opt(apiInstance)
		}
	})

	return apiInstance
}

func GetInstance() *APIv1 {
	return apiInstance
}

// Engine returns the middleware-wrapped HTTP handler. Routes added via
// AddRoute after this call remain reachable because the handler wraps the
// underlying mux by reference.
func (a *APIv1) Engine() http.Handler {
	return a.handler
}

// BasePath returns the configured route group prefix (e.g. "/v1"), or "".
func (a *APIv1) BasePath() string {
	return a.basePath
}

//	@title			Adder API
//	@version		v1
//	@description	Adder API
//	@Schemes		http
//	@BasePath		/

//	@contact.name	Blink Labs
//	@contact.url	https://blinklabs.io
//	@contact.email	support@blinklabs.io

// @license.name	Apache 2.0
// @license.url	http://www.apache.org/licenses/LICENSE-2.0.html
func (a *APIv1) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.listener != nil {
		return errors.New("API server is already running")
	}
	address := fmt.Sprintf("%s:%d", a.Host, a.Port)
	// Bind synchronously so listener errors (e.g. port already in
	// use, invalid address) are returned to the caller instead of
	// being lost in a background goroutine.
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", address, err)
	}
	a.listener = listener
	a.server = &http.Server{
		Handler:           a.handler,
		ReadHeaderTimeout: 60 * time.Second,
		// No WriteTimeout: it would cap SSE/WebSocket streaming.
		ReadTimeout: 60 * time.Second,
		IdleTimeout: 120 * time.Second,
	}
	server := a.server
	// Serve in the background; the listener is already bound.
	go func() {
		if err := server.Serve(listener); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			slog.Error("API server stopped unexpectedly", "error", err)
		}
	}()
	return nil
}

func (a *APIv1) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	server := a.server
	listener := a.listener
	a.mu.Unlock()
	if server == nil {
		return nil
	}
	if listener != nil {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("closing API listener: %w", err)
		}
	}
	if err := server.Shutdown(ctx); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("shutting down API server: %w", err)
	}
	a.mu.Lock()
	a.server = nil
	a.listener = nil
	a.mu.Unlock()
	return nil
}

// AddRoute registers handler for method+path under the configured group base
// path. Note: net/http's ServeMux panics if the same method+pattern is
// registered twice, so callers must register each route only once (the push
// plugin guards this with a routesRegistered flag). A path that already ends in
// "/" is registered as a ServeMux subtree (prefix match) with no exact-match
// variant, matching all sub-paths; avoid trailing slashes unless that is
// intended.
func (a *APIv1) AddRoute(method, path string, handler http.HandlerFunc) {
	switch method {
	case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
		// Register with the ServeMux method+path pattern, prefixed by the
		// configured group base path.
		full := a.basePath + path
		a.mux.HandleFunc(method+" "+full, handler)
		// Tolerate an optional trailing slash (mirrors Gin's
		// RedirectTrailingSlash) via the "{$}" end-of-path anchor, unless
		// the path already ends in a slash.
		if !strings.HasSuffix(full, "/") {
			a.mux.HandleFunc(method+" "+full+"/{$}", handler)
		}
	default:
		log.Printf("Unsupported HTTP method: %s", method)
	}
}

// HandleFunc registers a handler on the underlying mux without the group
// prefix. The pattern must include the method, e.g. "GET /events".
func (a *APIv1) HandleFunc(pattern string, handler http.HandlerFunc) {
	a.mux.HandleFunc(pattern, handler)
}

func ConfigureRouter() *http.ServeMux {
	mux := http.NewServeMux()
	// handleBoth registers a handler for the exact path and its
	// trailing-slash form, mirroring Gin's RedirectTrailingSlash.
	handleBoth := func(pattern string, h http.HandlerFunc) {
		mux.HandleFunc(pattern, h)
		mux.HandleFunc(pattern+"/{$}", h)
	}
	// Healthcheck endpoint
	handleBoth("GET /healthcheck", handleHealthcheck)
	// No-op API endpoint for testing
	handleBoth("GET /ping", handlePing)
	// Swagger UI
	mux.Handle("GET /swagger/", httpSwagger.WrapHandler)

	return mux
}

// recorder interface defines the accessors for the response state.
type recorder interface {
	http.ResponseWriter
	Status() int
	Wrote() bool
	Hijacked() bool
}

type baseRecorder struct {
	http.ResponseWriter
	status   int
	wrote    bool
	hijacked bool
}

func (r *baseRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *baseRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.wrote = true
	}
	return r.ResponseWriter.Write(b)
}

func (r *baseRecorder) Status() int    { return r.status }
func (r *baseRecorder) Wrote() bool    { return r.wrote }
func (r *baseRecorder) Hijacked() bool { return r.hijacked }

type recorderOnly struct {
	*baseRecorder
}

type recorderFlusher struct {
	*baseRecorder
}

func (r recorderFlusher) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		// Flush implicitly commits a 200; record it so panic recovery
		// and the access log don't report a contradictory status.
		r.wrote = true
		f.Flush()
	}
}

type recorderHijacker struct {
	*baseRecorder
}

func (r recorderHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h := r.ResponseWriter.(http.Hijacker)
	conn, rw, err := h.Hijack()
	if err == nil {
		r.hijacked = true
		r.wrote = true
		r.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}

type recorderFlusherHijacker struct {
	*baseRecorder
}

func (r recorderFlusherHijacker) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		// Flush implicitly commits a 200; record it so panic recovery
		// and the access log don't report a contradictory status.
		r.wrote = true
		f.Flush()
	}
}

func (r recorderFlusherHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h := r.ResponseWriter.(http.Hijacker)
	conn, rw, err := h.Hijack()
	if err == nil {
		r.hijacked = true
		r.wrote = true
		r.status = http.StatusSwitchingProtocols
	}
	return conn, rw, err
}

func newResponseRecorder(w http.ResponseWriter) http.ResponseWriter {
	base := &baseRecorder{ResponseWriter: w, status: http.StatusOK}
	_, isFlusher := w.(http.Flusher)
	_, isHijacker := w.(http.Hijacker)

	if isFlusher && isHijacker {
		return recorderFlusherHijacker{base}
	}
	if isFlusher {
		return recorderFlusher{base}
	}
	if isHijacker {
		return recorderHijacker{base}
	}
	return recorderOnly{base}
}

// withMiddleware wraps the router with panic recovery and access logging.
func withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := newResponseRecorder(w)
		recState := rec.(recorder)
		var errMsg string
		defer func() {
			if err := recover(); err != nil {
				errMsg = fmt.Sprint(err)
				// Only emit a response status if the handler hasn't
				// started one and the connection wasn't hijacked
				// (e.g. an upgraded WebSocket).
				if !recState.Wrote() && !recState.Hijacked() {
					recState.WriteHeader(http.StatusInternalServerError)
				}
				logging.GetLogger().Error(
					"panic recovered in HTTP handler",
					"error", errMsg,
					"path", r.URL.Path,
				)
			}
			accessLog(recState, r, time.Since(start), errMsg)
		}()
		next.ServeHTTP(rec, r)
	})
}

func accessLog(
	rec recorder,
	r *http.Request,
	latency time.Duration,
	errMsg string,
) {
	logger := logging.GetLogger()
	// Access lines are emitted at Info so they appear under the default
	// logging level. Skip the attribute work entirely when Info is
	// disabled by the configured level.
	if !logger.Enabled(r.Context(), slog.LevelInfo) {
		return
	}
	logger.Info(
		"access",
		"type", "access",
		"client_ip", clientIP(r),
		"method", r.Method,
		"path", r.URL.Path,
		"proto", r.Proto,
		"status_code", rec.Status(),
		"latency", latency.String(),
		"user_agent", r.UserAgent(),
		"error_message", errMsg,
	)
}

// clientIP resolves the originating client address, honoring proxy
// forwarding headers before falling back to the transport remote address.
// The X-Forwarded-For / X-Real-IP headers are client-supplied and trivially
// spoofable; the result is used for access logging only and must not drive
// any authorization or rate-limiting decision.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First entry is the original client.
		first, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(first)
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// WriteJSON writes v as a JSON response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to encode response"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

// @Summary		Ping Endpoint
// @Description	Returns a simple "pong" response to verify the API server is alive and reachable.
// @Produce		text/plain
// @Success		200	{string}	string	"pong"
// @Router			/ping [get]
func handlePing(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("pong"))
}

// @Summary		Healthcheck Endpoint
// @Description	Reports the health status of the running pipeline and registered checkers. Returns 503 if any registered checker is unhealthy.
// @Produce		json
// @Success		200	{object}	map[string]any	"Healthy status response"
// @Failure		503	{object}	map[string]any	"Service Unavailable response"
// @Router			/healthcheck [get]
func handleHealthcheck(w http.ResponseWriter, _ *http.Request) {
	healthCheckersMu.RLock()
	// Make a copy of the slice to avoid races with concurrent RegisterHealthChecker calls
	checkers := make([]HealthChecker, len(healthCheckers))
	copy(checkers, healthCheckers)
	healthCheckersMu.RUnlock()

	// If no health checkers are registered, report as healthy
	if len(checkers) == 0 {
		WriteJSON(w, http.StatusOK, map[string]any{"failed": false})
		return
	}

	// Check all registered health checkers
	for _, hc := range checkers {
		if !hc.IsRunning() {
			WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
				"failed": true,
				"reason": "pipeline is not running",
			})
			return
		}
	}

	WriteJSON(w, http.StatusOK, map[string]any{"failed": false})
}
