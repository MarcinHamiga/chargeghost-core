package queue

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// InMemoryQueue is a thread-safe in-memory FIFO queue. Messages are lost on restart.
type InMemoryQueue struct {
	mu         sync.Mutex
	messages   []QueuedMessage
	maxRetries int
}

func NewInMemoryQueue(maxRetries int) *InMemoryQueue {
	return &InMemoryQueue{maxRetries: maxRetries}
}

func (q *InMemoryQueue) Enqueue(msg QueuedMessage) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	msg.ID = uuid.New().String()
	msg.CreatedAt = time.Now()
	msg.MaxRetries = q.maxRetries
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
