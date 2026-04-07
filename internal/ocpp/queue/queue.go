package queue

import "time"

// QueuedMessage is a single buffered OCPP message.
type QueuedMessage struct {
	ID         string      `json:"id"`
	Type       string      `json:"type"` // "StartTransaction" | "StopTransaction" | "MeterValues"
	Payload    interface{} `json:"payload"`
	CreatedAt  time.Time   `json:"created_at"`
	RetryCount int         `json:"retry_count"`
	MaxRetries int         `json:"max_retries"`
}

// MessageQueue is a FIFO buffer for offline OCPP messages.
type MessageQueue interface {
	// Enqueue adds a message and returns its assigned ID.
	Enqueue(msg QueuedMessage) (string, error)
	// Peek returns the oldest message without removing it.
	Peek() (QueuedMessage, bool)
	// Dequeue removes the message with the given ID.
	Dequeue(id string)
	// Len returns the number of queued messages.
	Len() int
	// All returns a snapshot of all queued messages in FIFO order.
	All() []QueuedMessage
}

// NewQueue creates the appropriate queue backend based on persist flag.
// If persist is true and path is non-empty, creates a JsonFileQueue.
// Otherwise creates an InMemoryQueue.
func NewQueue(persist bool, path string, maxRetries int) (MessageQueue, error) {
	if persist && path != "" {
		return NewJsonFileQueue(path, maxRetries)
	}
	return NewInMemoryQueue(maxRetries), nil
}
