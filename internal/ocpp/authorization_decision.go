package ocpp

import (
	"strings"
	"time"
)

type AuthorizationDecision string

const (
	AuthorizationDecisionAccepted  AuthorizationDecision = "Accepted"
	AuthorizationDecisionBlocked   AuthorizationDecision = "Blocked"
	AuthorizationDecisionExpired   AuthorizationDecision = "Expired"
	AuthorizationDecisionMissing   AuthorizationDecision = "Missing"
	AuthorizationDecisionMalformed AuthorizationDecision = "Malformed"
)

func NormalizeAuthorizationStatus(status string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "accepted":
		return "Accepted", true
	case "blocked":
		return "Blocked", true
	case "expired":
		return "Expired", true
	case "concurrenttx":
		return "ConcurrentTx", true
	default:
		return "", false
	}
}

func authorizationDecision(status string, expiry *time.Time, now time.Time) AuthorizationDecision {
	if expiry != nil && now.After(*expiry) {
		return AuthorizationDecisionExpired
	}

	normalized, ok := NormalizeAuthorizationStatus(status)
	if !ok {
		return AuthorizationDecisionMalformed
	}

	switch normalized {
	case "Accepted":
		return AuthorizationDecisionAccepted
	case "Expired":
		return AuthorizationDecisionExpired
	default:
		return AuthorizationDecisionBlocked
	}
}
