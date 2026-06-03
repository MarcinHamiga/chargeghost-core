package v201

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chargeghost/engine/internal/ocpp/queue"
)

func TestDrainQueue_InvalidPayloadNotDequeued(t *testing.T) {
	b := newTestBridge(t)
	b.queue = queue.NewInMemoryQueue(3)
	b.connected.Store(true)

	_, err := b.queue.Enqueue(queue.QueuedMessage{
		Type:    "TransactionEvent",
		Payload: map[string]string{"invalid": "payload"},
	})
	require.NoError(t, err)

	b.drainQueue()

	assert.Equal(t, 1, b.queue.Len())
	msg, ok := b.queue.Peek()
	require.True(t, ok)
	assert.Equal(t, 1, msg.RetryCount)
	assert.NotEmpty(t, msg.LastError)
}
