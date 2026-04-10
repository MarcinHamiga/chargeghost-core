package ocpp

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/chargeghost/engine/internal/timeline"
)

// TimelineLogger writes OCPP protocol events to a timeline store.
type TimelineLogger struct {
	store *timeline.Store
}

// NewTimelineLogger creates a TimelineLogger. If store is nil, logging is a no-op.
func NewTimelineLogger(store *timeline.Store) *TimelineLogger {
	return &TimelineLogger{store: store}
}

// LogOutbound records an outbound (charge-point → CSMS) OCPP message.
func (l *TimelineLogger) LogOutbound(action string, connectorID *int, transactionID *int, summary string, payload interface{}) {
	l.log("ocpp_adapter", "outbound", "call", action, connectorID, transactionID, "info", summary, payload)
}

// LogInbound records an inbound (CSMS → charge-point) OCPP message.
func (l *TimelineLogger) LogInbound(action string, connectorID *int, transactionID *int, summary string, payload interface{}) {
	l.log("csms", "inbound", "call", action, connectorID, transactionID, "info", summary, payload)
}

// LogError records a failed OCPP message exchange.
func (l *TimelineLogger) LogError(action, direction string, connectorID *int, summary string) {
	l.log("ocpp_adapter", direction, "call_error", action, connectorID, nil, "error", summary, nil)
}

func (l *TimelineLogger) log(source, direction, eventType, action string, connectorID, transactionID *int, level, summary string, payload interface{}) {
	if l == nil || l.store == nil {
		return
	}
	l.store.Append(timeline.TimelineEvent{
		EventID:       uuid.NewString(),
		Timestamp:     time.Now(),
		Source:        source,
		Direction:     direction,
		EventType:     eventType,
		Action:        action,
		ConnectorID:   connectorID,
		TransactionID: transactionID,
		Level:         level,
		Summary:       summary,
		Payload:       payload,
	})
}

// IntPtr returns an *int for timeline logging call sites.
func IntPtr(v int) *int { return &v }

// FormatMeter formats a meter value for timeline summaries and log strings.
func FormatMeter(wh float64) string {
	return fmt.Sprintf("%.2f Wh", wh)
}
