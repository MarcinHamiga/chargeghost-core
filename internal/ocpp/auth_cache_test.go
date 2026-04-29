package ocpp_test

import (
	"testing"
	"time"

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

func TestAuthorizationCache_Decision(t *testing.T) {
	now := time.Now()
	expired := now.Add(-1 * time.Minute)

	tests := []struct {
		name     string
		setup    func(c *ocpp.AuthorizationCache)
		idTag    string
		expected ocpp.AuthorizationDecision
	}{
		{
			name: "accepted",
			setup: func(c *ocpp.AuthorizationCache) {
				c.Put("ACCEPTED", "accepted", nil)
			},
			idTag:    "ACCEPTED",
			expected: ocpp.AuthorizationDecisionAccepted,
		},
		{
			name: "blocked",
			setup: func(c *ocpp.AuthorizationCache) {
				c.Put("BLOCKED", "Blocked", nil)
			},
			idTag:    "BLOCKED",
			expected: ocpp.AuthorizationDecisionBlocked,
		},
		{
			name: "expired entry",
			setup: func(c *ocpp.AuthorizationCache) {
				c.Put("EXPIRED", "Accepted", &expired)
			},
			idTag:    "EXPIRED",
			expected: ocpp.AuthorizationDecisionExpired,
		},
		{
			name:     "missing",
			setup:    func(c *ocpp.AuthorizationCache) {},
			idTag:    "MISSING",
			expected: ocpp.AuthorizationDecisionMissing,
		},
		{
			name: "malformed status",
			setup: func(c *ocpp.AuthorizationCache) {
				c.Put("MALFORMED", "not-valid", nil)
			},
			idTag:    "MALFORMED",
			expected: ocpp.AuthorizationDecisionMalformed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := ocpp.NewAuthorizationCache()
			tt.setup(c)
			assert.Equal(t, tt.expected, c.Decision(tt.idTag, now))
		})
	}
}
