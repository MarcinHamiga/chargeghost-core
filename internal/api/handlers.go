package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/chargeghost/engine/internal/api/handlers"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
)

// Helper functions

// writeJSON serializes v to JSON and writes it with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// GetStatus delegates to the handlers package implementation.
func GetStatus(e *engine.Engine, startTime time.Time) http.HandlerFunc {
	return handlers.GetStatus(e, startTime)
}

// parseJSON decodes JSON from the request body into v.
func parseJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// connectorIDFromURL parses the {id} URL parameter as an integer.
// Returns (0, false) on parse failure.
func connectorIDFromURL(r *http.Request) (int, bool) {
	s := chi.URLParam(r, "id")
	id, err := strconv.Atoi(s)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// Connector Handlers

// ListConnectors handles GET /api/v1/connectors/
func ListConnectors(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		connectorIDs := e.GetConnectorIDs()
		connectors := make([]ConnectorDTO, 0, len(connectorIDs))
		for _, id := range connectorIDs {
			c := e.GetConnector(id)
			if c == nil {
				continue
			}
			connectors = append(connectors, ConnectorDTO{
				ID:          c.ID,
				Status:      string(c.Status),
				Voltage:     c.Voltage,
				Current:     c.Current,
				Phase:       c.Phase,
				IsPluggedIn: c.IsPluggedIn,
				IDTag:       c.IDTag,
			})
		}
		writeJSON(w, http.StatusOK, connectors)
	}
}

// CreateConnector handles POST /api/v1/connectors/
func CreateConnector(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateConnectorRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid request body",
			})
			return
		}

		c := e.AddConnector(req.Voltage, req.Current, req.Phase)
		writeJSON(w, http.StatusCreated, Response{
			Success: true,
			Message: "connector created",
			Details: c,
		})
	}
}

// GetConnector handles GET /api/v1/connectors/{id}
func GetConnector(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		c := e.GetConnector(id)
		if c == nil {
			writeJSON(w, http.StatusNotFound, Response{
				Success: false,
				Message: "connector not found",
			})
			return
		}
		writeJSON(w, http.StatusOK, ConnectorDTO{
			ID:          c.ID,
			Status:      string(c.Status),
			Voltage:     c.Voltage,
			Current:     c.Current,
			Phase:       c.Phase,
			IsPluggedIn: c.IsPluggedIn,
			IDTag:       c.IDTag,
		})
	}
}

// UpdateConnector handles PUT /api/v1/connectors/{id}
func UpdateConnector(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		var req UpdateConnectorRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid request body",
			})
			return
		}
		err := e.UpdateConnector(id, req.Voltage, req.Current, req.Phase)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "connector updated",
		})
	}
}

// DeleteConnector handles DELETE /api/v1/connectors/{id}
func DeleteConnector(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		err := e.RemoveConnector(id)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "connector deleted",
		})
	}
}

// PlugIn handles POST /api/v1/connectors/{id}/plug_in
func PlugIn(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		e.PlugIn(id)
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "connector plugged in",
		})
	}
}

// Unplug handles POST /api/v1/connectors/{id}/unplug
func Unplug(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		e.Unplug(id)
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "connector unplugged",
		})
	}
}

// SuspendEV handles POST /api/v1/connectors/{id}/suspend_ev
func SuspendEV(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		err := e.SuspendEV(id)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "EV suspended",
		})
	}
}

// ResumeCharging handles POST /api/v1/connectors/{id}/resume_charging
func ResumeCharging(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		err := e.ResumeCharging(id)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "charging resumed",
		})
	}
}

// StartCharging handles POST /api/v1/connectors/{id}/start-charging
func StartCharging(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		// For now, start a session with default parameters
		err := e.StartSession(id, 1, 0, nil, 0)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "charging started",
		})
	}
}

// StopCharging handles POST /api/v1/connectors/{id}/stop-charging
func StopCharging(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		e.StopSession(&id, "user_requested")
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "charging stopped",
		})
	}
}

// SetRFID handles PUT /api/v1/connectors/{id}/rfid
func SetRFID(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		var req struct {
			IDTag string `json:"id_tag"`
		}
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid request body",
			})
			return
		}
		c := e.GetConnector(id)
		if c == nil {
			writeJSON(w, http.StatusNotFound, Response{
				Success: false,
				Message: "connector not found",
			})
			return
		}
		c.IDTag = &req.IDTag
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "RFID tag set",
		})
	}
}

// ClearRFID handles DELETE /api/v1/connectors/{id}/rfid
func ClearRFID(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		c := e.GetConnector(id)
		if c == nil {
			writeJSON(w, http.StatusNotFound, Response{
				Success: false,
				Message: "connector not found",
			})
			return
		}
		c.IDTag = nil
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "RFID tag cleared",
		})
	}
}

// Session Handlers

// ListSessions handles GET /api/v1/sessions/
func ListSessions(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions := e.GetSessionInfo()
		sessionDTOs := make([]SessionDTO, 0, len(sessions))
		for _, s := range sessions {
			sessionDTOs = append(sessionDTOs, SessionDTO{
				TransactionID: s.TransactionID,
				ConnectorID:   s.ConnectorID,
				EnergyWh:      s.EnergyCharged,
				StateOfCharge: s.StateOfCharge,
				StartTime:     s.StartTime,
				IDTag:         s.IDTag,
				IsCharging:    s.IsCharging,
			})
		}
		writeJSON(w, http.StatusOK, sessionDTOs)
	}
}

// StartSession handles POST /api/v1/sessions/start
func StartSession(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req StartSessionRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid request body",
			})
			return
		}
		err := e.StartSession(req.ConnectorID, 1, req.MaxEnergy, req.IDTag, 0)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "session started",
		})
	}
}

// StopAllSessions handles POST /api/v1/sessions/stop
func StopAllSessions(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		e.StopSession(nil, "system")
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "all sessions stopped",
		})
	}
}

// GetLastStoppedSession handles GET /api/v1/sessions/last-stopped
func GetLastStoppedSession(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stopped := e.GetLastStoppedSession()
		if stopped == nil {
			writeJSON(w, http.StatusNotFound, Response{
				Success: false,
				Message: "no stopped sessions",
			})
			return
		}
		writeJSON(w, http.StatusOK, StoppedSessionDTO{
			TransactionID: stopped.TransactionID,
			ConnectorID:   stopped.ConnectorID,
			EnergyWh:      stopped.EnergyCharged,
			MeterStop:     stopped.MeterStop,
			Reason:        stopped.Reason,
			IDTag:         stopped.IDTag,
		})
	}
}

// GetActiveSession handles GET /api/v1/sessions/active
func GetActiveSession(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions := e.GetSessionInfo()
		if len(sessions) == 0 {
			writeJSON(w, http.StatusNotFound, Response{
				Success: false,
				Message: "no active sessions",
			})
			return
		}
		s := sessions[0]
		writeJSON(w, http.StatusOK, SessionDTO{
			TransactionID: s.TransactionID,
			ConnectorID:   s.ConnectorID,
			EnergyWh:      s.EnergyCharged,
			StateOfCharge: s.StateOfCharge,
			StartTime:     s.StartTime,
			IDTag:         s.IDTag,
			IsCharging:    s.IsCharging,
		})
	}
}

// GetSessionInfo handles GET /api/v1/sessions/info
func GetSessionInfo(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessions := e.GetSessionInfo()
		sessionDTOs := make([]SessionDTO, 0, len(sessions))
		for _, s := range sessions {
			sessionDTOs = append(sessionDTOs, SessionDTO{
				TransactionID: s.TransactionID,
				ConnectorID:   s.ConnectorID,
				EnergyWh:      s.EnergyCharged,
				StateOfCharge: s.StateOfCharge,
				StartTime:     s.StartTime,
				IDTag:         s.IDTag,
				IsCharging:    s.IsCharging,
			})
		}
		writeJSON(w, http.StatusOK, sessionDTOs)
	}
}

// GetSessionByConnector handles GET /api/v1/sessions/{connector_id}
func GetSessionByConnector(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "connector_id"))
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid connector_id"})
			return
		}
		s := e.GetSession(id)
		if s == nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "no active session"})
			return
		}
		m := e.GetEnergyMeter(id)
		writeJSON(w, http.StatusOK, SessionDTO{
			TransactionID: s.TransactionID,
			ConnectorID:   s.ConnectorID,
			EnergyWh:      s.EnergyCharged,
			StateOfCharge: s.StateOfCharge,
			StartTime:     s.StartTime,
			IDTag:         s.IDTag,
			IsCharging:    m != nil && m.IsCharging,
		})
	}
}

// Config Handlers

// GetConfig handles GET /api/v1/config/
func GetConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, cfg)
	}
}

// PatchConfig handles PATCH /api/v1/config/
func PatchConfig(cfg *config.Config, e *engine.Engine) http.HandlerFunc {
	// bridgeFields are config keys that require an OCPP bridge restart.
	bridgeFields := map[string]bool{
		"connection_url": true, "ocpp_id": true, "ocpp_password": true,
		"skip_tls_verify": true, "charge_point_model": true,
		"charge_point_vendor": true, "ocpp_version": true,
	}
	// topologyFields are config keys that require a full runtime rebuild.
	topologyFields := map[string]bool{
		"multi_evse_mode": true, "connectors": true, "ev_battery_capacity": true,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		sessions := e.GetSessionInfo()
		var req PatchConfigRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}

		changed := []string{}
		action := "no-op"

		applyChange := func(field string, apply func()) {
			apply()
			changed = append(changed, field)
			if topologyFields[field] {
				if len(sessions) > 0 {
					action = "rejected"
				} else if action != "rejected" {
					action = "runtime_rebuild_required"
				}
			} else if bridgeFields[field] && action != "runtime_rebuild_required" && action != "rejected" {
				action = "bridge_restart_required"
			}
		}

		if req.ConnectionURL != nil {
			applyChange("connection_url", func() { cfg.ConnectionURL = *req.ConnectionURL })
		}
		if req.OCPPID != nil {
			applyChange("ocpp_id", func() { cfg.OCPPID = *req.OCPPID })
		}
		if req.ChargePointModel != nil {
			applyChange("charge_point_model", func() { cfg.ChargePointModel = *req.ChargePointModel })
		}
		if req.ChargePointVendor != nil {
			applyChange("charge_point_vendor", func() { cfg.ChargePointVendor = *req.ChargePointVendor })
		}
		if req.SkipTLSVerify != nil {
			applyChange("skip_tls_verify", func() { cfg.SkipTLSVerify = *req.SkipTLSVerify })
		}
		if req.LogMode != nil {
			applyChange("log_mode", func() { cfg.LogMode = *req.LogMode })
		}
		if req.MultiEVSEMode != nil {
			applyChange("multi_evse_mode", func() { cfg.MultiEVSEMode = *req.MultiEVSEMode })
		}
		if req.EVBatteryCapacity != nil {
			applyChange("ev_battery_capacity", func() { cfg.EVBatteryCapacity = *req.EVBatteryCapacity })
		}
		if req.OCPPVersion != nil {
			applyChange("ocpp_version", func() { cfg.OCPPVersion = *req.OCPPVersion })
		}
		if req.PersistMessageQueue != nil {
			applyChange("persist_message_queue", func() { cfg.PersistMessageQueue = *req.PersistMessageQueue })
		}
		if req.RFIDTag != nil {
			applyChange("rfid_tag", func() { cfg.RFIDTag = req.RFIDTag })
		}

		if action == "rejected" {
			writeJSON(w, http.StatusConflict, PatchConfigResponse{
				Success:       false,
				Action:        "rejected",
				ChangedFields: changed,
				Message:       "Topology changes rejected: active sessions in progress",
			})
			return
		}

		msg := "Configuration updated in memory."
		if action == "bridge_restart_required" {
			msg += " Bridge restart required."
		} else if action == "runtime_rebuild_required" {
			msg += " Save required to rebuild runtime."
		}

		writeJSON(w, http.StatusOK, PatchConfigResponse{
			Success:       true,
			Action:        action,
			ChangedFields: changed,
			Message:       msg,
		})
	}
}

// SaveConfig handles POST /api/v1/config/save
func SaveConfig(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "config saved",
		})
	}
}

// Reservation Handlers

// ListReservations handles GET /api/v1/reservations/
func ListReservations(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ids := e.GetConnectorIDs()
		result := make([]ReservationDTO, 0)
		for _, cid := range ids {
			if res := e.GetReservation(cid); res != nil {
				result = append(result, ReservationDTO{
					ReservationID: res.ReservationID,
					ConnectorID:   res.ConnectorID,
					IDTag:         res.IDTag,
					ExpiryDate:    res.ExpiryDate.UTC().Format(time.RFC3339),
					ParentIDTag:   res.ParentIDTag,
				})
			}
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// CreateReservation handles POST /api/v1/reservations/
func CreateReservation(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateReservationRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}
		expiry, err := time.Parse(time.RFC3339, req.ExpiryDate)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "expiry_date must be RFC3339"})
			return
		}
		result := e.ReserveConnector(req.ConnectorID, req.ReservationID, req.IDTag, expiry, req.ParentIDTag)
		if result != "accepted" {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: result})
			return
		}
		writeJSON(w, http.StatusCreated, Response{Success: true, Message: "Reservation created"})
	}
}

// CancelReservation handles DELETE /api/v1/reservations/{reservation_id}
func CancelReservation(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "reservation_id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid reservation_id"})
			return
		}
		result := e.CancelReservation(id)
		if result != "accepted" {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "reservation not found"})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "Reservation cancelled"})
	}
}
