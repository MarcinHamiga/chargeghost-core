package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/chargeghost/engine/internal/api"
	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
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

	hub := ws.NewHub()
	go hub.Run(ctx)
	go ws.StartTicker(ctx, hub, e, 1*time.Second)

	dispatcher := ocpp.NewCommandDispatcher()
	go dispatcher.Run(ctx)

	profileManager := ocpp.NewChargingProfileManager()
	e.GetLimit = func(connectorID int, transactionID int) *float64 {
		session := e.GetSession(connectorID)
		c := e.GetConnector(connectorID)
		if c == nil {
			return nil
		}
		var txStart *time.Time
		if session != nil {
			t := session.StartTime
			txStart = &t
		}
		return profileManager.GetCompositeLimit(connectorID, transactionID, time.Now(), c.Voltage, txStart, c.Phase)
	}

	configKeys := ocpp.NewConfigKeyManager()
	authCache := ocpp.NewAuthorizationCache()
	localAuthReal := ocpp.NewLocalAuthListManager()

	queuePath := os.ExpandEnv("$HOME/.chargeghost/message_queue.json")
	messageQueue, err := queue.NewQueue(cfg.PersistMessageQueue, queuePath, 3)
	if err != nil {
		slog.Error("failed to create message queue", "err", err)
		os.Exit(1)
	}

	// Create real firmware/diagnostics managers and data transfer registry.
	// Callbacks will be set after bridge is created (they reference bridge).
	var fwOnStatus func(string)
	var diagOnStatus func(string)
	firmwareManager := ocpp.NewFirmwareManager(func(status string) {
		if fwOnStatus != nil {
			fwOnStatus(status)
		}
	})
	diagnosticsManager := ocpp.NewDiagnosticsManager(func(status string) {
		if diagOnStatus != nil {
			diagOnStatus(status)
		}
	})
	dataTransferReg := ocpp.NewDataTransferRegistry()

	bridge := ocpp.NewBridge(e, hub, cfg, dispatcher, profileManager, configKeys, authCache, localAuthReal, messageQueue, firmwareManager, diagnosticsManager, dataTransferReg)

	// Wire firmware/diagnostics status callbacks now that bridge exists.
	fwOnStatus = func(status string) {
		hub.BroadcastMessage(ws.Message{
			Type: "firmware_status_changed",
			Data: map[string]string{"status": status},
		})
		if bridge.IsConnected() {
			dispatcher.Enqueue(ocpp.OCPPCommand{
				Description: "FirmwareStatusNotification",
				Execute: func() error {
					return bridge.SendFirmwareStatusNotification(status)
				},
			})
		}
	}

	diagOnStatus = func(status string) {
		hub.BroadcastMessage(ws.Message{
			Type: "diagnostics_status_changed",
			Data: map[string]string{"status": status},
		})
		if bridge.IsConnected() {
			dispatcher.Enqueue(ocpp.OCPPCommand{
				Description: "DiagnosticsStatusNotification",
				Execute: func() error {
					return bridge.SendDiagnosticsStatusNotification(status)
				},
			})
		}
	}

	// Wire engine event callbacks to WebSocket broadcasts.
	// All callbacks must be non-blocking — BroadcastMessage is non-blocking.
	e.OnConnectorStatusChanged = func(connectorID int, status engine.ConnectorState) {
		hub.BroadcastMessage(ws.Message{
			Type: "connector_status_changed",
			Data: map[string]interface{}{
				"connector_id": connectorID,
				"status":       string(status),
			},
		})
		if bridge.IsConnected() {
			dispatcher.Enqueue(ocpp.OCPPCommand{
				Description: fmt.Sprintf("StatusNotification connector %d", connectorID),
				Execute: func() error {
					return bridge.SendStatusNotification(connectorID, "NoError", string(status))
				},
			})
		}
	}

	e.OnConnectorParamsChanged = func(connectorID int, voltage, current float64, phase int) {
		hub.BroadcastMessage(ws.Message{
			Type: "connector_params_changed",
			Data: map[string]interface{}{
				"connector_id": connectorID,
				"voltage":      voltage,
				"current":      current,
				"phase":        phase,
			},
		})
	}

	e.OnSessionStarted = func(connectorID int) {
		hub.BroadcastMessage(ws.Message{
			Type: "session_started",
			Data: map[string]interface{}{"connector_id": connectorID},
		})
		if !bridge.IsConnected() {
			return
		}
		session := e.GetSession(connectorID)
		if session == nil {
			return
		}
		idTag := ""
		if session.IDTag != nil {
			idTag = *session.IDTag
		}
		meter, _ := e.GetMeterSnapshot(connectorID)
		reservationID := session.ReservationID

		dispatcher.Enqueue(ocpp.OCPPCommand{
			Description: fmt.Sprintf("StartTransaction connector %d", connectorID),
			Execute: func() error {
				txID, err := bridge.SendStartTransaction(connectorID, idTag, meter, time.Now(), reservationID)
				if err != nil {
					return err
				}
				e.SetActiveTransaction(connectorID, txID)
				return nil
			},
		})
	}

	e.OnSessionStopped = func(connectorID int) {
		info := e.GetLastStoppedSession()
		if info == nil {
			hub.BroadcastMessage(ws.Message{
				Type: "session_stopped",
				Data: map[string]interface{}{"connector_id": connectorID},
			})
			return
		}
		hub.BroadcastMessage(ws.Message{
			Type: "session_stopped",
			Data: map[string]interface{}{
				"connector_id":      connectorID,
				"transaction_id":    info.TransactionID,
				"energy_charged_wh": info.EnergyCharged,
				"reason":            info.Reason,
			},
		})
		if !bridge.IsConnected() {
			return
		}
		snapshot := *info
		dispatcher.Enqueue(ocpp.OCPPCommand{
			Description: fmt.Sprintf("StopTransaction connector %d tx %d", connectorID, snapshot.TransactionID),
			Execute: func() error {
				return bridge.SendStopTransaction(snapshot.MeterStop, time.Now(), snapshot.TransactionID, snapshot.Reason, snapshot.MeterHistory)
			},
		})
	}

	e.OnReservationExpired = func(reservationID, connectorID int) {
		hub.BroadcastMessage(ws.Message{
			Type: "reservation_changed",
			Data: map[string]interface{}{
				"action":         "expired",
				"reservation_id": reservationID,
				"connector_id":   connectorID,
			},
		})
	}

	timelineStore := timeline.NewStore(1000)

	app := &api.AppContext{
		Engine:         e,
		Config:         cfg,
		StartTime:      time.Now(),
		Timeline:       timelineStore,
		LocalAuth:      localAuthReal,
		Firmware:       firmwareManager,
		Diagnostics:    diagnosticsManager,
		Hub:            hub,
		ProfileManager: profileManager,
		ConfigKeys:     configKeys,
		OCPP:           bridge,
	}
	router := api.NewRouter(app)
	srv := api.NewServer(":8080", router)

	go func() {
		if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server error", "err", err)
		}
	}()

	// Start the bridge (connects to CSMS).
	go bridge.Start(ctx)

	// Start periodic MeterValues ticker.
	go ocpp.StartMeterValueTicker(ctx, e, bridge, 30*time.Second)

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
