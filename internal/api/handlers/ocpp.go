package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
)

// OCPPSendAPI defines the outbound OCPP operations exposed via REST.
type OCPPSendAPI interface {
	SendAuthorize(idTag string) error
	SendHeartbeat() error
	SendBootNotification() error
	SendStatusNotification(connectorID int, errorCode, status string) error
	SendMeterValues(connectorID int, value float64, transactionID int, context string) error
	SendTransactionStart(connectorID int, idTag string, meterStart float64, timestamp time.Time, reservationID *int) (int, error)
	SendTransactionStop(meterStop float64, timestamp time.Time, transactionID int, reason string, meterHistory []engine.MeterRecord) error
	SendDataTransfer(vendorID, messageID, data string) (string, string, error)
	IsConnected() bool
}

func GetOCPPConfigKeys(m ocpp.ConfigKeyAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.GetConfigKeyInfo())
	}
}

func PatchOCPPConfigKey(m ocpp.ConfigKeyAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
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
		var req struct {
			IDTag string `json:"id_tag"`
		}
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
		var req struct {
			ConnectorID int    `json:"connector_id"`
			ErrorCode   string `json:"error_code"`
			Status      string `json:"status"`
		}
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
		var req struct {
			ConnectorID   int `json:"connector_id"`
			TransactionID int `json:"transaction_id"`
		}
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
		var req struct {
			VendorID  string `json:"vendor_id"`
			MessageID string `json:"message_id"`
			Data      string `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid body"})
			return
		}
		status, data, err := ocppAPI.SendDataTransfer(req.VendorID, req.MessageID, req.Data)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": status, "data": data})
	}
}

func SendRawStartTransaction(e *engine.Engine, ocppAPI OCPPSendAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ConnectorID   int        `json:"connector_id"`
			IDTag         string     `json:"id_tag"`
			MeterStart    *float64   `json:"meter_start"`
			Timestamp     *time.Time `json:"timestamp"`
			ReservationID *int       `json:"reservation_id"`
		}
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

		meterStart, _ := e.GetMeterSnapshot(req.ConnectorID)
		if req.MeterStart != nil {
			meterStart = *req.MeterStart
		}

		timestamp := time.Now()
		if req.Timestamp != nil {
			timestamp = *req.Timestamp
		}

		transactionID, err := ocppAPI.SendTransactionStart(req.ConnectorID, req.IDTag, meterStart, timestamp, req.ReservationID)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: err.Error()})
			return
		}

		resp := Response{Success: true, Message: "StartTransaction sent"}
		if transactionID != 0 {
			resp.Details = map[string]int{"transaction_id": transactionID}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func SendRawStopTransaction(e *engine.Engine, ocppAPI OCPPSendAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TransactionID int        `json:"transaction_id"`
			MeterStop     *float64   `json:"meter_stop"`
			Timestamp     *time.Time `json:"timestamp"`
			Reason        string     `json:"reason"`
		}
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

		meterStop, _ := e.GetMeterSnapshot(connectorID)
		if req.MeterStop != nil {
			meterStop = *req.MeterStop
		}

		timestamp := time.Now()
		if req.Timestamp != nil {
			timestamp = *req.Timestamp
		}
		if err := ocppAPI.SendTransactionStop(meterStop, timestamp, req.TransactionID, req.Reason, session.MeterHistory); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "StopTransaction sent"})
	}
}
