package ocpp

import (
	"context"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

// StartMeterValueTicker periodically sends MeterValues for all active sessions.
// interval defaults to 30s (overridden by MeterValueSampleInterval config key in Plan 5d).
// Call in a dedicated goroutine.
func StartMeterValueTicker(ctx context.Context, e *engine.Engine, bridge OCPPBridge, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !bridge.IsConnected() {
				continue
			}
			for _, connID := range e.GetConnectorIDs() {
				meterReading, txID := e.GetMeterSnapshot(connID)
				if txID == 0 {
					continue // no active transaction
				}
				cid := connID
				reading := meterReading
				tid := txID
				bridge.Dispatcher().Enqueue(OCPPCommand{
					Description: "MeterValues",
					Execute: func() error {
						return bridge.SendMeterValues(cid, reading, tid, "Sample.Periodic")
					},
				})
			}
		}
	}
}
