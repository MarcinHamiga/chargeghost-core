package persistence

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockPersistable struct {
	saveCount atomic.Int32
}

func (m *mockPersistable) SaveState(dir string) error {
	m.saveCount.Add(1)
	return nil
}

func (m *mockPersistable) LoadState(dir string) error {
	return nil
}

func TestCoordinator_PeriodicSave(t *testing.T) {
	mock := &mockPersistable{}
	coord := NewCoordinator(t.TempDir(), 50*time.Millisecond, mock)

	ctx, cancel := context.WithCancel(context.Background())
	go coord.Run(ctx)

	time.Sleep(180 * time.Millisecond)
	cancel()

	// Should have fired at least 2 times in 180ms with 50ms interval.
	assert.GreaterOrEqual(t, mock.saveCount.Load(), int32(2))
}

func TestCoordinator_SaveAll(t *testing.T) {
	mock := &mockPersistable{}
	coord := NewCoordinator(t.TempDir(), time.Hour, mock)

	coord.SaveAll()
	assert.Equal(t, int32(1), mock.saveCount.Load())
}
