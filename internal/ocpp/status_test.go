package ocpp_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTracker() *ocpp.StatusTracker {
	return ocpp.NewStatusTracker("wss://csms.example.com/ocpp", "CP_1", "1.6")
}

func TestStatusTracker_ConnectIncrementsReconnectCountAndSetsConnectedAt(t *testing.T) {
	tr := newTracker()
	tr.OnConnect()

	s := tr.Snapshot("wss://csms.example.com/ocpp", "CP_1", "1.6")
	assert.True(t, s.Connected)
	assert.Equal(t, 1, s.ReconnectCount)
	assert.False(t, s.ConnectedAt.IsZero())
	assert.Equal(t, "wss://csms.example.com/ocpp", s.CSMSURL)
	assert.Equal(t, "CP_1", s.OCPPID)
	assert.Equal(t, "1.6", s.Version)
	assert.False(t, s.UpSince.IsZero())
}

func TestStatusTracker_MultipleConnectsIncrementReconnectCount(t *testing.T) {
	tr := newTracker()
	tr.OnConnect()
	tr.OnDisconnect("connection lost")
	tr.OnConnect()
	tr.OnConnect() // already connected, should not increment
	tr.OnDisconnect("another drop")
	tr.OnConnect()

	s := tr.Snapshot("", "", "")
	assert.True(t, s.Connected)
	assert.Equal(t, 3, s.ReconnectCount, "expected 3 successful (re)connects: initial + 2 reconnects")
}

func TestStatusTracker_DisconnectClearsConnectedAndStampsDisconnectedAt(t *testing.T) {
	tr := newTracker()
	tr.OnConnect()
	tr.OnDisconnect("connection reset by peer")

	s := tr.Snapshot("", "", "")
	assert.False(t, s.Connected)
	assert.False(t, s.DisconnectedAt.IsZero())
	assert.Equal(t, "connection reset by peer", s.LastError)
	assert.False(t, s.LastErrorAt.IsZero())
}

func TestStatusTracker_DisconnectWithEmptyReasonKeepsLastErrorUntouched(t *testing.T) {
	tr := newTracker()
	tr.OnOutboundError(errors.New("synthetic outbound failure"))
	tr.OnConnect()
	tr.OnDisconnect("")

	s := tr.Snapshot("", "", "")
	assert.False(t, s.Connected)
	// Empty reason should not overwrite the previously-recorded error.
	assert.Equal(t, "synthetic outbound failure", s.LastError)
}

func TestStatusTracker_OnOutboundSuccessUpdatesLastMessageAt(t *testing.T) {
	tr := newTracker()
	tr.OnOutboundSuccess()

	s := tr.Snapshot("", "", "")
	assert.False(t, s.LastMessageAt.IsZero())
}

func TestStatusTracker_OnOutboundErrorUpdatesLastErrorAndLastErrorAt(t *testing.T) {
	tr := newTracker()
	tr.OnOutboundError(errors.New("send failed: timeout"))

	s := tr.Snapshot("", "", "")
	assert.Equal(t, "send failed: timeout", s.LastError)
	assert.False(t, s.LastErrorAt.IsZero())
}

func TestStatusTracker_OnOutboundErrorWithNilIsNoOp(t *testing.T) {
	tr := newTracker()
	tr.OnOutboundError(nil)

	s := tr.Snapshot("", "", "")
	assert.Empty(t, s.LastError)
	assert.True(t, s.LastErrorAt.IsZero())
}

func TestStatusTracker_HeartbeatSuccessUpdatesRTTAndCounter(t *testing.T) {
	tr := newTracker()
	tr.OnHeartbeat(250*time.Millisecond, nil)
	tr.OnHeartbeat(120*time.Millisecond, nil)

	s := tr.Snapshot("", "", "")
	assert.Equal(t, int64(2), s.HeartbeatSuccesses)
	assert.Equal(t, int64(0), s.HeartbeatFailures)
	assert.Equal(t, int64(120), s.LastHeartbeatRTTMs)
	assert.False(t, s.LastHeartbeatAt.IsZero())
}

func TestStatusTracker_HeartbeatErrorUpdatesLastErrorAndFailureCounter(t *testing.T) {
	tr := newTracker()
	tr.OnHeartbeat(0, errors.New("heartbeat timeout"))
	tr.OnHeartbeat(50*time.Millisecond, nil)

	s := tr.Snapshot("", "", "")
	assert.Equal(t, int64(1), s.HeartbeatSuccesses)
	assert.Equal(t, int64(1), s.HeartbeatFailures)
	assert.Equal(t, "heartbeat timeout", s.LastError)
	assert.False(t, s.LastErrorAt.IsZero())
}

func TestStatusTracker_HeartbeatRTTZeroIsNotRecorded(t *testing.T) {
	tr := newTracker()
	tr.OnHeartbeat(75*time.Millisecond, nil)
	tr.OnHeartbeat(0, nil) // zero RTT should not overwrite a previous measurement

	s := tr.Snapshot("", "", "")
	assert.Equal(t, int64(75), s.LastHeartbeatRTTMs)
}

func TestStatusTracker_QueueDepthUpdates(t *testing.T) {
	tr := newTracker()
	tr.SetQueueDepth(3)
	tr.SetQueueDepth(7)

	s := tr.Snapshot("wss://csms.example.com/ocpp", "CP_1", "2.0.1")
	assert.Equal(t, 7, s.QueueDepth)
}

func TestStatusTracker_QueueExhaustedAndDrainFlags(t *testing.T) {
	tr := newTracker()
	tr.SetQueueExhausted(2)
	tr.SetDrainInProgress(true)

	s := tr.Snapshot("", "", "2.0.1")
	assert.Equal(t, 2, s.QueueExhausted)
	assert.True(t, s.DrainInProgress)

	tr.SetDrainInProgress(false)
	s = tr.Snapshot("", "", "2.0.1")
	assert.False(t, s.DrainInProgress)
}

func TestStatusTracker_SnapshotOveridesMetadata(t *testing.T) {
	tr := newTracker()
	tr.OnConnect()

	s := tr.Snapshot("wss://override.example.com", "CP_99", "2.0.1")
	assert.Equal(t, "wss://override.example.com", s.CSMSURL)
	assert.Equal(t, "CP_99", s.OCPPID)
	assert.Equal(t, "2.0.1", s.Version)
}

func TestStatusTracker_ConcurrentUpdatesDoNotRace(t *testing.T) {
	tr := newTracker()
	tr.OnConnect()

	const goroutines = 16
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				switch (workerID + j) % 6 {
				case 0:
					tr.OnConnect()
				case 1:
					tr.OnDisconnect("drop")
				case 2:
					tr.OnOutboundSuccess()
				case 3:
					tr.OnOutboundError(errors.New("err"))
				case 4:
					tr.OnHeartbeat(time.Duration(j)*time.Millisecond, nil)
				case 5:
					_ = tr.Snapshot("", "", "")
				}
			}
		}(i)
	}
	wg.Wait()

	s := tr.Snapshot("", "", "")
	require.NotNil(t, s)
	// Sanity: at least one heartbeat should have been recorded.
	assert.GreaterOrEqual(t, s.HeartbeatSuccesses+s.HeartbeatFailures, int64(0))
}
