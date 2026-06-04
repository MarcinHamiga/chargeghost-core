package queue

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// InMemoryQueue is a thread-safe in-memory FIFO queue. Messages are lost on restart.
type InMemoryQueue struct {
	mu         sync.Mutex
	messages   []QueuedMessage
	maxRetries int
	cfg        Config
	dl         *DeadLetter
	dropped    int
}

func NewInMemoryQueue(maxRetries int) *InMemoryQueue {
	return NewInMemoryQueueWithConfig(maxRetries, Config{})
}

// NewInMemoryQueueWithConfig is like NewInMemoryQueue but applies a
// size cap and dead-letter writer. When the cap is exceeded, the
// oldest message is moved to the dead-letter file (if any) and
// discarded.
func NewInMemoryQueueWithConfig(maxRetries int, cfg Config) *InMemoryQueue {
	return &InMemoryQueue{
		maxRetries: maxRetries,
		cfg:        cfg,
		dl:         NewDeadLetter(cfg.DeadLetterPath),
	}
}

func (q *InMemoryQueue) Enqueue(msg QueuedMessage) (string, error) {
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
	return msg.ID, nil
}

func (q *InMemoryQueue) Peek() (QueuedMessage, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.messages) == 0 {
		return QueuedMessage{}, false
	}
	return q.messages[0], true
}

func (q *InMemoryQueue) Dequeue(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, m := range q.messages {
		if m.ID == id {
			q.messages = append(q.messages[:i], q.messages[i+1:]...)
			return
		}
	}
}

func (q *InMemoryQueue) Update(msg QueuedMessage) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, m := range q.messages {
		if m.ID == msg.ID {
			q.messages[i] = msg
			return nil
		}
	}
	return fmt.Errorf("queued message not found: %s", msg.ID)
}

func (q *InMemoryQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages)
}

func (q *InMemoryQueue) All() []QueuedMessage {
	q.mu.Lock()
	defer q.mu.Unlock()
	result := make([]QueuedMessage, len(q.messages))
	copy(result, q.messages)
	return result
}

// Dropped returns the cumulative number of messages moved to the
// dead-letter file since process start.
func (q *InMemoryQueue) Dropped() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

// DeadLetter exposes the underlying dead-letter writer.
func (q *InMemoryQueue) DeadLetter() *DeadLetter {
	return q.dl
}

// IncDropped records that the drain code moved a message to the
// dead-letter file.
func (q *InMemoryQueue) IncDropped() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.dropped++
}

func (q *InMemoryQueue) evictOldestLocked() (QueuedMessage, bool) {
	if !q.shouldEvictLocked() {
		return QueuedMessage{}, false
	}
	// Prefer dropping exhausted messages.
	for i, m := range q.messages {
		if m.MaxRetries > 0 && m.RetryCount >= m.MaxRetries {
			evicted := m
			q.messages = append(q.messages[:i], q.messages[i+1:]...)
			return evicted, true
		}
	}
	if len(q.messages) > 0 {
		evicted := q.messages[0]
		q.messages = q.messages[1:]
		return evicted, true
	}
	return QueuedMessage{}, false
}

func (q *InMemoryQueue) shouldEvictLocked() bool {
	if q.cfg.MaxMessages > 0 && len(q.messages) >= q.cfg.MaxMessages {
		return true
	}
	return false
}
