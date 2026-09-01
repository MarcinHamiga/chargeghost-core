package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server wraps net/http.Server with structured logging and lifecycle methods.
type Server struct {
	httpServer *http.Server
}

// NewServer creates the HTTP server bound to addr (e.g. ":8080").
func NewServer(addr string, handler http.Handler) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr:         addr,
			Handler:      handler,
			ReadTimeout:  15 * time.Second,
			WriteTimeout: 15 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
	}
}

// Listen binds addr without serving. Use with Serve. An addr of
// "127.0.0.1:0" yields an OS-assigned port, discoverable via ln.Addr().
func (s *Server) Listen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// Serve serves on the supplied listener; blocks until the server stops.
func (s *Server) Serve(ln net.Listener) error {
	slog.Info("HTTP server listening", "addr", s.httpServer.Addr)
	return s.httpServer.Serve(ln)
}

// Start begins listening. Blocks until the server stops.
func (s *Server) Start() error {
	ln, err := s.Listen(s.httpServer.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ln)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
