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
