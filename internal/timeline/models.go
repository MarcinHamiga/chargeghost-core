package timeline

import "time"

// TimelineEvent is a single OCPP protocol event stored in the timeline.
type TimelineEvent struct {
	EventID        string      `json:"event_id"`
	Timestamp      time.Time   `json:"timestamp"`
	Source         string      `json:"source"`         // "ocpp_adapter" | "csms"
	Direction      string      `json:"direction"`      // "inbound" | "outbound"
	EventType      string      `json:"event_type"`     // "call" | "call_result" | "call_error"
	Action         string      `json:"action"`         // OCPP action name
	MessageID      string      `json:"message_id"`
	ConnectorID    *int        `json:"connector_id"`
	TransactionID  *int        `json:"transaction_id"`
	Level          string      `json:"level"`          // "info" | "warn" | "error"
	Summary        string      `json:"summary"`
	Payload        interface{} `json:"payload"`
	CorrelationKey *string     `json:"correlation_key"`
	Tags           []string    `json:"tags"`
}

// TimelineFilter specifies filtering criteria for timeline queries.
type TimelineFilter struct {
	Source        string
	Direction     string
	EventType     string
	Action        string
	Limit         int    // default 100
	Offset        int
	ConnectorID   *int
	TransactionID *int
	MinLevel      string
	Search        string // substring match on Summary
}
