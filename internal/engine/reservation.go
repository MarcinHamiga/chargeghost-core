package engine

import "time"

// Reservation represents a time-bounded slot on a connector.
type Reservation struct {
	ReservationID int
	ConnectorID   int
	IDTag         string
	ExpiryDate    time.Time
	ParentIDTag   *string
}

// IsExpired returns true when the reservation has passed its expiry time.
func (r *Reservation) IsExpired(now time.Time) bool {
	return !now.Before(r.ExpiryDate)
}
