package ocpp

import (
	"errors"
	"log/slog"
	"time"
)

var ErrLocalSessionNotAuthorized = errors.New("id tag not authorized for local start")

// AdmitLocalSession determines whether an idTag may start a session
// locally (without consulting the CSMS). The decision chain — local
// authorization list, then authorization cache — is logged at Debug
// level so operators can audit "why was this idTag accepted offline".
// Use RedactIDTag when surfacing the idTag in user-facing log lines.
func AdmitLocalSession(idTag *string, connected bool, localAuthEnabled bool, localAuth LocalAuthManager, authCacheEnabled bool, authCache AuthorizationCacheStore, now time.Time) error {
	if idTag == nil || *idTag == "" || connected {
		return nil
	}

	redacted := RedactIDTag(*idTag)

	if decision, ok := localSessionDecision(localAuthEnabled, localAuth, *idTag, now); ok {
		slog.Debug("auth decision: local list",
			"idTag", redacted,
			"source", "localList",
			"decision", string(decision),
		)
		if decision == AuthorizationDecisionAccepted {
			slog.Info("local session admitted", "idTag", redacted, "source", "localList")
			return nil
		}
		return ErrLocalSessionNotAuthorized
	}

	if decision, ok := localSessionDecision(authCacheEnabled, authCache, *idTag, now); ok {
		slog.Debug("auth decision: cache",
			"idTag", redacted,
			"source", "authCache",
			"decision", string(decision),
		)
		if decision == AuthorizationDecisionAccepted {
			slog.Info("local session admitted", "idTag", redacted, "source", "authCache")
			return nil
		}
		return ErrLocalSessionNotAuthorized
	}

	slog.Debug("auth decision: miss",
		"idTag", redacted,
		"localListEnabled", localAuthEnabled,
		"cacheEnabled", authCacheEnabled,
		"decision", "Blocked",
	)
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
