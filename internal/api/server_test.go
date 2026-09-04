package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

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
