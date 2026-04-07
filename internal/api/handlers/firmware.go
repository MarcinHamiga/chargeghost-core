package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/chargeghost/engine/internal/ocpp"
)

func GetFirmwareStatus(m ocpp.FirmwareManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.GetStatus())
	}
}

func TriggerFirmwareUpdate(m ocpp.FirmwareManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Location     string `json:"location"`
			RetrieveDate string `json:"retrieve_date"` // RFC3339
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}
		retrieveDate, err := time.Parse(time.RFC3339, req.RetrieveDate)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "retrieve_date must be RFC3339"})
			return
		}
		if err := m.TriggerUpdate(req.Location, retrieveDate); err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "Firmware update started"})
	}
}

func CancelFirmwareUpdate(m ocpp.FirmwareManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := m.CancelUpdate(); err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: "no firmware update in progress"})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "Firmware update cancelled"})
	}
}

func GetDiagnosticsStatus(m ocpp.DiagnosticsManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.GetStatus())
	}
}

func TriggerDiagnosticsUpload(m ocpp.DiagnosticsManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Location      string `json:"location"`
			Retries       int    `json:"retries"`
			RetryInterval int    `json:"retry_interval"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}
		if err := m.TriggerUpload(req.Location, req.Retries, req.RetryInterval); err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "Diagnostics upload started"})
	}
}

func CancelDiagnosticsUpload(m ocpp.DiagnosticsManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := m.CancelUpload(); err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: "no diagnostics upload in progress"})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "Diagnostics upload cancelled"})
	}
}
