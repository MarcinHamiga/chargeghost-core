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
// the given OCPP-* type with the supplied data payload and broadcasts
// it to all WebSocket clients. Safe to call from any goroutine.
func (h *Hub) BroadcastOCPPEvent(eventType string, data map[string]interface{}) {
	h.BroadcastMessage(Message{Type: eventType, Data: data})
}

// BroadcastOCPPQueueOverflow pushes a structured overflow event to the
// WebSocket hub. Used by the OCPP command dispatcher when its 256-slot
// command buffer is full and a command must be dropped. Satisfies the
// ocpp.HubBroadcaster interface.
func (h *Hub) BroadcastOCPPQueueOverflow(description string, queueDepth, queueCap, droppedTotal int) {
	h.BroadcastOCPPEvent(MsgOCPPQueueOverflow, map[string]interface{}{
		"description":  description,
		"queueDepth":   queueDepth,
		"queueCap":     queueCap,
		"droppedTotal": droppedTotal,
	})
}
