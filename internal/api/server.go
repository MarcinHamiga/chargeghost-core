package api

import (
	"context"
	"log/slog"
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

// Start begins listening. Blocks until the server stops.
func (s *Server) Start() error {
	slog.Info("HTTP server listening", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
