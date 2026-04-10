package v201

import (
	"testing"
	"time"

	data201 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/data"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/diagnostics"
	firmware201 "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/firmware"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/localauth"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/provisioning"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/remotecontrol"
	"github.com/lorenzodonini/ocpp-go/ocpp2.0.1/smartcharging"
	ocpp201types "github.com/lorenzodonini/ocpp-go/ocpp2.0.1/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

type diagnosticsManagerStub struct {
	status       ocpppkg.DiagnosticsStatus
	triggerCalls int
	lastLocation string
	lastRetries  int
	lastInterval int
	triggerErr   error
}

func (m *diagnosticsManagerStub) GetStatus() ocpppkg.DiagnosticsStatus {
	return m.status
}

func (m *diagnosticsManagerStub) TriggerUpload(location string, retries, retryInterval int) error {
	m.triggerCalls++
	m.lastLocation = location
	m.lastRetries = retries
	m.lastInterval = retryInterval
	if m.triggerErr != nil {
		return m.triggerErr
	}
	m.status = ocpppkg.DiagnosticsStatus{Status: "Uploading", Location: &location}
	return nil
}

func (m *diagnosticsManagerStub) CancelUpload() error {
	m.status = ocpppkg.DiagnosticsStatus{Status: "Idle"}
	return nil
}

func newTestBridge(t *testing.T) *Bridge201 {
	t.Helper()
	e := engine.NewEngine(false, 55000)
	cfg := config.DefaultConfig()
	dispatcher := ocpppkg.NewCommandDispatcher()
	q, err := queue.NewQueue(false, "", 0)
	require.NoError(t, err)
	b := NewBridge(e, nil, cfg, dispatcher, q, nil)
	fw := ocpppkg.NewFirmwareManager(nil)
	diag := ocpppkg.NewDiagnosticsManager(nil)
	dt := ocpppkg.NewDataTransferRegistry()
	la := ocpppkg.NewLocalAuthListManager()
	auth := ocpppkg.NewAuthorizationCache()
	b.SetManagers(auth, la, fw, diag, dt)
	return b
}

func captureEnqueuedCommands(b *Bridge201) *[]string {
	commands := &[]string{}
	b.enqueueCommand = func(cmd ocpppkg.OCPPCommand) {
		*commands = append(*commands, cmd.Description)
	}
	return commands
}

func setupActiveResetSession(t *testing.T, b *Bridge201, txID int) {
	t.Helper()

	b.engine.AddConnector(230, 32, 1)
	b.engine.PlugIn(1)
	require.NoError(t, b.engine.StartSession(1, txID, 0, nil, 0))

	b.mu.Lock()
	b.txBuilders[1] = NewTransactionEventBuilder(1, 1)
	b.txIntToEVSE[txID] = 1
	b.mu.Unlock()

	b.engine.OnSessionStopped = func(connectorID int, info *engine.StoppedSessionInfo) {
		require.NotNil(t, info)
		require.NoError(t, b.SendTransactionStop(info.MeterStop, time.Now(), info.TransactionID, info.Reason, info.MeterHistory))
	}
}

// --- Reset ---

func TestOnReset_Immediate(t *testing.T) {
	b := newTestBridge(t)
	commands := captureEnqueuedCommands(b)

	b.mu.Lock()
	b.txBuilders[1] = NewTransactionEventBuilder(1, 1)
	b.txIntToEVSE[99] = 1
	b.mu.Unlock()

	req := provisioning.NewResetRequest(provisioning.ResetTypeImmediate)
	resp, err := b.OnReset(req)
	require.NoError(t, err)
	assert.Equal(t, provisioning.ResetStatusAccepted, resp.Status)
	assert.False(t, b.pendingReset.Load())
	assert.Empty(t, b.txBuilders)
	assert.Empty(t, b.txIntToEVSE)
	assert.Equal(t, []string{"BootNotification (post-reset)"}, *commands)
}

func TestOnReset_OnIdle_NoActiveTx(t *testing.T) {
	b := newTestBridge(t)
	commands := captureEnqueuedCommands(b)

	b.mu.Lock()
	b.txBuilders[1] = NewTransactionEventBuilder(1, 1)
	b.txIntToEVSE[77] = 1
	b.mu.Unlock()

	req := provisioning.NewResetRequest(provisioning.ResetTypeOnIdle)
	resp, err := b.OnReset(req)
	require.NoError(t, err)
	assert.Equal(t, provisioning.ResetStatusAccepted, resp.Status)
	assert.False(t, b.pendingReset.Load())
	assert.Empty(t, b.txBuilders)
	assert.Empty(t, b.txIntToEVSE)
	assert.Equal(t, []string{"BootNotification (post-reset)"}, *commands)
}

func TestOnReset_OnIdle_WithActiveTx(t *testing.T) {
	b := newTestBridge(t)
	commands := captureEnqueuedCommands(b)
	setupActiveResetSession(t, b, 123)

	req := provisioning.NewResetRequest(provisioning.ResetTypeOnIdle)
	resp, err := b.OnReset(req)
	require.NoError(t, err)
	assert.Equal(t, provisioning.ResetStatusScheduled, resp.Status)
	assert.True(t, b.pendingReset.Load())
	assert.NotNil(t, b.engine.GetSession(1))
	assert.Empty(t, *commands)
}

func TestOnReset_Immediate_StopsActiveSessionAndCompletesReset(t *testing.T) {
	b := newTestBridge(t)
	commands := captureEnqueuedCommands(b)
	setupActiveResetSession(t, b, 456)

	req := provisioning.NewResetRequest(provisioning.ResetTypeImmediate)
	resp, err := b.OnReset(req)
	require.NoError(t, err)
	assert.Equal(t, provisioning.ResetStatusAccepted, resp.Status)
	assert.False(t, b.pendingReset.Load())
	assert.Nil(t, b.engine.GetSession(1))
	assert.Equal(t, string(engine.StateFinishing), b.engine.GetConnectorStatus(1))

	stopped := b.engine.GetLastStoppedSession()
	require.NotNil(t, stopped)
	assert.Equal(t, "Reboot", stopped.Reason)

	assert.Empty(t, b.txBuilders)
	assert.Empty(t, b.txIntToEVSE)
	assert.Equal(t, []string{"BootNotification (post-reset)"}, *commands)
}

func TestOnReset_OnIdle_CompletesAfterLastTransactionEnds(t *testing.T) {
	b := newTestBridge(t)
	commands := captureEnqueuedCommands(b)
	setupActiveResetSession(t, b, 789)

	req := provisioning.NewResetRequest(provisioning.ResetTypeOnIdle)
	resp, err := b.OnReset(req)
	require.NoError(t, err)
	assert.Equal(t, provisioning.ResetStatusScheduled, resp.Status)
	assert.True(t, b.pendingReset.Load())
	assert.Empty(t, *commands)

	connectorID := 1
	info := b.engine.StopSession(&connectorID, "Remote")
	require.NotNil(t, info)

	assert.False(t, b.pendingReset.Load())
	assert.Nil(t, b.engine.GetSession(1))
	assert.Empty(t, b.txBuilders)
	assert.Empty(t, b.txIntToEVSE)
	assert.Equal(t, []string{"BootNotification (post-reset)"}, *commands)
}

func TestOnGetBaseReport_NotSupported(t *testing.T) {
	b := newTestBridge(t)

	req := provisioning.NewGetBaseReportRequest(7, provisioning.ReportTypeFullInventory)
	resp, err := b.OnGetBaseReport(req)
	require.NoError(t, err)
	assert.Equal(t, ocpp201types.GenericDeviceModelStatusNotSupported, resp.Status)
}

func TestOnGetReport_NotSupported(t *testing.T) {
	b := newTestBridge(t)

	req := provisioning.NewGetReportRequest()
	requestID := 11
	req.RequestID = &requestID
	resp, err := b.OnGetReport(req)
	require.NoError(t, err)
	assert.Equal(t, ocpp201types.GenericDeviceModelStatusNotSupported, resp.Status)
}

func TestOnGetCompositeSchedule_Rejected(t *testing.T) {
	b := newTestBridge(t)

	req := smartcharging.NewGetCompositeScheduleRequest(300, 1)
	resp, err := b.OnGetCompositeSchedule(req)
	require.NoError(t, err)
	assert.Equal(t, smartcharging.GetCompositeScheduleStatusRejected, resp.Status)
	assert.Nil(t, resp.Schedule)
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

func TestOnGetLog_AcceptedTriggersDiagnosticsManager(t *testing.T) {
	b := newTestBridge(t)
	diag := &diagnosticsManagerStub{}
	b.diagManager = diag

	retries := 2
	retryInterval := 30
	req := diagnostics.NewGetLogRequest(
		diagnostics.LogTypeDiagnostics,
		42,
		diagnostics.LogParameters{RemoteLocation: "https://example.com/diag"},
	)
	req.Retries = &retries
	req.RetryInterval = &retryInterval

	resp, err := b.OnGetLog(req)
	require.NoError(t, err)
	assert.Equal(t, diagnostics.LogStatusAccepted, resp.Status)
	assert.Equal(t, "diagnostics.tgz", resp.Filename)
	assert.Equal(t, 1, diag.triggerCalls)
	assert.Equal(t, "https://example.com/diag", diag.lastLocation)
	assert.Equal(t, 2, diag.lastRetries)
	assert.Equal(t, 30, diag.lastInterval)
	assert.EqualValues(t, 42, b.diagRequestID.Load())
}

func TestOnGetLog_UnsupportedLogTypeRejected(t *testing.T) {
	b := newTestBridge(t)
	diag := &diagnosticsManagerStub{}
	b.diagManager = diag

	req := diagnostics.NewGetLogRequest(
		diagnostics.LogTypeSecurity,
		7,
		diagnostics.LogParameters{RemoteLocation: "https://example.com/security"},
	)

	resp, err := b.OnGetLog(req)
	require.NoError(t, err)
	assert.Equal(t, diagnostics.LogStatusRejected, resp.Status)
	assert.Equal(t, 0, diag.triggerCalls)
}

func TestOnSetMonitoringBase_RejectedWhenUnsupported(t *testing.T) {
	b := newTestBridge(t)

	req := diagnostics.NewSetMonitoringBaseRequest(diagnostics.MonitoringBaseAll)
	resp, err := b.OnSetMonitoringBase(req)
	require.NoError(t, err)
	assert.Equal(t, ocpp201types.GenericDeviceModelStatusRejected, resp.Status)
}

func TestOnSetVariableMonitoring_RejectedWhenUnsupported(t *testing.T) {
	b := newTestBridge(t)

	req := diagnostics.NewSetVariableMonitoringRequest([]diagnostics.SetMonitoringData{{
		Value:     10,
		Type:      diagnostics.MonitorUpperThreshold,
		Severity:  5,
		Component: ocpp201types.Component{Name: "EVSE", EVSE: &ocpp201types.EVSE{ID: 1}},
		Variable:  ocpp201types.Variable{Name: "AvailabilityState"},
	}})
	resp, err := b.OnSetVariableMonitoring(req)
	require.NoError(t, err)
	require.Len(t, resp.MonitoringResult, 1)
	assert.Equal(t, diagnostics.SetMonitoringStatusRejected, resp.MonitoringResult[0].Status)
	assert.Nil(t, resp.MonitoringResult[0].ID)
	assert.Empty(t, b.monitoringManager.GetAllMonitors())
}

func TestOnGetMonitoringReport_NotSupported(t *testing.T) {
	b := newTestBridge(t)

	requestID := 23
	req := diagnostics.NewGetMonitoringReportRequest()
	req.RequestID = &requestID
	resp, err := b.OnGetMonitoringReport(req)
	require.NoError(t, err)
	assert.Equal(t, ocpp201types.GenericDeviceModelStatusNotSupported, resp.Status)
}

func TestOnSetMonitoringLevel_RejectedWhenUnsupported(t *testing.T) {
	b := newTestBridge(t)

	req := diagnostics.NewSetMonitoringLevelRequest(5)
	resp, err := b.OnSetMonitoringLevel(req)
	require.NoError(t, err)
	assert.Equal(t, ocpp201types.GenericDeviceModelStatusRejected, resp.Status)
}

func TestOnCustomerInformation_RejectedWhenUnsupported(t *testing.T) {
	b := newTestBridge(t)

	req := diagnostics.NewCustomerInformationRequest(17, true, false)
	resp, err := b.OnCustomerInformation(req)
	require.NoError(t, err)
	assert.Equal(t, diagnostics.CustomerInformationStatusRejected, resp.Status)
}

func TestOnPublishFirmware_RejectedWhenUnsupported(t *testing.T) {
	b := newTestBridge(t)

	req := firmware201.NewPublishFirmwareRequest("https://example.com/fw.bin", "0123456789abcdef0123456789abcdef", 9)
	resp, err := b.OnPublishFirmware(req)
	require.NoError(t, err)
	assert.Equal(t, ocpp201types.GenericStatusRejected, resp.Status)
}

func TestOnUnpublishFirmware_NoFirmwareWhenUnsupported(t *testing.T) {
	b := newTestBridge(t)

	req := firmware201.NewUnpublishFirmwareRequest("0123456789abcdef0123456789abcdef")
	resp, err := b.OnUnpublishFirmware(req)
	require.NoError(t, err)
	assert.Equal(t, firmware201.UnpublishFirmwareStatusNoFirmware, resp.Status)
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

func TestOnSendLocalList_DifferentialDelete(t *testing.T) {
	b := newTestBridge(t)

	fullReq := localauth.NewSendLocalListRequest(1, localauth.UpdateTypeFull)
	fullReq.LocalAuthorizationList = []localauth.AuthorizationData{
		{
			IdToken:     ocpp201types.IdToken{IdToken: "RFID001", Type: ocpp201types.IdTokenTypeISO14443},
			IdTokenInfo: &ocpp201types.IdTokenInfo{Status: ocpp201types.AuthorizationStatusAccepted},
		},
		{
			IdToken:     ocpp201types.IdToken{IdToken: "RFID002", Type: ocpp201types.IdTokenTypeISO14443},
			IdTokenInfo: &ocpp201types.IdTokenInfo{Status: ocpp201types.AuthorizationStatusBlocked},
		},
	}

	resp, err := b.OnSendLocalList(fullReq)
	require.NoError(t, err)
	assert.Equal(t, localauth.SendLocalListStatusAccepted, resp.Status)

	diffReq := localauth.NewSendLocalListRequest(2, localauth.UpdateTypeDifferential)
	diffReq.LocalAuthorizationList = []localauth.AuthorizationData{{
		IdToken: ocpp201types.IdToken{IdToken: "RFID001", Type: ocpp201types.IdTokenTypeISO14443},
	}}

	resp, err = b.OnSendLocalList(diffReq)
	require.NoError(t, err)
	assert.Equal(t, localauth.SendLocalListStatusAccepted, resp.Status)
	assert.Nil(t, b.localAuth.GetEntry("RFID001"))
	assert.NotNil(t, b.localAuth.GetEntry("RFID002"))
	assert.Equal(t, 2, b.localAuth.GetVersion())
}

func TestOnSendLocalList_DifferentialVersionMismatch(t *testing.T) {
	b := newTestBridge(t)

	require.NoError(t, b.localAuth.UpdateList(2, []ocpppkg.LocalAuthEntry{{IDTag: "RFID001", Status: "Accepted"}}, "Full"))

	req := localauth.NewSendLocalListRequest(2, localauth.UpdateTypeDifferential)
	req.LocalAuthorizationList = []localauth.AuthorizationData{{
		IdToken: ocpp201types.IdToken{IdToken: "RFID001", Type: ocpp201types.IdTokenTypeISO14443},
	}}

	resp, err := b.OnSendLocalList(req)
	require.NoError(t, err)
	assert.Equal(t, localauth.SendLocalListStatusVersionMismatch, resp.Status)
	assert.NotNil(t, b.localAuth.GetEntry("RFID001"))
	assert.Equal(t, 2, b.localAuth.GetVersion())
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
