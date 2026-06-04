package ocpp

// HubBroadcaster is the minimal surface the CommandDispatcher needs to
// push OCPP-level events to the WebSocket hub. The ws.Hub satisfies
// this interface; tests can plug in a stub.
type HubBroadcaster interface {
	BroadcastOCPPQueueOverflow(description string, queueDepth, queueCap, droppedTotal int)
}

// SetHubBroadcaster installs a hub broadcaster that will receive an
// overflow event whenever the dispatcher drops a command. Pass nil
// to detach.
func (d *CommandDispatcher) SetHubBroadcaster(h HubBroadcaster) {
	if h == nil {
		d.hub.Store(nil)
		return
	}
	d.hub.Store(&h)
}
