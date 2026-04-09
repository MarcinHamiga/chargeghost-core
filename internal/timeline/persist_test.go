package timeline

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStore_SaveLoadState(t *testing.T) {
	dir := t.TempDir()

	s := NewStore(100)
	for i := 0; i < 5; i++ {
		s.Append(TimelineEvent{
			EventID:   fmt.Sprintf("evt-%d", i),
			Timestamp: time.Now(),
			Source:    "ocpp_adapter",
			Direction: "outbound",
			Action:    fmt.Sprintf("Action%d", i),
			Summary:   fmt.Sprintf("Event %d", i),
		})
	}

	require.NoError(t, s.SaveState(dir))

	s2 := NewStore(100)
	require.NoError(t, s2.LoadState(dir))

	assert.Equal(t, 5, s2.Count())

	// Query newest-first; most recent should be evt-4.
	events, _ := s2.Query(TimelineFilter{Limit: 10})
	require.Len(t, events, 5)
	assert.Equal(t, "evt-4", events[0].EventID)
	assert.Equal(t, "evt-0", events[4].EventID)
}

func TestStore_SaveLoadState_RingWrap(t *testing.T) {
	dir := t.TempDir()

	s := NewStore(3) // Small capacity to force wrapping.
	for i := 0; i < 5; i++ {
		s.Append(TimelineEvent{
			EventID: fmt.Sprintf("evt-%d", i),
			Summary: fmt.Sprintf("Event %d", i),
		})
	}
	// Ring should contain evt-2, evt-3, evt-4 (oldest 2 overwritten).
	assert.Equal(t, 3, s.Count())

	require.NoError(t, s.SaveState(dir))

	s2 := NewStore(3)
	require.NoError(t, s2.LoadState(dir))

	assert.Equal(t, 3, s2.Count())
	events, _ := s2.Query(TimelineFilter{Limit: 10})
	require.Len(t, events, 3)
	assert.Equal(t, "evt-4", events[0].EventID)
	assert.Equal(t, "evt-2", events[2].EventID)
}

func TestStore_LoadState_MissingFile(t *testing.T) {
	s := NewStore(100)
	err := s.LoadState(t.TempDir())
	assert.NoError(t, err)
	assert.Equal(t, 0, s.Count())
}
