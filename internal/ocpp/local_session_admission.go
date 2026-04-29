package ocpp

import (
	"errors"
	"time"
)

var ErrLocalSessionNotAuthorized = errors.New("id tag not authorized for local start")

func AdmitLocalSession(idTag *string, connected bool, localAuthEnabled bool, localAuth LocalAuthManager, authCacheEnabled bool, authCache AuthorizationCacheStore, now time.Time) error {
	if idTag == nil || *idTag == "" || connected {
		return nil
	}

	if decision, ok := localSessionDecision(localAuthEnabled, localAuth, *idTag, now); ok {
		if decision == AuthorizationDecisionAccepted {
			return nil
		}
		return ErrLocalSessionNotAuthorized
	}

	if decision, ok := localSessionDecision(authCacheEnabled, authCache, *idTag, now); ok {
		if decision == AuthorizationDecisionAccepted {
			return nil
		}
		return ErrLocalSessionNotAuthorized
	}

	return ErrLocalSessionNotAuthorized
}

func localSessionDecision(enabled bool, source interface {
	Decision(string, time.Time) AuthorizationDecision
}, idTag string, now time.Time) (AuthorizationDecision, bool) {
	if !enabled || source == nil {
		return AuthorizationDecisionMissing, false
	}
	decision := source.Decision(idTag, now)
	if decision == AuthorizationDecisionMissing {
		return decision, false
	}
	return decision, true
}
