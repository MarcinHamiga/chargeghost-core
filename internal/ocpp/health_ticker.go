package ocpp

import (
	"context"
	"log/slog"
	"time"
)

// StartHealthTicker periodically emits a structured slog line summarising
// the current OCPP link health. The interval is intentionally long (60s by
// default) so the log is useful for post-mortem timing without flooding
// the operator's stream. The goroutine exits when ctx is cancelled.
func StartHealthTicker(ctx context.Context, t *StatusTracker, interval time.Duration) {
	if t == nil || interval <= 0 {
		return
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s := t.Snapshot("", "", "")
			// Choose a level: Warn when disconnected, Info otherwise. This
			// lets operators tail the stream and immediately see when the
			// link has been down for a while.
			lvl := slog.LevelInfo
			attrs := []slog.Attr{
				slog.String("ocppId", s.OCPPID),
				slog.Bool("connected", s.Connected),
				slog.Int("reconnectCount", s.ReconnectCount),
				slog.String("uptime", time.Since(s.UpSince).Round(time.Second).String()),
			}
			if !s.Connected {
				lvl = slog.LevelWarn
				attrs = append(attrs, slog.String("disconnectedAt", s.DisconnectedAt.Format(time.RFC3339)))
			}
			if !s.LastMessageAt.IsZero() {
				attrs = append(attrs, slog.String("lastMessageAt", s.LastMessageAt.Format(time.RFC3339)))
			}
			if s.LastError != "" {
				attrs = append(attrs,
					slog.String("lastError", s.LastError),
					slog.String("lastErrorAt", s.LastErrorAt.Format(time.RFC3339)),
				)
			}
			if !s.LastHeartbeatAt.IsZero() {
				attrs = append(attrs,
					slog.String("lastHeartbeatAt", s.LastHeartbeatAt.Format(time.RFC3339)),
					slog.Int64("lastHeartbeatRttMs", s.LastHeartbeatRTTMs),
					slog.Int64("heartbeatSuccesses", s.HeartbeatSuccesses),
					slog.Int64("heartbeatFailures", s.HeartbeatFailures),
				)
			}
			if s.Version == "2.0.1" {
				attrs = append(attrs,
					slog.Int("queueDepth", s.QueueDepth),
					slog.Int("queueExhausted", s.QueueExhausted),
					slog.Bool("drainInProgress", s.DrainInProgress),
				)
			}
			slog.LogAttrs(ctx, lvl, "ocpp health", attrs...)
		}
	}
}
