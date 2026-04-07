package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chargeghost/engine/internal/api"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
	rt "github.com/chargeghost/engine/internal/runtime"
	"github.com/chargeghost/engine/internal/timeline"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg := config.DefaultConfig()
	e := engine.NewEngine(cfg.MultiEVSEMode, cfg.EVBatteryCapacity*1000) // kWh → Wh
	for _, cc := range cfg.Connectors {
		e.AddConnector(cc.Voltage, cc.Current, cc.Phase)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runtime := rt.NewRuntime(e)
	go runtime.Run(ctx)

	timelineStore := timeline.NewStore(1000)
	localAuth := ocpp.NewStubLocalAuthManager()
	firmware := ocpp.NewStubFirmwareManager()
	diagnostics := ocpp.NewStubDiagnosticsManager()

	app := &api.AppContext{
		Engine:      e,
		Config:      cfg,
		StartTime:   time.Now(),
		Timeline:    timelineStore,
		LocalAuth:   localAuth,
		Firmware:    firmware,
		Diagnostics: diagnostics,
	}
	router := api.NewRouter(app)
	srv := api.NewServer(":8080", router)

	go func() {
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "err", err)
		}
	}()

	slog.Info("ChargeGhost engine started", "addr", ":8080")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down")
	cancel()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutCancel()
	_ = srv.Shutdown(shutCtx)
}
