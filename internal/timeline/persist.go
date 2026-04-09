package timeline

import (
	"github.com/chargeghost/engine/internal/persistence"
)

const timelineFile = "timeline.json"

// SaveState writes all events (newest-first) to dir/timeline.json.
func (s *Store) SaveState(dir string) error {
	s.mu.RLock()
	events := make([]TimelineEvent, 0, s.count)
	for i := 0; i < s.count; i++ {
		idx := (s.head - 1 - i + s.capacity) % s.capacity
		events = append(events, s.events[idx])
	}
	s.mu.RUnlock()
	return persistence.WriteJSON(dir, timelineFile, events)
}

// LoadState restores events from dir/timeline.json into the ring buffer.
// Events are stored newest-first; we replay oldest-first to maintain ring order.
func (s *Store) LoadState(dir string) error {
	var events []TimelineEvent
	if err := persistence.ReadJSON(dir, timelineFile, &events); err != nil {
		return err
	}
	if events == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Reset ring buffer.
	s.head = 0
	s.count = 0

	// Replay oldest-first (reverse of the saved newest-first order).
	for i := len(events) - 1; i >= 0; i-- {
		s.events[s.head] = events[i]
		s.head = (s.head + 1) % s.capacity
		if s.count < s.capacity {
			s.count++
		}
	}
	return nil
}
