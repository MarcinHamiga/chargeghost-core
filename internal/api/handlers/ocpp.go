package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
)

// OCPPSendAPI defines the outbound OCPP operations exposed via REST.
// Transaction messages go through the durable enqueue path (the same one
// engine callbacks use) so raw sends survive disconnects and preserve
// FIFO order instead of blocking on a synchronous send.
type OCPPSendAPI interface {
	SendAuthorize(idTag string) error
	SendHeartbeat() error
	SendBootNotification() error
	SendStatusNotification(connectorID int, errorCode, status string) error
	SendMeterValues(connectorID int, value float64, transactionID int, context string) error
	SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error)
	SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error
	EnqueueTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error)
	EnqueueTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, idTag *string, meterHistory []engine.MeterRecord) error
	SendDataTransfer(vendorID, messageID, data string) (string, string, error)
	IsConnected() bool
}

type PatchOCPPConfigKeyRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type AuthorizeRequest struct {
	IDTag string `json:"id_tag"`
}

type RawStatusNotificationRequest struct {
	ConnectorID int    `json:"connector_id"`
	ErrorCode   string `json:"error_code"`
	Status      string `json:"status"`
}

type RawMeterValuesRequest struct {
	ConnectorID   int `json:"connector_id"`
	TransactionID int `json:"transaction_id"`
}

type RawDataTransferRequest struct {
	VendorID  string `json:"vendor_id"`
	MessageID string `json:"message_id"`
	Data      string `json:"data"`
}

type RawStartTransactionRequest struct {
	ConnectorID   int    `json:"connector_id"`
	IDTag         string `json:"id_tag"`
	ReservationID *int   `json:"reservation_id"`
}

type RawStopTransactionRequest struct {
	TransactionID int    `json:"transaction_id"`
	Reason        string `json:"reason"`
}

type DataTransferResponse struct {
	Status string `json:"status"`
	Data   string `json:"data"`
}

func GetOCPPConfigKeys(m ocpp.ConfigKeyAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.GetConfigKeyInfo())
	}
}

func PatchOCPPConfigKey(m ocpp.ConfigKeyAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req PatchOCPPConfigKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}
		result := m.SetConfigValue(req.Key, req.Value)
		switch result {
		case "Accepted":
			writeJSON(w, http.StatusOK, Response{Success: true, Message: "Key updated"})
		case "Rejected":
			writeJSON(w, http.StatusForbidden, Response{Success: false, Message: "key is read-only"})
		default:
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "key not supported"})
		}
	}
}

func SendAuthorize(ocppAPI OCPPSendAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req AuthorizeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid body"})
			return
		}
		if err := ocppAPI.SendAuthorize(req.IDTag); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "Authorize sent"})
	}
}

func SendHeartbeat(ocppAPI OCPPSendAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := ocppAPI.SendHeartbeat(); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "Heartbeat sent"})
	}
}

func SendRawStatusNotification(e *engine.Engine, ocppAPI OCPPSendAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RawStatusNotificationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid body"})
			return
		}
		if err := ocppAPI.SendStatusNotification(req.ConnectorID, req.ErrorCode, req.Status); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "StatusNotification sent"})
	}
}

func SendRawMeterValues(e *engine.Engine, ocppAPI OCPPSendAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RawMeterValuesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid body"})
			return
		}
		reading, txID := e.GetMeterSnapshot(req.ConnectorID)
		if req.TransactionID != 0 {
			txID = req.TransactionID
		}
		if err := ocppAPI.SendMeterValues(req.ConnectorID, reading, txID, "Sample.Clock"); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "MeterValues sent"})
	}
}

func SendRawDataTransfer(ocppAPI OCPPSendAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RawDataTransferRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid body"})
			return
		}
		status, data, err := ocppAPI.SendDataTransfer(req.VendorID, req.MessageID, req.Data)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, DataTransferResponse{Status: status, Data: data})
	}
}

func SendRawStartTransaction(e *engine.Engine, ocppAPI OCPPSendAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RawStartTransactionRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid body"})
			return
		}
		if req.ConnectorID <= 0 {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "connector_id must be positive"})
			return
		}
		if req.IDTag == "" {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "id_tag is required"})
			return
		}
		if e.GetConnector(req.ConnectorID) == nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "connector not found"})
			return
		}

		// The charge point stamps its own meter and clock: caller-supplied
		// meter_start/timestamp fields are not accepted (unknown JSON fields
		// are ignored), per OCPP metrology expectations.
		meterStart, _ := e.GetMeterSnapshot(req.ConnectorID)
		timestamp := time.Now()

		transactionID, err := ocppAPI.EnqueueTransactionStart(req.ConnectorID, req.IDTag, meterStart, timestamp, req.ReservationID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: err.Error()})
			return
		}

		resp := Response{Success: true, Message: "StartTransaction enqueued"}
		if transactionID != 0 {
			resp.Details = map[string]int{"transaction_id": transactionID}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func SendRawStopTransaction(e *engine.Engine, ocppAPI OCPPSendAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RawStopTransactionRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid body"})
			return
		}
		if req.TransactionID <= 0 {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "transaction_id must be positive"})
			return
		}
		if req.Reason == "" {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "reason is required"})
			return
		}

		connectorID, session := e.GetSessionByTransaction(req.TransactionID)
		if session == nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: "transaction not found"})
			return
		}

		// The charge point stamps its own meter and clock: caller-supplied
		// meter_stop/timestamp fields are not accepted (unknown JSON fields
		// are ignored), per OCPP metrology expectations.
		meterStop, _ := e.GetMeterSnapshot(connectorID)
		timestamp := time.Now()
		if err := ocppAPI.EnqueueTransactionStop(meterStop, timestamp, req.TransactionID, req.Reason, session.IDTag, session.MeterHistory); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "StopTransaction enqueued"})
	}
}
