package ocpp

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/chargeghost/engine/internal/timeline"
)

// TimelineLogger writes OCPP protocol events to a timeline store.
//
// It assigns a synthetic MessageID to every outbound CALL so the
// matching CSMS response (and any error) can be correlated. The
// correlation is propagated to the TimelineEvent.CorrelationKey field
// so consumers of the timeline API can stitch together a full
// request/response cycle for a single OCPP exchange.
type TimelineLogger struct {
	store *timeline.Store
}

// NewTimelineLogger creates a TimelineLogger. If store is nil, logging is a no-op.
func NewTimelineLogger(store *timeline.Store) *TimelineLogger {
	return &TimelineLogger{store: store}
}

// LogOutbound records an outbound (charge-point → CSMS) OCPP message
// and returns a synthetic MessageID the caller can use to correlate
// the eventual response (via LogResponse) or error (via LogError). A
// nil receiver returns an empty MessageID.
func (l *TimelineLogger) LogOutbound(action string, connectorID *int, transactionID *int, summary string, payload interface{}) string {
	if l == nil || l.store == nil {
		return ""
	}
	messageID := uuid.NewString()
	correlationKey := messageID
	l.log("ocpp_adapter", "outbound", "call", action, connectorID, transactionID, "info", summary, payload, &messageID, messageID, correlationKey)
	return messageID
}

// LogInbound records an inbound (CSMS → charge-point) OCPP message.
// A synthetic correlation key is generated for every inbound so the
// matching outbound response can be linked in the timeline (the CSMS
// doesn't expose its own message ID for CALL → CALL_RESULT, so we
// mint one locally). This is the hook that ties a SetChargingProfile
// REQUEST to the response we emit, allowing operators to follow one
// full exchange end-to-end in the timeline.
func (l *TimelineLogger) LogInbound(action string, connectorID *int, summary string, payload interface{}, correlationKey string) string {
	if l == nil || l.store == nil {
		return ""
	}
	if correlationKey == "" {
		correlationKey = uuid.NewString()
	}
	messageID := correlationKey
	l.log("csms", "inbound", "call", action, connectorID, nil, "info", summary, payload, &messageID, messageID, correlationKey)
	return correlationKey
}

// LogError records a failed OCPP message exchange. The optional
// payload is the request body that failed (helps operators see what
// was actually sent when the CSMS rejected or the network dropped
// the response).
func (l *TimelineLogger) LogError(action, direction string, connectorID *int, summary string, payload interface{}, correlationKey string) {
	l.log("ocpp_adapter", direction, "call_error", action, connectorID, nil, "error", summary, payload, nil, "", correlationKey)
}

// LogResponse records a CSMS response to a previous outbound call.
// It is intentionally lightweight: level=info, summary carries the
// measured RTT in milliseconds, and payload is the response body
// (may be nil when the caller does not have it). The correlation
// key MUST match the MessageID returned from the original
// LogOutbound so the timeline shows the call + response as a pair.
func (l *TimelineLogger) LogResponse(action string, connectorID *int, transactionID *int, messageID string, latency time.Duration, summary string, payload interface{}) {
	if l == nil || l.store == nil {
		return
	}
	if messageID == "" {
		// Nothing to correlate; fall back to a stand-alone response record.
		l.log("csms", "inbound", "call_result", action, connectorID, transactionID, "info", summary, payload, nil, "", "")
		return
	}
	// Surface the latency in the summary so the timeline UI doesn't
	// have to read the payload to display it.
	latencyMs := latency.Milliseconds()
	rttSummary := fmt.Sprintf("rtt=%dms %s", latencyMs, summary)
	l.log("csms", "inbound", "call_result", action, connectorID, transactionID, "info", rttSummary, payload, &messageID, messageID, messageID)
}

func (l *TimelineLogger) log(source, direction, eventType, action string, connectorID, transactionID *int, level, summary string, payload interface{}, messageID *string, messageIDValue, correlationKey string) {
	if l == nil || l.store == nil {
		return
	}
	event := timeline.TimelineEvent{
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
		Tags:          nil,
	}
	if messageID != nil {
		event.MessageID = *messageID
	}
	if correlationKey != "" {
		event.CorrelationKey = &correlationKey
	}
	_ = messageIDValue // currently unused; reserved for future correlationID
	l.store.Append(event)
}

// IntPtr returns an *int for timeline logging call sites.
func IntPtr(v int) *int { return &v }

// FormatMeter formats a meter value for timeline summaries and log strings.
func FormatMeter(wh float64) string {
	return fmt.Sprintf("%.2f Wh", wh)
}
