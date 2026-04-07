package handlers

import (
	"encoding/json"
	"net/http"

	engine "github.com/chargeghost/engine/internal/engine"
	v16 "github.com/chargeghost/engine/internal/ocpp/v16"
)

// OCPPSendAPI defines the outbound OCPP operations exposed via REST.
type OCPPSendAPI interface {
	SendAuthorize(idTag string) error
	SendHeartbeat() error
	SendBootNotification() error
	SendStatusNotification(connectorID int, errorCode, status string) error
	SendMeterValues(connectorID int, value float64, transactionID int, context string) error
	SendDataTransfer(vendorID, messageID, data string) (string, string, error)
	IsConnected() bool
}

func GetOCPPConfigKeys(m *v16.ConfigKeyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.GetConfigKeyInfo())
	}
}

func PatchOCPPConfigKey(m *v16.ConfigKeyManager) http.HandlerFunc {
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
