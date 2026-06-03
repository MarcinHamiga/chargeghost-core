package v201

import (
	"time"

	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
)

func (b *Bridge201) authorizationCacheDecision(idTag string, now time.Time) ocpppkg.AuthorizationDecision {
	if b.authCache == nil || !b.deviceModelBool("AuthCtrlr", "Enabled", true) {
		return ocpppkg.AuthorizationDecisionMissing
	}
	return b.authCache.Decision(idTag, now)
}

func (b *Bridge201) cacheAuthorizationDecision(idTag, status string, expiry *time.Time) {
	if b.authCache == nil || !b.deviceModelBool("AuthCtrlr", "Enabled", true) {
		return
	}
	b.authCache.Put(idTag, status, expiry)
}

func (b *Bridge201) localAuthorizationDecision(idTag string, now time.Time) ocpppkg.AuthorizationDecision {
	if b.localAuth == nil || !b.deviceModelBool("AuthCtrlr", "Enabled", true) {
		return ocpppkg.AuthorizationDecisionMissing
	}
	if !b.deviceModelBool("AuthCtrlr", "LocalAuthorizeOffline", true) {
		return ocpppkg.AuthorizationDecisionMissing
	}
	return b.localAuth.Decision(idTag, now)
}

func (b *Bridge201) admitRemoteStart(idTag string, now time.Time) bool {
	if !b.deviceModelBool("AuthCtrlr", "AuthorizeRemoteStart", true) {
		return true
	}
	if b.IsConnected() {
		return b.SendAuthorize(idTag) == nil
	}

	decision := b.localAuthorizationDecision(idTag, now)
	if decision != ocpppkg.AuthorizationDecisionMissing {
		return decision == ocpppkg.AuthorizationDecisionAccepted
	}

	return b.authorizationCacheDecision(idTag, now) == ocpppkg.AuthorizationDecisionAccepted
}
