package v16

import (
	"testing"
	"time"

	wsapi "github.com/chargeghost/engine/internal/api/ws"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/localauth"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/smartcharging"
	ocpp16types "github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

func makeAbsoluteProfile(profileID, connectorID, stackLevel int, limitA float64, purpose string) engine.ChargingProfile {
	return engine.ChargingProfile{
		ProfileID:     profileID,
		ConnectorID:   connectorID,
		StackLevel:    stackLevel,
		Purpose:       purpose,
		Kind:          "Absolute",
		StartSchedule: ptrTime(time.Now().Add(-1 * time.Hour)),
		Schedule: engine.ChargingSchedule{
			ChargingRateUnit: "A",
			Periods: []engine.ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: limitA},
			},
		},
	}
}

func ptrTime(v time.Time) *time.Time { return &v }

func newTestBridge16(t *testing.T) *Bridge16 {
	t.Helper()

	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	dispatcher := ocpp.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)

	return NewBridge(
		e,
		wsapi.NewHub(),
		cfg,
		dispatcher,
		NewChargingProfileManager(),
		NewConfigKeyManager(),
		ocpp.NewAuthorizationCache(),
		ocpp.NewLocalAuthListManager(),
		q,
		ocpp.NewFirmwareManager(nil),
		ocpp.NewDiagnosticsManager(nil),
		ocpp.NewDataTransferRegistry(),
		nil,
	)
}

func makeRemoteStartChargingProfile(profileID int) *ocpp16types.ChargingProfile {
	return &ocpp16types.ChargingProfile{
		ChargingProfileId: profileID,
		StackLevel:        1,
		ChargingSchedule: &ocpp16types.ChargingSchedule{
			ChargingRateUnit: ocpp16types.ChargingRateUnitAmperes,
			ChargingSchedulePeriod: []ocpp16types.ChargingSchedulePeriod{
				{StartPeriod: 0, Limit: 16},
			},
		},
	}
}

func TestOnRemoteStartTransaction_AcceptsAdmittedTag(t *testing.T) {
	b := newTestBridge16(t)
	b.engine.AddConnector(230.0, 32.0, 1)
	require.NoError(t, b.localAuth.UpdateList(1, []ocpp.LocalAuthEntry{{IDTag: "TAG-ACCEPTED", Status: "Accepted"}}, "Full"))

	connectorID := 1
	resp, err := b.OnRemoteStartTransaction(&core.RemoteStartTransactionRequest{
		ConnectorId:     &connectorID,
		IdTag:           "TAG-ACCEPTED",
		ChargingProfile: makeRemoteStartChargingProfile(77),
	})
	require.NoError(t, err)
	assert.Equal(t, ocpp16types.RemoteStartStopStatusAccepted, resp.Status)
	assert.Nil(t, b.engine.GetSession(1))

	b.engine.PlugIn(1)
	session := b.engine.GetSession(1)
	require.NotNil(t, session)
	require.NotNil(t, session.RemoteStartChargingProfile)
	assert.Equal(t, 77, session.RemoteStartChargingProfile.ProfileID)
}

func TestOnRemoteStartTransaction_RejectsBlockedTag(t *testing.T) {
	b := newTestBridge16(t)
	b.engine.AddConnector(230.0, 32.0, 1)
	b.authCache.Put("TAG-BLOCKED", "Blocked", nil)

	connectorID := 1
	resp, err := b.OnRemoteStartTransaction(&core.RemoteStartTransactionRequest{
		ConnectorId:     &connectorID,
		IdTag:           "TAG-BLOCKED",
		ChargingProfile: makeRemoteStartChargingProfile(88),
	})
	require.NoError(t, err)
	assert.Equal(t, ocpp16types.RemoteStartStopStatusRejected, resp.Status)

	b.engine.PlugIn(1)
	assert.Nil(t, b.engine.GetSession(1))
}

func TestOnRemoteStartTransaction_RejectsExpiredTag(t *testing.T) {
	b := newTestBridge16(t)
	b.engine.AddConnector(230.0, 32.0, 1)
	expiredAt := time.Now().Add(-1 * time.Minute)
	b.authCache.Put("TAG-EXPIRED", "Accepted", &expiredAt)

	connectorID := 1
	resp, err := b.OnRemoteStartTransaction(&core.RemoteStartTransactionRequest{
		ConnectorId: &connectorID,
		IdTag:       "TAG-EXPIRED",
	})
	require.NoError(t, err)
	assert.Equal(t, ocpp16types.RemoteStartStopStatusRejected, resp.Status)

	b.engine.PlugIn(1)
	assert.Nil(t, b.engine.GetSession(1))
}

func TestOnRemoteStartTransaction_RejectsUnknownOfflineTag(t *testing.T) {
	b := newTestBridge16(t)
	b.engine.AddConnector(230.0, 32.0, 1)

	connectorID := 1
	resp, err := b.OnRemoteStartTransaction(&core.RemoteStartTransactionRequest{
		ConnectorId: &connectorID,
		IdTag:       "TAG-UNKNOWN",
	})
	require.NoError(t, err)
	assert.Equal(t, ocpp16types.RemoteStartStopStatusRejected, resp.Status)

	b.engine.PlugIn(1)
	assert.Nil(t, b.engine.GetSession(1))
}

func TestOnRemoteStartTransaction_RejectsMissingConnector(t *testing.T) {
	b := newTestBridge16(t)
	require.NoError(t, b.localAuth.UpdateList(1, []ocpp.LocalAuthEntry{{IDTag: "TAG-ACCEPTED", Status: "Accepted"}}, "Full"))

	connectorID := 99
	resp, err := b.OnRemoteStartTransaction(&core.RemoteStartTransactionRequest{
		ConnectorId: &connectorID,
		IdTag:       "TAG-ACCEPTED",
	})
	require.NoError(t, err)
	assert.Equal(t, ocpp16types.RemoteStartStopStatusRejected, resp.Status)
}

func TestOnSendLocalList_DifferentialDelete(t *testing.T) {
	b := newTestBridge16(t)

	fullReq := localauth.NewSendLocalListRequest(1, localauth.UpdateTypeFull)
	fullReq.LocalAuthorizationList = []localauth.AuthorizationData{
		{
			IdTag:     "TAG1",
			IdTagInfo: &ocpp16types.IdTagInfo{Status: ocpp16types.AuthorizationStatusAccepted},
		},
		{
			IdTag:     "TAG2",
			IdTagInfo: &ocpp16types.IdTagInfo{Status: ocpp16types.AuthorizationStatusBlocked},
		},
	}

	resp, err := b.OnSendLocalList(fullReq)
	require.NoError(t, err)
	assert.Equal(t, localauth.UpdateStatusAccepted, resp.Status)

	diffReq := localauth.NewSendLocalListRequest(2, localauth.UpdateTypeDifferential)
	diffReq.LocalAuthorizationList = []localauth.AuthorizationData{{IdTag: "TAG1"}}

	resp, err = b.OnSendLocalList(diffReq)
	require.NoError(t, err)
	assert.Equal(t, localauth.UpdateStatusAccepted, resp.Status)
	assert.Nil(t, b.localAuth.GetEntry("TAG1"))
	assert.NotNil(t, b.localAuth.GetEntry("TAG2"))
	assert.Equal(t, 2, b.localAuth.GetVersion())
}

func TestOnSendLocalList_DifferentialDeleteMissingEntry(t *testing.T) {
	b := newTestBridge16(t)

	require.NoError(t, b.localAuth.UpdateList(1, []ocpp.LocalAuthEntry{{IDTag: "TAG1", Status: "Accepted"}}, "Full"))

	req := localauth.NewSendLocalListRequest(2, localauth.UpdateTypeDifferential)
	req.LocalAuthorizationList = []localauth.AuthorizationData{{IdTag: "MISSING"}}

	resp, err := b.OnSendLocalList(req)
	require.NoError(t, err)
	assert.Equal(t, localauth.UpdateStatusAccepted, resp.Status)
	assert.NotNil(t, b.localAuth.GetEntry("TAG1"))
	assert.Equal(t, 2, b.localAuth.GetVersion())
}

func TestOnSendLocalList_NormalizesStatus(t *testing.T) {
	b := newTestBridge16(t)

	req := localauth.NewSendLocalListRequest(1, localauth.UpdateTypeFull)
	req.LocalAuthorizationList = []localauth.AuthorizationData{
		{
			IdTag:     "TAG1",
			IdTagInfo: &ocpp16types.IdTagInfo{Status: ocpp16types.AuthorizationStatus("accepted")},
		},
	}

	resp, err := b.OnSendLocalList(req)
	require.NoError(t, err)
	assert.Equal(t, localauth.UpdateStatusAccepted, resp.Status)

	entry := b.localAuth.GetEntry("TAG1")
	require.NotNil(t, entry)
	assert.Equal(t, "Accepted", entry.Status)
}

func TestOnClearChargingProfile_ReturnsUnknownWhenNothingCleared(t *testing.T) {
	b := newTestBridge16(t)

	profile := makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile")
	require.NoError(t, b.profileManager.SetChargingProfile(1, profile))

	request := smartcharging.NewClearChargingProfileRequest()
	profileID := 999
	request.Id = &profileID

	resp, err := b.OnClearChargingProfile(request)
	require.NoError(t, err)
	assert.Equal(t, smartcharging.ClearChargingProfileStatusUnknown, resp.Status)

	_, ok := b.profileManager.GetChargingProfile(1, 1)
	assert.True(t, ok)
}

func TestOnClearChargingProfile_UsesStackLevelFilter(t *testing.T) {
	b := newTestBridge16(t)

	require.NoError(t, b.profileManager.SetChargingProfile(1, makeAbsoluteProfile(1, 1, 0, 16.0, "TxDefaultProfile")))
	require.NoError(t, b.profileManager.SetChargingProfile(1, makeAbsoluteProfile(2, 1, 1, 24.0, "TxDefaultProfile")))

	request := smartcharging.NewClearChargingProfileRequest()
	connectorID := 1
	stackLevel := 1
	request.ConnectorId = &connectorID
	request.StackLevel = &stackLevel

	resp, err := b.OnClearChargingProfile(request)
	require.NoError(t, err)
	assert.Equal(t, smartcharging.ClearChargingProfileStatusAccepted, resp.Status)

	_, ok := b.profileManager.GetChargingProfile(1, 1)
	assert.True(t, ok)
	_, ok = b.profileManager.GetChargingProfile(1, 2)
	assert.False(t, ok)
}

func TestOnUnlockConnector_ReturnsNotSupported(t *testing.T) {
	b := newTestBridge16(t)

	resp, err := b.OnUnlockConnector(core.NewUnlockConnectorRequest(1))
	require.NoError(t, err)
	assert.Equal(t, core.UnlockStatusNotSupported, resp.Status)
}

func TestOnClearCache_RemovesCachedAuthorizationDecisions(t *testing.T) {
	b := newTestBridge16(t)
	now := time.Now()

	b.cacheAuthorizationDecision("TAG1", "Accepted", nil)
	assert.Equal(t, ocpp.AuthorizationDecisionAccepted, b.authorizationCacheDecision("TAG1", now))

	resp, err := b.OnClearCache(core.NewClearCacheRequest())
	require.NoError(t, err)
	assert.Equal(t, core.ClearCacheStatusAccepted, resp.Status)
	assert.Equal(t, ocpp.AuthorizationDecisionMissing, b.authorizationCacheDecision("TAG1", now))
	assert.Equal(t, 0, b.authCache.Size())
}

func TestAuthorizationCacheEnabledFalse_BypassesCacheReadsAndWrites(t *testing.T) {
	b := newTestBridge16(t)
	now := time.Now()

	b.authCache.Put("PREEXISTING", "Accepted", nil)
	assert.Equal(t, "true", b.configKeys.GetConfigValue("AuthorizationCacheEnabled"))
	assert.Equal(t, ocpp.AuthorizationDecisionAccepted, b.authorizationCacheDecision("PREEXISTING", now))

	result := b.configKeys.SetConfigValue("AuthorizationCacheEnabled", "false")
	assert.Equal(t, "Accepted", result)
	assert.False(t, b.configKeys.GetAuthorizationCacheEnabled())
	assert.Equal(t, ocpp.AuthorizationDecisionMissing, b.authorizationCacheDecision("PREEXISTING", now))

	b.cacheAuthorizationDecision("NEW", "Accepted", nil)
	_, _, found := b.authCache.Get("NEW")
	assert.False(t, found)
	assert.Equal(t, 1, b.authCache.Size())
	assert.Equal(t, ocpp.AuthorizationDecisionAccepted, b.authCache.Decision("PREEXISTING", now))
}

func TestLocalAuthListEnabledFalse_BypassesLocalAuthReads(t *testing.T) {
	b := newTestBridge16(t)
	now := time.Now()

	require.NoError(t, b.localAuth.UpdateList(1, []ocpp.LocalAuthEntry{{IDTag: "LOCAL1", Status: "Accepted"}}, "Full"))
	assert.Equal(t, ocpp.AuthorizationDecisionAccepted, b.localAuthorizationDecision("LOCAL1", now))

	result := b.configKeys.SetConfigValue("LocalAuthListEnabled", "false")
	assert.Equal(t, "Accepted", result)
	assert.False(t, b.configKeys.GetLocalAuthListEnabled())
	assert.Equal(t, ocpp.AuthorizationDecisionMissing, b.localAuthorizationDecision("LOCAL1", now))
	assert.NotNil(t, b.localAuth.GetEntry("LOCAL1"))
}
