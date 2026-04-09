package timeline_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/chargeghost/engine/internal/timeline"
	"github.com/stretchr/testify/assert"
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

func TestStore_NewestFirst(t *testing.T) {
	s := timeline.NewStore(3)
	s.Append(timeline.TimelineEvent{EventID: "e0", Summary: "event 0"})
	s.Append(timeline.TimelineEvent{EventID: "e1", Summary: "event 1"})
	s.Append(timeline.TimelineEvent{EventID: "e2", Summary: "event 2"})
	s.Append(timeline.TimelineEvent{EventID: "e3", Summary: "event 3"})
	s.Append(timeline.TimelineEvent{EventID: "e4", Summary: "event 4"})

	events, total := s.Query(timeline.TimelineFilter{Limit: 100})
	assert.Equal(t, 3, total)
	// newest-first: e4, e3, e2 (e0, e1 evicted)
	assert.Equal(t, "e4", events[0].EventID)
	assert.Equal(t, "e3", events[1].EventID)
	assert.Equal(t, "e2", events[2].EventID)
}
