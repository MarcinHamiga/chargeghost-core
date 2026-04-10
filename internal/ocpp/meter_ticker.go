package ocpp

import (
	"context"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

// StartMeterValueTicker periodically sends MeterValues for all active sessions.
// The interval is read from the provider at runtime so config changes take effect
// without restarting the process.
// Call in a dedicated goroutine.
func StartMeterValueTicker(ctx context.Context, e *engine.Engine, bridge OCPPBridge, provider MeterValueIntervalProvider) {
	interval := meterValueInterval(provider)
	timer := time.NewTimer(interval)
	defer timer.Stop()

	var refresh *time.Ticker
	var refreshC <-chan time.Time
	var changeC <-chan struct{}
	if notifier, ok := provider.(ConfigChangeNotifier); ok {
		changeC = notifier.ConfigChanges()
	} else {
		refresh = time.NewTicker(250 * time.Millisecond)
		refreshC = refresh.C
		defer refresh.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-refreshC:
			next := meterValueInterval(provider)
			if next == interval {
				continue
			}
			interval = next
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(interval)
		case <-changeC:
			next := meterValueInterval(provider)
			if next == interval {
				continue
			}
			interval = next
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(interval)
		case <-timer.C:
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
			timer.Reset(interval)
		}
	}
}

func meterValueInterval(provider MeterValueIntervalProvider) time.Duration {
	if provider == nil {
		return 30 * time.Second
	}
	interval := time.Duration(provider.GetMeterValueSampleInterval()) * time.Second
	if interval <= 0 {
		return 30 * time.Second
	}
	return interval
}
