package ocpp_test

import (
	"testing"

	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/stretchr/testify/assert"
)

func TestAuthorizationCache_PutAndGet(t *testing.T) {
	c := ocpp.NewAuthorizationCache()
	c.Put("ABC123", "Accepted", nil)

	status, expiry, found := c.Get("ABC123")
	assert.True(t, found)
	assert.Equal(t, "Accepted", status)
	assert.Nil(t, expiry)
}

func TestAuthorizationCache_Remove(t *testing.T) {
	c := ocpp.NewAuthorizationCache()
	c.Put("ABC123", "Accepted", nil)
	c.Remove("ABC123")
	_, _, found := c.Get("ABC123")
	assert.False(t, found)
}

func TestAuthorizationCache_Clear(t *testing.T) {
	c := ocpp.NewAuthorizationCache()
	c.Put("A", "Accepted", nil)
	c.Put("B", "Blocked", nil)
	c.Clear()
	assert.Equal(t, 0, c.Size())
}
