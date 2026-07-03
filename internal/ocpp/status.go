package ocpp

import (
	"sync"
	"sync/atomic"
	"time"
)

// Status is the JSON shape returned by GET /api/v1/ocpp/status.
// It surfaces OCPP link health (connection state, message timing, queue
// depth, recent errors, reconnect count, uptime) for both v1.6 and v2.0.1.
//
// Fields with `omitempty` are zeroed until the corresponding event occurs
// (e.g. DisconnectedAt, LastError, LastHeartbeatAt).
type Status struct {
	// Version identifies the OCPP protocol version ("1.6" or "2.0.1").
	Version string `json:"version"`

	// Connection state.
	Connected      bool      `json:"connected"`
	ConnectedAt    time.Time `json:"connectedAt,omitempty"`
	DisconnectedAt time.Time `json:"disconnectedAt,omitempty"`

	// Connecting is true while the bridge is retrying its initial (or
	// post-disconnect) dial to the CSMS. Distinguishes "never connected,
	// actively retrying" from "connected once, then dropped" — both look
	// like Connected=false otherwise. Cleared by OnConnect.
	Connecting      bool `json:"connecting,omitempty"`
	ConnectAttempts int  `json:"connectAttempts,omitempty"`

	// Outbound traffic.
	LastMessageAt time.Time `json:"lastMessageAt,omitempty"`

	// Last error observed on any outbound send.
	LastError   string    `json:"lastError,omitempty"`
	LastErrorAt time.Time `json:"lastErrorAt,omitempty"`

	// ReconnectCount increments each time the WebSocket re-establishes.
	ReconnectCount int `json:"reconnectCount"`

	// UpSince is the process start time, captured when the tracker is
	// constructed. Distinct from ConnectedAt: UpSince never resets.
	UpSince time.Time `json:"upSince"`

	// CSMSURL and OCPPID are the configured target identity.
	CSMSURL string `json:"csmsUrl"`
	OCPPID  string `json:"ocppId"`

	// Heartbeat metrics. Tracked for v1.6 and v2.0.1 alike.
	LastHeartbeatAt    time.Time `json:"lastHeartbeatAt,omitempty"`
	LastHeartbeatRTTMs int64     `json:"lastHeartbeatRttMs,omitempty"`
	HeartbeatSuccesses int64     `json:"heartbeatSuccesses"`
	HeartbeatFailures  int64     `json:"heartbeatFailures"`

	// v2.0.1-only queue observability. For v1.6 these stay at zero values
	// and are elided by omitempty.
	QueueDepth      int  `json:"queueDepth"`
	QueueExhausted  int  `json:"queueExhausted"`
	QueueDropped    int  `json:"queueDropped"`
	DrainInProgress bool `json:"drainInProgress"`
}

// StatusTracker is a thread-safe accumulator of OCPP link health state.
// One tracker is owned by each bridge; callbacks and sender wrappers feed it.
type StatusTracker struct {
	mu sync.RWMutex

	// Identity is set at construction and is effectively immutable.
	csmsURL string
	ocppID  string
	version string
	upSince time.Time

	// Connection state. Connected uses an atomic.Bool for the common
	// IsConnected() path; mu covers the rest of the lifecycle fields.
	connected       atomic.Bool
	connectedAt     time.Time
	disconnected    time.Time
	reconnects      int64
	connecting      bool
	connectAttempts int

	// Outbound traffic + errors.
	lastMessageAt time.Time
	lastError     string
	lastErrorAt   time.Time

	// Heartbeat metrics.
	lastHeartbeatAt    time.Time
	lastHeartbeatRTTMs int64
	heartbeatSuccesses int64
	heartbeatFailures  int64

	// v2.0.1 queue observability.
	queueDepth     int
	queueExhausted int
	queueDropped   int
	drainActive    bool
}

// NewStatusTracker constructs a tracker with the bridge identity. The
// returned tracker is safe for concurrent use.
func NewStatusTracker(csmsURL, ocppID, version string) *StatusTracker {
	return &StatusTracker{
		csmsURL: csmsURL,
		ocppID:  ocppID,
		version: version,
		upSince: time.Now().UTC(),
	}
}

// OnConnect marks the link as connected. It also stamps ConnectedAt
// (only if the link was previously disconnected) and increments
// ReconnectCount. The very first OnConnect after construction does not
// count as a reconnect.
func (t *StatusTracker) OnConnect() {
	wasConnected := t.connected.Swap(true)
	now := time.Now().UTC()
	t.mu.Lock()
	if !wasConnected {
		t.connectedAt = now
		t.reconnects++
	}
	t.disconnected = time.Time{}
	t.connecting = false
	t.connectAttempts = 0
	t.mu.Unlock()
}

// OnConnectAttemptFailed records a failed dial attempt while the bridge is
// retrying its connection to the CSMS (see the retry loop in Bridge16/
// Bridge201.Start). Distinct from OnDisconnect, which marks a link that was
// connected and then dropped — this covers the "never connected yet, still
// trying" window so GET /ocpp/status can tell the two apart instead of
// showing an indefinite, unexplained Connected=false.
func (t *StatusTracker) OnConnectAttemptFailed(err error) {
	now := time.Now().UTC()
	t.mu.Lock()
	t.connecting = true
	t.connectAttempts++
	if err != nil {
		t.lastError = err.Error()
		t.lastErrorAt = now
	}
	t.mu.Unlock()
}

// OnDisconnect marks the link as disconnected and records the reason
// in LastError/LastErrorAt. A nil/empty reason produces an empty
// LastError string so the JSON omitempty keeps the field hidden.
func (t *StatusTracker) OnDisconnect(reason string) {
	t.connected.Store(false)
	now := time.Now().UTC()
	t.mu.Lock()
	t.disconnected = now
	if reason != "" {
		t.lastError = reason
		t.lastErrorAt = now
	}
	t.mu.Unlock()
}

// OnOutboundSuccess records a successful outbound message.
func (t *StatusTracker) OnOutboundSuccess() {
	t.mu.Lock()
	t.lastMessageAt = time.Now().UTC()
	t.mu.Unlock()
}

// OnOutboundError records a failed outbound message. The error string
// is stored verbatim.
func (t *StatusTracker) OnOutboundError(err error) {
	if err == nil {
		return
	}
	now := time.Now().UTC()
	t.mu.Lock()
	t.lastError = err.Error()
	t.lastErrorAt = now
	t.mu.Unlock()
}

// OnHeartbeat records the result of a heartbeat attempt. rtt is the
// round-trip time of the last attempt; err indicates failure.
func (t *StatusTracker) OnHeartbeat(rtt time.Duration, err error) {
	now := time.Now().UTC()
	t.mu.Lock()
	t.lastHeartbeatAt = now
	if rtt > 0 {
		t.lastHeartbeatRTTMs = rtt.Milliseconds()
	}
	if err == nil {
		t.heartbeatSuccesses++
	} else {
		t.heartbeatFailures++
		t.lastError = err.Error()
		t.lastErrorAt = now
	}
	t.mu.Unlock()
}

// SetQueueDepth updates the v2.0.1 queue depth snapshot.
func (t *StatusTracker) SetQueueDepth(depth int) {
	t.mu.Lock()
	t.queueDepth = depth
	t.mu.Unlock()
}

// SetQueueExhausted updates the v2.0.1 exhausted-message count.
func (t *StatusTracker) SetQueueExhausted(count int) {
	t.mu.Lock()
	t.queueExhausted = count
	t.mu.Unlock()
}

// SetQueueDropped updates the cumulative number of messages moved to
// the dead-letter file (queue overflow or retries exhausted). This is
// a v2.0.1-only signal; for v1.6 it stays at zero and is omitted from
// the status JSON.
func (t *StatusTracker) SetQueueDropped(count int) {
	t.mu.Lock()
	t.queueDropped = count
	t.mu.Unlock()
}

// SetDrainInProgress updates the v2.0.1 drain-in-progress flag.
func (t *StatusTracker) SetDrainInProgress(active bool) {
	t.mu.Lock()
	t.drainActive = active
	t.mu.Unlock()
}

// Snapshot returns a copy of the current tracker state. The metadata
// arguments (csmsURL/ocppID/version) override the constructor identity
// for cases where they were not known at construction time.
func (t *StatusTracker) Snapshot(csmsURL, ocppID, version string) Status {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if csmsURL == "" {
		csmsURL = t.csmsURL
	}
	if ocppID == "" {
		ocppID = t.ocppID
	}
	if version == "" {
		version = t.version
	}

	return Status{
		Version:            version,
		Connected:          t.connected.Load(),
		ConnectedAt:        t.connectedAt,
		DisconnectedAt:     t.disconnected,
		Connecting:         t.connecting,
		ConnectAttempts:    t.connectAttempts,
		LastMessageAt:      t.lastMessageAt,
		LastError:          t.lastError,
		LastErrorAt:        t.lastErrorAt,
		ReconnectCount:     int(t.reconnects),
		UpSince:            t.upSince,
		CSMSURL:            csmsURL,
		OCPPID:             ocppID,
		LastHeartbeatAt:    t.lastHeartbeatAt,
		LastHeartbeatRTTMs: t.lastHeartbeatRTTMs,
		HeartbeatSuccesses: t.heartbeatSuccesses,
		HeartbeatFailures:  t.heartbeatFailures,
		QueueDepth:         t.queueDepth,
		QueueExhausted:     t.queueExhausted,
		QueueDropped:       t.queueDropped,
		DrainInProgress:    t.drainActive,
	}
}
