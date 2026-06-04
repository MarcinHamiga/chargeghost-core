package queue

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
)

type jsonFileData struct {
	Messages []QueuedMessage `json:"messages"`
}

// JsonFileQueue persists the message queue to a JSON file. Survives restarts.
type JsonFileQueue struct {
	mu         sync.Mutex
	path       string
	messages   []QueuedMessage
	maxRetries int
	cfg        Config
	dl         *DeadLetter
	dropped    int
}

// NewJsonFileQueue creates or loads an existing queue from the given file path.
func NewJsonFileQueue(path string, maxRetries int) (*JsonFileQueue, error) {
	return NewJsonFileQueueWithConfig(path, maxRetries, Config{})
}

// NewJsonFileQueueWithConfig is like NewJsonFileQueue but applies a
// size cap and dead-letter writer. The cap is enforced on Enqueue;
// when full, the oldest non-exhausted message is moved to the
// dead-letter file (if any) and discarded from the active queue.
func NewJsonFileQueueWithConfig(path string, maxRetries int, cfg Config) (*JsonFileQueue, error) {
	q := &JsonFileQueue{
		path:       path,
		maxRetries: maxRetries,
		cfg:        cfg,
		dl:         NewDeadLetter(cfg.DeadLetterPath),
	}
	if err := q.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return q, nil
}

func (q *JsonFileQueue) Enqueue(msg QueuedMessage) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	msg.ID = uuid.New().String()
	msg.CreatedAt = time.Now()
	msg.MaxRetries = q.maxRetries

	if evicted, ok := q.evictOldestLocked(); ok {
		if err := q.dl.Write(evicted, "queue-full"); err != nil {
			slog.Warn("queue: failed to write evicted message to dead-letter", "id", evicted.ID, "error", err)
		} else {
			q.dropped++
		}
		slog.Warn("queue: cap exceeded, evicted oldest non-exhausted message to dead-letter",
			"id", evicted.ID, "type", evicted.Type, "idempotencyKey", evicted.IdempotencyKey,
			"queueLen", len(q.messages))
	}

	q.messages = append(q.messages, msg)
	return msg.ID, q.save()
}

func (q *JsonFileQueue) Peek() (QueuedMessage, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return QueuedMessage{}, false
	}
	return q.messages[0], true
}

func (q *JsonFileQueue) Dequeue(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, m := range q.messages {
		if m.ID == id {
			q.messages = append(q.messages[:i], q.messages[i+1:]...)
			_ = q.save()
			return
		}
	}
}

func (q *JsonFileQueue) Update(msg QueuedMessage) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, m := range q.messages {
		if m.ID == msg.ID {
			q.messages[i] = msg
			return q.save()
		}
	}
	return fmt.Errorf("queued message not found: %s", msg.ID)
}

func (q *JsonFileQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}

func (q *JsonFileQueue) All() []QueuedMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]QueuedMessage, len(q.messages))
	copy(result, q.messages)
	return result
}

// Dropped returns the cumulative number of messages moved to the
// dead-letter file (either because the queue was full at Enqueue or
// because the drain code exhausted the retry budget).
func (q *JsonFileQueue) Dropped() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

// DeadLetter exposes the underlying dead-letter writer so callers
// (the v2.0.1 drain code) can move exhausted messages to it.
func (q *JsonFileQueue) DeadLetter() *DeadLetter {
	return q.dl
}

// IncDropped records that the drain code moved a message to the
// dead-letter file. The file write itself is performed by the caller.
func (q *JsonFileQueue) IncDropped() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.dropped++
}

// evictOldestLocked removes the oldest message that still has retries
// left, returning it. Returns ok=false if no eviction is needed or no
// evictable message is found. Caller must hold q.mu.
//
// The implementation prefers dropping "stuck" messages (those that
// have already used all their retries) before evicting a fresh one;
// if all stuck messages are gone, it drops the head of the queue.
func (q *JsonFileQueue) evictOldestLocked() (QueuedMessage, bool) {
	if !q.shouldEvictLocked() {
		return QueuedMessage{}, false
	}

	// First pass: oldest exhausted message.
	for i, m := range q.messages {
		if m.MaxRetries > 0 && m.RetryCount >= m.MaxRetries {
			evicted := m
			q.messages = append(q.messages[:i], q.messages[i+1:]...)
			_ = q.save()
			return evicted, true
		}
	}

	// Second pass: oldest message of any kind.
	if len(q.messages) > 0 {
		evicted := q.messages[0]
		q.messages = q.messages[1:]
		_ = q.save()
		return evicted, true
	}
	return QueuedMessage{}, false
}

func (q *JsonFileQueue) shouldEvictLocked() bool {
	if q.cfg.MaxMessages > 0 && len(q.messages) >= q.cfg.MaxMessages {
		return true
	}
	if q.cfg.MaxBytes > 0 {
		// We avoid a serialization cost on every Enqueue by
		// approximating bytes from the in-memory representation
		// (JSON-encoded length of the message list). This is
		// conservative; a stricter implementation could re-marshal
		// incrementally.
		if data, err := json.Marshal(jsonFileData{Messages: q.messages}); err == nil && int64(len(data)) >= q.cfg.MaxBytes {
			return true
		}
	}
	return false
}

func (q *JsonFileQueue) load() error {
	data, err := os.ReadFile(q.path)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var fd jsonFileData
	if err := json.Unmarshal(data, &fd); err != nil {
		return err
	}
	q.messages = fd.Messages
	return nil
}

func (q *JsonFileQueue) save() error {
	data, err := json.MarshalIndent(jsonFileData{Messages: q.messages}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(q.path, data, 0600)
}
