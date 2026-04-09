package queue_test

import (
	"os"
	"testing"

	"github.com/chargeghost/engine/internal/ocpp/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testQueue(t *testing.T, q queue.MessageQueue) {
	t.Helper()
	assert.Equal(t, 0, q.Len())

	id1, err := q.Enqueue(queue.QueuedMessage{Type: "StartTransaction", Payload: map[string]string{"foo": "bar"}})
	require.NoError(t, err)
	assert.NotEmpty(t, id1)

	id2, _ := q.Enqueue(queue.QueuedMessage{Type: "StopTransaction", Payload: nil})
	assert.Equal(t, 2, q.Len())

	msg, ok := q.Peek()
	assert.True(t, ok)
	assert.Equal(t, "StartTransaction", msg.Type)
	assert.Equal(t, id1, msg.ID)

	q.Dequeue(id1)
	assert.Equal(t, 1, q.Len())

	msg, ok = q.Peek()
	assert.True(t, ok)
	assert.Equal(t, id2, msg.ID)
}

func TestInMemoryQueue(t *testing.T) {
	q := queue.NewInMemoryQueue(3)
	testQueue(t, q)
}

func TestJsonFileQueue(t *testing.T) {
	f, err := os.CreateTemp("", "queue-*.json")
	require.NoError(t, err)
	f.Close()
	defer os.Remove(f.Name())

	q, err := queue.NewJsonFileQueue(f.Name(), 3)
	require.NoError(t, err)
	testQueue(t, q)

	// Verify persistence: create a new queue from same file.
	q2, err := queue.NewJsonFileQueue(f.Name(), 3)
	require.NoError(t, err)
	assert.Equal(t, 1, q2.Len()) // "StopTransaction" survived
}
