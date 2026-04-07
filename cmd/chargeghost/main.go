package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	engine "github.com/chargeghost/engine/internal/engine"
	rt "github.com/chargeghost/engine/internal/runtime"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	// Default engine: single-EVSE, 55 kWh battery.
	e := engine.NewEngine(false, 55000.0)
	e.AddConnector(230.0, 32.0, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	r := rt.NewRuntime(e)
	go r.Run(ctx)

	slog.Info("ChargeGhost engine started", "connectors", 1)

	// Wait for SIGINT or SIGTERM.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down")
	cancel()
}
