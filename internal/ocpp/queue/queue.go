package queue

import "time"

// QueuedMessage is a single buffered OCPP message.
type QueuedMessage struct {
	ID            string      `json:"id"`
	Type          string      `json:"type"` // "StartTransaction" | "StopTransaction" | "MeterValues"
	Payload       interface{} `json:"payload"`
	CreatedAt     time.Time   `json:"created_at"`
	LastAttemptAt *time.Time  `json:"last_attempt_at,omitempty"`
	RetryCount    int         `json:"retry_count"`
	MaxRetries    int         `json:"max_retries"`
	LastError     string      `json:"last_error,omitempty"`
	// IdempotencyKey, when set, is a stable identifier derived from the
	// logical event (e.g. txID+seqNo for TransactionEvent). It travels with
	// the queued message so that a replay carries the same key, and is
	// logged on every send attempt and on dead-lettering.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// Config controls the size and behavior of the queue backends.
// Zero values mean "unlimited".
type Config struct {
	// MaxMessages caps the number of messages kept in the queue.
	// When Enqueue would exceed this, the oldest non-exhausted message
	// is moved to the dead-letter file (if configured) and dropped.
	MaxMessages int
	// MaxBytes caps the total serialized size of the queue.
	// When Enqueue would exceed this, the oldest non-exhausted message
	// is moved to the dead-letter file (if configured) and dropped.
	MaxBytes int64
	// DeadLetterPath, if non-empty, is the file that receives messages
	// dropped from the queue (overflow) or moved out of rotation
	// (retries exhausted).
	DeadLetterPath string
}

// MessageQueue is a FIFO buffer for offline OCPP messages.
type MessageQueue interface {
	// Enqueue adds a message and returns its assigned ID.
	Enqueue(msg QueuedMessage) (string, error)
	// Peek returns the oldest message without removing it.
	Peek() (QueuedMessage, bool)
	// Dequeue removes the message with the given ID.
	Dequeue(id string)
	// Update replaces a queued message in place.
	Update(msg QueuedMessage) error
	// Len returns the number of queued messages.
	Len() int
	// All returns a snapshot of all queued messages in FIFO order.
	All() []QueuedMessage
	// Dropped returns the cumulative number of messages that have been
	// moved to dead-letter (or otherwise removed from the active queue)
	// since process start. Useful for the status endpoint and metrics.
	Dropped() int
}

// DeadLetterQueue is an optional capability interface implemented by
// production queue backends (JsonFileQueue, InMemoryQueue) that
// support dead-lettering. Consumers (such as the v2.0.1 drain code)
// type-assert to it before calling DeadLetter()/IncDropped() so that
// simple test mocks can implement just MessageQueue.
type DeadLetterQueue interface {
	MessageQueue
	DeadLetter() *DeadLetter
	IncDropped()
}

// NewQueue creates the appropriate queue backend based on persist flag.
// If persist is true and path is non-empty, creates a JsonFileQueue.
// Otherwise creates an InMemoryQueue.
func NewQueue(persist bool, path string, maxRetries int) (MessageQueue, error) {
	return NewQueueWithConfig(persist, path, maxRetries, Config{})
}

// NewQueueWithConfig is like NewQueue but applies the given Config to the
// resulting backend. The dead-letter file is only attached when both
// Config.DeadLetterPath is non-empty and the chosen backend supports it.
func NewQueueWithConfig(persist bool, path string, maxRetries int, cfg Config) (MessageQueue, error) {
	if persist && path != "" {
		return NewJsonFileQueueWithConfig(path, maxRetries, cfg)
	}
	return NewInMemoryQueueWithConfig(maxRetries, cfg), nil
}
