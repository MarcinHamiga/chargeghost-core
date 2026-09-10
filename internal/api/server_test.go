package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerListenEphemeralPort(t *testing.T) {
	srv := NewServer("127.0.0.1:0", http.NewServeMux())

	ln, err := srv.Listen("127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	addr := ln.Addr().String()
	require.NotContains(t, addr, ":0", "ephemeral port should be resolved")
	require.True(t, len(addr) > 0)
}

func TestServerServeAndShutdown(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := NewServer("127.0.0.1:0", mux)

	ln, err := srv.Listen("127.0.0.1:0")
	require.NoError(t, err)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	url := "http://" + ln.Addr().String() + "/health"
	resp, err := http.Get(url)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))

	select {
	case err := <-serveErr:
		require.True(t, errors.Is(err, http.ErrServerClosed), "Serve should return ErrServerClosed, got %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Shutdown")
	}
}

func TestServerListenRejectedAddr(t *testing.T) {
	// Bind a listener to occupy a port, then point Listen at it.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	srv := NewServer(ln.Addr().String(), http.NewServeMux())
	_, err = srv.Listen(ln.Addr().String())
	require.Error(t, err, "Listen on an occupied port should fail")
}

func TestCorsMiddleware_ReflectsOnlyAllowedOrigins(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	h := corsMiddlewareWithOrigins([]string{"https://dash.example.com"})(next)

	loopback := httptest.NewRequest(http.MethodGet, "/health", nil)
	loopback.Header.Set("Origin", "http://localhost:3000")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, loopback)
	assert.Equal(t, "http://localhost:3000", w.Header().Get("Access-Control-Allow-Origin"))

	configured := httptest.NewRequest(http.MethodGet, "/health", nil)
	configured.Header.Set("Origin", "https://dash.example.com")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, configured)
	assert.Equal(t, "https://dash.example.com", w.Header().Get("Access-Control-Allow-Origin"))

	foreign := httptest.NewRequest(http.MethodGet, "/health", nil)
	foreign.Header.Set("Origin", "https://evil.example.com")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, foreign)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"), "foreign origins must not be reflected")
	assert.NotEqual(t, "*", w.Header().Get("Access-Control-Allow-Origin"), "wildcard CORS must not be emitted")

	plain := httptest.NewRequest(http.MethodGet, "/health", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, plain)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"), "non-browser requests need no ACAO header")
}

func TestRequireLoopback_AllowsLoopbackOnly(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := requireLoopback(next)

	for _, addr := range []string{"127.0.0.1:4321", "[::1]:4321", "localhost:4321"} {
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "loopback %s must pass", addr)
	}

	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "203.0.113.5:4321"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestRequireLoopback_RejectsSpoofedProxyHeaders verifies the gate judges
// the pre-RealIP socket peer, not client-supplied proxy headers: a remote
// caller claiming 127.0.0.1 via X-Forwarded-For (or its siblings) must still
// get 403 through the production middleware order.
func TestRequireLoopback_RejectsSpoofedProxyHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := recordPeerAddr(middleware.RealIP(requireLoopback(next)))

	spoofed := map[string]string{
		"X-Forwarded-For": "127.0.0.1",
		"X-Real-Ip":       "127.0.0.1",
		"True-Client-Ip":  "127.0.0.1",
	}
	for header, value := range spoofed {
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.RemoteAddr = "203.0.113.5:4321"
		req.Header.Set(header, value)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "spoofed %s must not pass", header)
	}

	// A genuine loopback peer stays allowed even when proxy headers claim a
	// remote address — the snapshot wins over RealIP's rewrite.
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.RemoteAddr = "127.0.0.1:4321"
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
