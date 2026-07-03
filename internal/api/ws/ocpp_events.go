package ws

// OCPP event message type constants. Dashboards listen for these to
// render connection state, queue pressure, and heartbeat health in
// real time.
const (
	MsgOCPPConnected     = "ocpp_connected"
	MsgOCPPDisconnected  = "ocpp_disconnected"
	MsgOCPPReconnected   = "ocpp_reconnected"
	MsgOCPPQueueOverflow = "ocpp_queue_overflow"
)

// BroadcastOCPPEvent is a convenience wrapper that builds a Message of
// the given OCPP-* type with the supplied data payload and broadcasts it.
// Safe to call from any goroutine.
//
// It does NOT set StationID, so the resulting message is fleet-scoped —
// visible only to ScopeAll subscribers (see messageVisibleTo), not to
// per-station or default-scope clients. Per-station OCPP events (connection
// state, queue overflow, etc.) must go through a station-tagging wrapper
// instead — see cmd/chargeghost's stationHubBroadcaster, which is what
// every real dispatcher in this codebase is wired to via SetHubBroadcaster.
// Call this directly only for events that are genuinely fleet-wide.
func (h *Hub) BroadcastOCPPEvent(eventType string, data map[string]interface{}) {
	h.BroadcastMessage(Message{Type: eventType, Data: data})
}

// BroadcastOCPPQueueOverflow satisfies the ocpp.HubBroadcaster interface
// with a fleet-scoped (untagged) overflow event — see BroadcastOCPPEvent's
// doc comment. Do not wire this directly to a CommandDispatcher via
// SetHubBroadcaster for a real per-station dispatcher; use a tagging
// wrapper so the event carries the correct StationID.
func (h *Hub) BroadcastOCPPQueueOverflow(description string, queueDepth, queueCap, droppedTotal int) {
	h.BroadcastOCPPEvent(MsgOCPPQueueOverflow, map[string]interface{}{
		"description":  description,
		"queueDepth":   queueDepth,
		"queueCap":     queueCap,
		"droppedTotal": droppedTotal,
	})
}
