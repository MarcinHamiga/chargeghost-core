package v201

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCostStore_UpdateAndGet(t *testing.T) {
	store := NewCostStore()
	store.Update("tx-123", 12.50)

	cost, ok := store.Get("tx-123")
	assert.True(t, ok)
	assert.Equal(t, 12.50, cost)
}

func TestCostStore_GetUnknown(t *testing.T) {
	store := NewCostStore()
	_, ok := store.Get("unknown")
	assert.False(t, ok)
}

func TestCostStore_Clear(t *testing.T) {
	store := NewCostStore()
	store.Update("tx-123", 10.0)
	store.Clear("tx-123")

	_, ok := store.Get("tx-123")
	assert.False(t, ok)
}
