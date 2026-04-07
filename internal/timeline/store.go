package timeline

import (
	"strings"
	"sync"
)

// Store is a thread-safe, fixed-capacity ring buffer of TimelineEvents.
type Store struct {
	mu       sync.RWMutex
	events   []TimelineEvent
	capacity int
	head     int // next write position
	count    int // number of valid entries
}

// NewStore creates a Store with the given capacity. Use 1000 for production.
func NewStore(capacity int) *Store {
	return &Store{
		events:   make([]TimelineEvent, capacity),
		capacity: capacity,
	}
}

// Append adds an event to the ring buffer, overwriting the oldest if full.
func (s *Store) Append(evt TimelineEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events[s.head] = evt
	s.head = (s.head + 1) % s.capacity
	if s.count < s.capacity {
		s.count++
	}
}

// Query returns (matching events, total matching count) applying the filter.
// Events are returned newest-first.
func (s *Store) Query(f TimelineFilter) ([]TimelineEvent, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if f.Limit <= 0 {
		f.Limit = 100
	}

	// Collect all events newest-first.
	all := make([]TimelineEvent, 0, s.count)
	for i := 0; i < s.count; i++ {
		idx := (s.head - 1 - i + s.capacity) % s.capacity
		all = append(all, s.events[idx])
	}

	// Apply filters.
	filtered := all[:0:len(all)]
	for _, evt := range all {
		if !matchesFilter(evt, f) {
			continue
		}
		filtered = append(filtered, evt)
	}
	total := len(filtered)

	// Apply offset + limit.
	if f.Offset >= len(filtered) {
		return []TimelineEvent{}, total
	}
	filtered = filtered[f.Offset:]
	if len(filtered) > f.Limit {
		filtered = filtered[:f.Limit]
	}
	return filtered, total
}

// Count returns the number of events in the store (not filtered).
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.count
}

// Clear removes all events.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.head = 0
	s.count = 0
}

func matchesFilter(evt TimelineEvent, f TimelineFilter) bool {
	if f.Source != "" && evt.Source != f.Source {
		return false
	}
	if f.Direction != "" && evt.Direction != f.Direction {
		return false
	}
	if f.EventType != "" && evt.EventType != f.EventType {
		return false
	}
	if f.Action != "" && evt.Action != f.Action {
		return false
	}
	if f.ConnectorID != nil && (evt.ConnectorID == nil || *evt.ConnectorID != *f.ConnectorID) {
		return false
	}
	if f.TransactionID != nil && (evt.TransactionID == nil || *evt.TransactionID != *f.TransactionID) {
		return false
	}
	if f.Search != "" && !strings.Contains(evt.Summary, f.Search) {
		return false
	}
	return true
}
