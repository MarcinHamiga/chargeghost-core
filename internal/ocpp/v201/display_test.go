package v201

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDisplayMessageStore_SetAndGet(t *testing.T) {
	store := NewDisplayMessageStore()
	store.Set(DisplayMessage{ID: 1, Priority: "NormalCycle", Text: "Welcome"})

	msg, ok := store.Get(1)
	require.True(t, ok)
	assert.Equal(t, "Welcome", msg.Text)
}

func TestDisplayMessageStore_Clear(t *testing.T) {
	store := NewDisplayMessageStore()
	store.Set(DisplayMessage{ID: 1, Priority: "NormalCycle", Text: "Hello"})

	ok := store.Clear(1)
	assert.True(t, ok)

	_, found := store.Get(1)
	assert.False(t, found)
}

func TestDisplayMessageStore_GetAll(t *testing.T) {
	store := NewDisplayMessageStore()
	store.Set(DisplayMessage{ID: 1, Priority: "NormalCycle", Text: "A"})
	store.Set(DisplayMessage{ID: 2, Priority: "InFront", Text: "B"})

	all := store.GetAll()
	assert.Len(t, all, 2)
}
