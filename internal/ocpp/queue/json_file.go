package queue

import (
	"encoding/json"
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
}

// NewJsonFileQueue creates or loads an existing queue from the given file path.
func NewJsonFileQueue(path string, maxRetries int) (*JsonFileQueue, error) {
	q := &JsonFileQueue{path: path, maxRetries: maxRetries}
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
