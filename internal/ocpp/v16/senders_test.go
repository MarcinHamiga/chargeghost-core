package v16

import (
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp"
	ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engine "github.com/chargeghost/engine/internal/engine"
	ocpppkg "github.com/chargeghost/engine/internal/ocpp"
	"github.com/chargeghost/engine/internal/ocpp/queue"
)

type stubChargePoint struct {
	ocpp16.ChargePoint
	sendRequest func(request ocpp.Request) (ocpp.Response, error)
}

func (s *stubChargePoint) SendRequest(request ocpp.Request) (ocpp.Response, error) {
	return s.sendRequest(request)
}

func TestSendMeterValues_UsesSuppliedContext(t *testing.T) {
	b := &Bridge16{}
	b.connected.Store(true)

	var captured *core.MeterValuesRequest
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.MeterValuesRequest)
		require.True(t, ok)
		captured = req
		return core.NewMeterValuesConfirmation(), nil
	}}

	require.NoError(t, b.SendMeterValues(1, 123.45, 77, "Sample.Clock"))
	require.NotNil(t, captured)
	require.Len(t, captured.MeterValue, 1)
	require.Len(t, captured.MeterValue[0].SampledValue, 1)
	assert.Equal(t, types.ReadingContextSampleClock, captured.MeterValue[0].SampledValue[0].Context)
	assert.Equal(t, 77, *captured.TransactionId)
}

func TestSendMeterValues_NormalizesInvalidContextToOther(t *testing.T) {
	b := &Bridge16{}
	b.connected.Store(true)

	var captured *core.MeterValuesRequest
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.MeterValuesRequest)
		require.True(t, ok)
		captured = req
		return core.NewMeterValuesConfirmation(), nil
	}}

	require.NoError(t, b.SendMeterValues(1, 123.45, 0, "not-valid"))
	require.NotNil(t, captured)
	assert.Equal(t, types.ReadingContextOther, captured.MeterValue[0].SampledValue[0].Context)
	assert.Nil(t, captured.TransactionId)
}

func TestDrainQueue_PreservesQueuedMeterContext(t *testing.T) {
	q := queue.NewInMemoryQueue(3)
	_, err := q.Enqueue(queue.QueuedMessage{
		Type: "MeterValues",
		Payload: map[string]interface{}{
			"connectorID":   1,
			"value":         50.0,
			"transactionID": 12,
			"context":       "Trigger",
		},
	})
	require.NoError(t, err)

	b := &Bridge16{queue: q, engine: engine.NewEngine(false, 55000)}
	b.connected.Store(true)

	var captured *core.MeterValuesRequest
	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.MeterValuesRequest)
		require.True(t, ok)
		captured = req
		return core.NewMeterValuesConfirmation(), nil
	}}

	b.drainQueue()

	require.NotNil(t, captured)
	assert.Equal(t, types.ReadingContextTrigger, captured.MeterValue[0].SampledValue[0].Context)
	assert.Equal(t, 0, q.Len())
}

func TestSendAuthorize_CachesAcceptedResponse(t *testing.T) {
	expiresAt := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)
	b := &Bridge16{authCache: ocpppkg.NewAuthorizationCache()}
	b.connected.Store(true)

	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		req, ok := request.(*core.AuthorizeRequest)
		require.True(t, ok)
		assert.Equal(t, "TAG-1", req.IdTag)

		idTagInfo := types.NewIdTagInfo(types.AuthorizationStatus("accepted"))
		idTagInfo.ExpiryDate = types.NewDateTime(expiresAt)
		return core.NewAuthorizationConfirmation(idTagInfo), nil
	}}

	require.NoError(t, b.SendAuthorize("TAG-1"))

	status, expiry, found := b.authCache.Get("TAG-1")
	require.True(t, found)
	assert.Equal(t, "Accepted", status)
	require.NotNil(t, expiry)
	assert.True(t, expiresAt.Equal(*expiry))
}

func TestSendAuthorize_CachesBlockedResponseAndReturnsError(t *testing.T) {
	b := &Bridge16{authCache: ocpppkg.NewAuthorizationCache()}
	b.connected.Store(true)

	b.cp = &stubChargePoint{sendRequest: func(request ocpp.Request) (ocpp.Response, error) {
		_, ok := request.(*core.AuthorizeRequest)
		require.True(t, ok)
		return core.NewAuthorizationConfirmation(types.NewIdTagInfo(types.AuthorizationStatusBlocked)), nil
	}}

	err := b.SendAuthorize("TAG-BLOCKED")
	require.EqualError(t, err, "authorize rejected: status=Blocked")

	status, expiry, found := b.authCache.Get("TAG-BLOCKED")
	require.True(t, found)
	assert.Equal(t, "Blocked", status)
	assert.Nil(t, expiry)
}
