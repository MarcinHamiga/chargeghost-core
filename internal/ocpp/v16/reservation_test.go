package v16

import (
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp1.6/reservation"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	engine "github.com/chargeghost/engine/internal/engine"
)

// newReservationTestBridge creates a minimal Bridge16 with only the engine field
// set, sufficient for testing the reservation handler methods.
func newReservationTestBridge(e *engine.Engine) *Bridge16 {
	return &Bridge16{engine: e}
}

func TestOnReserveNow_Accepted(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	b := newReservationTestBridge(e)

	expiry := types.NewDateTime(time.Now().Add(30 * time.Minute))
	req := reservation.NewReserveNowRequest(1, expiry, "TAG001", 42)

	conf, err := b.OnReserveNow(req)
	require.NoError(t, err)
	assert.Equal(t, reservation.ReservationStatusAccepted, conf.Status)
}

func TestOnReserveNow_Occupied_WhenPluggedIn(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	// Plug in an EV so the connector is occupied.
	e.PlugIn(1)

	b := newReservationTestBridge(e)
	expiry := types.NewDateTime(time.Now().Add(30 * time.Minute))
	req := reservation.NewReserveNowRequest(1, expiry, "TAG001", 42)

	conf, confErr := b.OnReserveNow(req)
	require.NoError(t, confErr)
	assert.Equal(t, reservation.ReservationStatusOccupied, conf.Status)
}

func TestOnReserveNow_Rejected_UnknownConnector(t *testing.T) {
	e := engine.NewEngine(false, 0)
	b := newReservationTestBridge(e)

	expiry := types.NewDateTime(time.Now().Add(30 * time.Minute))
	req := reservation.NewReserveNowRequest(99, expiry, "TAG001", 42)

	conf, err := b.OnReserveNow(req)
	require.NoError(t, err)
	assert.Equal(t, reservation.ReservationStatusRejected, conf.Status)
}

func TestOnCancelReservation_Accepted(t *testing.T) {
	e := engine.NewEngine(false, 0)
	e.AddConnector(230, 32, 1)
	b := newReservationTestBridge(e)

	// First create a reservation.
	expiry := types.NewDateTime(time.Now().Add(30 * time.Minute))
	reserveReq := reservation.NewReserveNowRequest(1, expiry, "TAG001", 42)
	reserveConf, err := b.OnReserveNow(reserveReq)
	require.NoError(t, err)
	require.Equal(t, reservation.ReservationStatusAccepted, reserveConf.Status)

	// Now cancel it.
	cancelReq := reservation.NewCancelReservationRequest(42)
	cancelConf, cancelErr := b.OnCancelReservation(cancelReq)
	require.NoError(t, cancelErr)
	assert.Equal(t, reservation.CancelReservationStatusAccepted, cancelConf.Status)
}

func TestOnCancelReservation_Rejected_NotFound(t *testing.T) {
	e := engine.NewEngine(false, 0)
	b := newReservationTestBridge(e)

	cancelReq := reservation.NewCancelReservationRequest(999)
	conf, err := b.OnCancelReservation(cancelReq)
	require.NoError(t, err)
	assert.Equal(t, reservation.CancelReservationStatusRejected, conf.Status)
}
