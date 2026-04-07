package timeline_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/chargeghost/engine/internal/timeline"
)

func TestStore_AppendAndFilter(t *testing.T) {
	s := timeline.NewStore(100)
	s.Append(timeline.TimelineEvent{
		EventID:   "evt1",
		Timestamp: time.Now(),
		Source:    "ocpp_adapter",
		Direction: "outbound",
		Action:    "BootNotification",
		Level:     "info",
		Summary:   "BootNotification sent",
	})

	events, total := s.Query(timeline.TimelineFilter{Limit: 10})
	assert.Equal(t, 1, total)
	assert.Len(t, events, 1)
	assert.Equal(t, "BootNotification", events[0].Action)
}

func TestStore_RingBufferEvictsOldest(t *testing.T) {
	s := timeline.NewStore(3) // capacity = 3
	for i := 0; i < 5; i++ {
		s.Append(timeline.TimelineEvent{EventID: fmt.Sprintf("e%d", i), Summary: fmt.Sprintf("event %d", i)})
	}
	_, total := s.Query(timeline.TimelineFilter{Limit: 100})
	assert.Equal(t, 3, total) // only 3 kept
}

func TestStore_FilterByAction(t *testing.T) {
	s := timeline.NewStore(100)
	s.Append(timeline.TimelineEvent{EventID: "e1", Action: "BootNotification"})
	s.Append(timeline.TimelineEvent{EventID: "e2", Action: "Heartbeat"})

	events, _ := s.Query(timeline.TimelineFilter{Action: "BootNotification", Limit: 10})
	assert.Len(t, events, 1)
	assert.Equal(t, "BootNotification", events[0].Action)
}

func TestStore_Clear(t *testing.T) {
	s := timeline.NewStore(100)
	s.Append(timeline.TimelineEvent{EventID: "e1"})
	s.Clear()
	_, total := s.Query(timeline.TimelineFilter{Limit: 10})
	assert.Equal(t, 0, total)
}
