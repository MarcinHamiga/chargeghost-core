package ocpp_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
)

// TestConfigChangeNotifierContract verifies the contract that the
// runtime config re-apply path depends on: a ConfigChangeNotifier
// implementation must expose a buffered (capacity >= 1) channel and
// must signal on every change.
func TestConfigChangeNotifierContract(t *testing.T) {
	mock := &notifierMock{ch: make(chan struct{}, 1)}

	// Static interface assertions: a provider that wants runtime
	// re-apply must implement both interfaces.
	var _ ocpppkg.MeterValueIntervalProvider = mock
	var _ ocpppkg.ConfigChangeNotifier = mock

	ch := mock.ConfigChanges()
	require.NotNil(t, ch)

	mock.signal()
	select {
	case <-ch:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected signal on channel")
	}

	// Second signal must also be received (proves the channel is
	// non-blocking and re-usable across cycles).
	mock.signal()
	select {
	case <-ch:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected second signal on channel")
	}
}

type notifierMock struct {
	val int
	ch  chan struct{}
}

func (m *notifierMock) GetMeterValueSampleInterval() int { return m.val }
func (m *notifierMock) ConfigChanges() <-chan struct{}   { return m.ch }
func (m *notifierMock) signal()                          { m.ch <- struct{}{} }
