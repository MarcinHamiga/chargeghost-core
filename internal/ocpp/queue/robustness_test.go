package queue

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInMemoryQueue_DroppedCountStartsAtZero(t *testing.T) {
	q := NewInMemoryQueue(3)
	assert.Equal(t, 0, q.Dropped())
}

func TestInMemoryQueue_NoEvictionWhenUnderCap(t *testing.T) {
	q := NewInMemoryQueueWithConfig(3, Config{MaxMessages: 5})
	for i := 0; i < 5; i++ {
		_, err := q.Enqueue(QueuedMessage{Type: "Test", Payload: "x"})
		require.NoError(t, err)
	}
	assert.Equal(t, 5, q.Len())
	assert.Equal(t, 0, q.Dropped(), "no eviction when at exact cap")
}

func TestInMemoryQueue_EvictsOldestWhenOverCap(t *testing.T) {
	q := NewInMemoryQueueWithConfig(3, Config{MaxMessages: 3})
	for i := 0; i < 4; i++ {
		_, err := q.Enqueue(QueuedMessage{Type: "Test", Payload: i})
		require.NoError(t, err)
	}
	assert.Equal(t, 3, q.Len())
	assert.Equal(t, 1, q.Dropped())

	// The remaining three are messages 1, 2, 3 (second through fourth enqueued).
	for i, m := range q.All() {
		assert.Equal(t, i+1, m.Payload, "remaining messages should be the second-through-fourth enqueued")
	}
}

func TestInMemoryQueue_PrefersDroppingExhaustedFirst(t *testing.T) {
	q := NewInMemoryQueueWithConfig(2, Config{MaxMessages: 2})

	exhausted, err := q.Enqueue(QueuedMessage{Type: "Test", Payload: "exhausted", MaxRetries: 1, RetryCount: 1})
	require.NoError(t, err)

	fresh, err := q.Enqueue(QueuedMessage{Type: "Test", Payload: "fresh"})
	require.NoError(t, err)

	_, err = q.Enqueue(QueuedMessage{Type: "Test", Payload: "new"})
	require.NoError(t, err)

	for _, m := range q.All() {
		assert.NotEqual(t, exhausted, m.ID, "exhausted message should be evicted before fresh")
	}
	found := false
	for _, m := range q.All() {
		if m.ID == fresh {
			found = true
		}
	}
	assert.True(t, found, "fresh message should still be in the queue")
}

func TestJsonFileQueue_PersistsAcrossInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.json")
	dl := filepath.Join(t.TempDir(), "dl.jsonl")

	q1, err := NewJsonFileQueueWithConfig(path, 3, Config{DeadLetterPath: dl})
	require.NoError(t, err)
	_, err = q1.Enqueue(QueuedMessage{Type: "Test", Payload: "x"})
	require.NoError(t, err)

	q2, err := NewJsonFileQueueWithConfig(path, 3, Config{DeadLetterPath: dl})
	require.NoError(t, err)
	assert.Equal(t, 1, q2.Len())
}

func TestJsonFileQueue_DroppedCounterPersistsWithinProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.json")
	dl := filepath.Join(t.TempDir(), "dl.jsonl")
	q, err := NewJsonFileQueueWithConfig(path, 3, Config{MaxMessages: 2, DeadLetterPath: dl})
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		_, err := q.Enqueue(QueuedMessage{Type: "Test", Payload: i})
		require.NoError(t, err)
	}
	assert.Equal(t, 2, q.Len())
	assert.Equal(t, 3, q.Dropped())

	data, err := os.ReadFile(dl)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	assert.Equal(t, 3, len(lines), "dead-letter file should have 3 records")
	for _, line := range lines {
		assert.True(t, strings.Contains(line, `"reason":"queue-full"`))
	}
}

func TestJsonFileQueue_EvictionWritesToDeadLetter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.json")
	dl := filepath.Join(t.TempDir(), "dl.jsonl")
	q, err := NewJsonFileQueueWithConfig(path, 3, Config{MaxMessages: 1, DeadLetterPath: dl})
	require.NoError(t, err)
	_, err = q.Enqueue(QueuedMessage{Type: "Test", Payload: "first", IdempotencyKey: "key-1"})
	require.NoError(t, err)
	_, err = q.Enqueue(QueuedMessage{Type: "Test", Payload: "second", IdempotencyKey: "key-2"})
	require.NoError(t, err)
	assert.Equal(t, 1, q.Len())

	data, err := os.ReadFile(dl)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"idempotency_key":"key-1"`, "first message should be in dead-letter")
}

func TestJsonFileQueue_NoDeadLetterWhenPathEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.json")
	q, err := NewJsonFileQueueWithConfig(path, 3, Config{MaxMessages: 1})
	require.NoError(t, err)
	_, err = q.Enqueue(QueuedMessage{Type: "Test", Payload: "first"})
	require.NoError(t, err)
	_, err = q.Enqueue(QueuedMessage{Type: "Test", Payload: "second"})
	require.NoError(t, err)
	assert.Equal(t, 1, q.Len())
	assert.Equal(t, 1, q.Dropped(), "dropped counter still increments even without a DLQ file")
}

func TestDeadLetter_DisabledWhenPathEmpty(t *testing.T) {
	dl := NewDeadLetter("")
	assert.False(t, dl.Enabled())
	assert.NoError(t, dl.Write(QueuedMessage{}, "test"), "Disabled writer should be a no-op")
}

func TestDeadLetter_AppendsJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dl.jsonl")
	dl := NewDeadLetter(path)
	require.True(t, dl.Enabled())

	for i := 0; i < 3; i++ {
		require.NoError(t, dl.Write(QueuedMessage{ID: "msg-" + string(rune('a'+i)), Type: "Test"}, "test"))
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	assert.Equal(t, 3, len(lines))
	for _, line := range lines {
		assert.Contains(t, line, `"reason":"test"`)
	}
}

func TestInMemoryQueue_ConcurrentEnqueueRespectsCap(t *testing.T) {
	q := NewInMemoryQueueWithConfig(3, Config{MaxMessages: 10})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := q.Enqueue(QueuedMessage{Type: "Test", Payload: i})
			assert.NoError(t, err)
		}(i)
	}
	wg.Wait()
	assert.LessOrEqual(t, q.Len(), 10)
	assert.Equal(t, 40, q.Dropped(), "40 messages should have been evicted to fit the 10-message cap")
}
