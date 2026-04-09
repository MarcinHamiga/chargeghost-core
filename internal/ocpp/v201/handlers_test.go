package v201

import (
	"testing"
	"time"

	data201 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/data"
	firmware201 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/firmware"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/localauth"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/remotecontrol"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

func newTestBridge(t *testing.T) *Bridge201 {
	t.Helper()
	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	dispatcher := ocpppkg.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)
	b := NewBridge(e, nil, cfg, dispatcher, q)
	fw := ocpppkg.NewFirmwareManager(nil)
	diag := ocpppkg.NewDiagnosticsManager(nil)
	dt := ocpppkg.NewDataTransferRegistry()
	la := ocpppkg.NewLocalAuthListManager()
	auth := ocpppkg.NewAuthorizationCache()
	b.SetManagers(auth, la, fw, diag, dt)
	return b
}

// --- Reset ---

func TestOnReset_Immediate(t *testing.T) {
	b := newTestBridge(t)
	req := provisioning.NewResetRequest(provisioning.ResetTypeImmediate)
	resp, err := b.OnReset(req)
	require.NoError(t, err)
	assert.Equal(t, provisioning.ResetStatusAccepted, resp.Status)
	assert.False(t, b.pendingReset.Load())
}

func TestOnReset_OnIdle_NoActiveTx(t *testing.T) {
	b := newTestBridge(t)
	req := provisioning.NewResetRequest(provisioning.ResetTypeOnIdle)
	resp, err := b.OnReset(req)
	require.NoError(t, err)
	assert.Equal(t, provisioning.ResetStatusAccepted, resp.Status)
	assert.False(t, b.pendingReset.Load())
}

func TestOnReset_OnIdle_WithActiveTx(t *testing.T) {
	b := newTestBridge(t)
	b.mu.Lock()
	b.txBuilders[1] = NewTransactionEventBuilder(1, 1)
	b.mu.Unlock()

	req := provisioning.NewResetRequest(provisioning.ResetTypeOnIdle)
	resp, err := b.OnReset(req)
	require.NoError(t, err)
	assert.Equal(t, provisioning.ResetStatusScheduled, resp.Status)
	assert.True(t, b.pendingReset.Load())
}

// --- Firmware ---

func TestOnUpdateFirmware_Accepted(t *testing.T) {
	b := newTestBridge(t)
	fwReq := &firmware201.UpdateFirmwareRequest{
		RequestID: 1,
		Firmware: firmware201.Firmware{
			Location:         "http://example.com/fw.bin",
			RetrieveDateTime: ocpp201types.NewDateTime(time.Now().Add(time.Minute)),
		},
	}
	resp, err := b.OnUpdateFirmware(fwReq)
	require.NoError(t, err)
	assert.Equal(t, firmware201.UpdateFirmwareStatusAccepted, resp.Status)
}

// --- LocalAuth ---

func TestOnGetLocalListVersion(t *testing.T) {
	b := newTestBridge(t)
	req := &localauth.GetLocalListVersionRequest{}
	resp, err := b.OnGetLocalListVersion(req)
	require.NoError(t, err)
	assert.Equal(t, 0, resp.VersionNumber)
}

func TestOnSendLocalList_Full(t *testing.T) {
	b := newTestBridge(t)

	expiry := ocpp201types.NewDateTime(time.Now().Add(time.Hour))
	req := localauth.NewSendLocalListRequest(1, localauth.UpdateTypeFull)
	req.LocalAuthorizationList = []localauth.AuthorizationData{
		{
			IdToken: ocpp201types.IdToken{IdToken: "RFID001", Type: ocpp201types.IdTokenTypeISO14443},
			IdTokenInfo: &ocpp201types.IdTokenInfo{
				Status:              ocpp201types.AuthorizationStatusAccepted,
				CacheExpiryDateTime: expiry,
			},
		},
	}

	resp, err := b.OnSendLocalList(req)
	require.NoError(t, err)
	assert.Equal(t, localauth.SendLocalListStatusAccepted, resp.Status)

	vResp, _ := b.OnGetLocalListVersion(&localauth.GetLocalListVersionRequest{})
	assert.Equal(t, 1, vResp.VersionNumber)
}

func TestOnSendLocalList_WithGroupIdToken(t *testing.T) {
	b := newTestBridge(t)

	req := localauth.NewSendLocalListRequest(1, localauth.UpdateTypeFull)
	req.LocalAuthorizationList = []localauth.AuthorizationData{
		{
			IdToken: ocpp201types.IdToken{IdToken: "RFID002", Type: ocpp201types.IdTokenTypeISO14443},
			IdTokenInfo: &ocpp201types.IdTokenInfo{
				Status:       ocpp201types.AuthorizationStatusAccepted,
				GroupIdToken: &ocpp201types.GroupIdToken{IdToken: "GROUP1", Type: ocpp201types.IdTokenTypeCentral},
			},
		},
	}

	resp, err := b.OnSendLocalList(req)
	require.NoError(t, err)
	assert.Equal(t, localauth.SendLocalListStatusAccepted, resp.Status)

	entry := b.localAuth.GetEntry("RFID002")
	require.NotNil(t, entry)
	require.NotNil(t, entry.ParentIDTag)
	assert.Equal(t, "GROUP1", *entry.ParentIDTag)
}

// --- DataTransfer ---

func TestOnDataTransfer_KnownVendor(t *testing.T) {
	b := newTestBridge(t)
	b.dataTransfer.Register("acme", "ping", func(messageID, data string) (string, string) {
		return "Accepted", "pong"
	})

	req := &data201.DataTransferRequest{VendorID: "acme", MessageID: "ping", Data: "hello"}
	resp, err := b.OnDataTransfer(req)
	require.NoError(t, err)
	assert.Equal(t, data201.DataTransferStatusAccepted, resp.Status)
	assert.Equal(t, "pong", resp.Data)
}

func TestOnDataTransfer_UnknownVendor(t *testing.T) {
	b := newTestBridge(t)

	req := &data201.DataTransferRequest{VendorID: "unknown", MessageID: "foo"}
	resp, err := b.OnDataTransfer(req)
	require.NoError(t, err)
	assert.Equal(t, data201.DataTransferStatusUnknownVendorId, resp.Status)
}

// --- TriggerMessage ---

func TestOnTriggerMessage_LogStatus_NotImplemented(t *testing.T) {
	b := newTestBridge(t)
	req := remotecontrol.NewTriggerMessageRequest(remotecontrol.MessageTriggerLogStatusNotification)
	resp, err := b.OnTriggerMessage(req)
	require.NoError(t, err)
	assert.Equal(t, remotecontrol.TriggerMessageStatusNotImplemented, resp.Status)
}

func TestOnTriggerMessage_TransactionEvent_NotImplemented(t *testing.T) {
	b := newTestBridge(t)
	req := remotecontrol.NewTriggerMessageRequest(remotecontrol.MessageTriggerTransactionEvent)
	resp, err := b.OnTriggerMessage(req)
	require.NoError(t, err)
	assert.Equal(t, remotecontrol.TriggerMessageStatusNotImplemented, resp.Status)
}
