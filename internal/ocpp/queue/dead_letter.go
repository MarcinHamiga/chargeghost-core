package queue

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// DeadLetter persists messages that have been moved out of the active
// queue. Two reasons for removal:
//
//   1. The queue reached its size cap (MaxMessages or MaxBytes) and
//      had to evict an old message to make room.
//   2. A message exhausted its retry budget and the operator did not
//      want it retried further.
//
// The file is JSONL (one record per line) so it can be appended to
// cheaply without rewriting the whole file, and so operators can
// inspect or replay it with standard tools.
//
// The implementation is deliberately minimal: it does not implement
// deletion, since dead-letter messages are by definition terminal
// until the operator decides to do something with them. If the file
// does not exist, Write is a no-op (we still log the move at the
// caller).
type DeadLetter struct {
	mu   sync.Mutex
	path string
}

// NewDeadLetter returns a writer for the given path. If path is empty,
// the returned writer is a no-op.
func NewDeadLetter(path string) *DeadLetter {
	return &DeadLetter{path: path}
}

// Enabled reports whether the writer will actually persist anything.
func (d *DeadLetter) Enabled() bool {
	return d != nil && d.path != ""
}

// Write appends a single message to the dead-letter file. The
// `reason` is a short tag (e.g. "queue-full", "exhausted") recorded
// alongside the message for later triage.
func (d *DeadLetter) Write(msg QueuedMessage, reason string) error {
	if !d.Enabled() {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	envelope := struct {
		MovedAt time.Time     `json:"moved_at"`
		Reason  string        `json:"reason"`
		Message QueuedMessage `json:"message"`
	}{
		MovedAt: time.Now().UTC(),
		Reason:  reason,
		Message: msg,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	f, err := os.OpenFile(d.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}
