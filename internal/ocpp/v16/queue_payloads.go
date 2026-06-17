package v16

import (
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
)

type queuedStartTransaction16 struct {
	ConnectorID   int       `json:"connector_id"`
	IDTag         string    `json:"id_tag"`
	MeterStart    float64   `json:"meter_start"`
	Timestamp     time.Time `json:"timestamp"`
	ReservationID *int      `json:"reservation_id,omitempty"`
}

type queuedStopTransaction16 struct {
	TransactionID int                  `json:"transaction_id"`
	MeterStop     float64              `json:"meter_stop"`
	Timestamp     time.Time            `json:"timestamp"`
	Reason        string               `json:"reason"`
	IDTag         *string              `json:"id_tag,omitempty"`
	MeterHistory  []engine.MeterRecord `json:"meter_history,omitempty"`
}

type queuedMeterValues16 struct {
	ConnectorID   int       `json:"connector_id"`
	Value         float64   `json:"value"`
	TransactionID int       `json:"transaction_id"`
	Context       string    `json:"context"`
	Timestamp     time.Time `json:"timestamp"`
}
