package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/chargeghost/engine/internal/api/handlers"
	ws "github.com/chargeghost/engine/internal/api/ws"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/go-chi/chi/v5"
)

// Helper functions

// writeJSON serializes v to JSON and writes it with the given HTTP status.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// GetStatus delegates to the handlers package implementation.
func GetStatus(e *engine.Engine, startTime time.Time, ocppBridge handlers.OCPPSendAPI) http.HandlerFunc {
	return handlers.GetStatus(e, startTime, ocppBridge)
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

func sessionIDTag(defaultTag, requested *string) *string {
	if requested != nil && *requested != "" {
		return requested
	}
	if defaultTag != nil && *defaultTag != "" {
		tag := *defaultTag
		return &tag
	}
	return nil
}

func timeoutSeconds(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}

type localSessionAdmissionFunc func(idTag *string) error

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

		if req.Voltage < 120 || req.Voltage > 1000 {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "voltage out of range (120–1000V)"})
			return
		}
		if req.Current < 6 || req.Current > 150 {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "current out of range (6–150A)"})
			return
		}
		if req.Phase != 1 && req.Phase != 3 {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "phase must be 1 or 3"})
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
			writeJSON(w, http.StatusConflict, Response{
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
		if e.GetConnector(id) == nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "connector not found"})
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
		if e.GetConnector(id) == nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "connector not found"})
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
			writeJSON(w, http.StatusConflict, Response{
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
			writeJSON(w, http.StatusConflict, Response{
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
func StartCharging(e *engine.Engine, cfg *config.Config, admit localSessionAdmissionFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid connector id",
			})
			return
		}
		timeout, err := timeoutSeconds(r.URL.Query().Get("timeout_seconds"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "timeout_seconds must be a non-negative integer"})
			return
		}
		var defaultTag *string
		if cfg != nil {
			defaultTag = cfg.RFIDTag
		}
		idTag := sessionIDTag(defaultTag, nil)
		if admit != nil {
			if err := admit(idTag); err != nil {
				writeJSON(w, http.StatusForbidden, Response{Success: false, Message: err.Error()})
				return
			}
		}
		err = e.StartSession(id, -1, idTag, timeout)
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
		if e.GetConnector(id) == nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "connector not found"})
			return
		}
		if e.GetSession(id) == nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: "no active session"})
			return
		}
		e.StopSession(&id, "user_requested")
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "charging stopped",
		})
	}
}

// UpdateAvailability handles PUT /api/v1/connectors/{id}/availability
func UpdateAvailability(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid connector id"})
			return
		}
		var req UpdateAvailabilityRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}
		if e.GetConnector(id) == nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "connector not found"})
			return
		}
		result := e.SetConnectorAvailability(id, req.Type)
		switch result {
		case "accepted":
			writeJSON(w, http.StatusOK, Response{Success: true, Message: "availability updated"})
		case "scheduled":
			writeJSON(w, http.StatusAccepted, Response{Success: true, Message: "availability change scheduled after the active session ends"})
		default:
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "availability change rejected"})
		}
	}
}

// SetRFID handles PUT /api/v1/connectors/{id}/rfid
func SetRFID(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid connector id"})
			return
		}
		tag := r.URL.Query().Get("rfid_tag")
		if tag == "" {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "rfid_tag query param required"})
			return
		}
		if err := e.SetIDTag(id, tag); err != nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "connector not found"})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "RFID tag set"})
	}
}

// ClearRFID handles DELETE /api/v1/connectors/{id}/rfid
func ClearRFID(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := connectorIDFromURL(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid connector id"})
			return
		}
		if err := e.ClearIDTag(id); err != nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "connector not found"})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "RFID tag cleared"})
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
func StartSession(e *engine.Engine, cfg *config.Config, admit localSessionAdmissionFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req StartSessionRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid request body",
			})
			return
		}
		var defaultTag *string
		if cfg != nil {
			defaultTag = cfg.RFIDTag
		}
		idTag := sessionIDTag(defaultTag, req.IDTag)
		if admit != nil {
			if err := admit(idTag); err != nil {
				writeJSON(w, http.StatusForbidden, Response{Success: false, Message: err.Error()})
				return
			}
		}
		err := e.StartSession(req.ConnectorID, 1, idTag, req.TimeoutSeconds)
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
		stopped := e.StopAllSessions("Local")
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "all sessions stopped",
			Details: map[string]int{"stopped_count": len(stopped)},
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
		id, _ := strconv.Atoi(r.URL.Query().Get("connector_id"))
		if id == 0 {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "connector_id required"})
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
		writeJSON(w, http.StatusOK, cfg.Sanitized())
	}
}

// PatchConfig handles PATCH /api/v1/config/
func PatchConfig(cfg *config.Config, e *engine.Engine) http.HandlerFunc {
	restartFields := map[string]bool{
		"charge_point_model":    true,
		"charge_point_vendor":   true,
		"connection_url":        true,
		"log_mode":              true,
		"multi_evse_mode":       true,
		"ocpp_id":               true,
		"ocpp_password":         true,
		"ocpp_version":          true,
		"persist_message_queue": true,
		"security_profile":      true,
		"skip_tls_verify":       true,
		"tls_ca_path":           true,
		"tls_client_cert_path":  true,
		"tls_client_key_path":   true,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var req PatchConfigRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}

		next := *cfg
		changedSet := make(map[string]struct{})
		restartRequired := false

		applyChange := func(field string, apply func()) {
			apply()
			changedSet[field] = struct{}{}
			if restartFields[field] {
				restartRequired = true
			}
		}

		if req.ConnectionURL != nil {
			applyChange("connection_url", func() { next.ConnectionURL = *req.ConnectionURL })
		}
		if req.OCPPID != nil {
			applyChange("ocpp_id", func() { next.OCPPID = *req.OCPPID })
		}
		if req.ChargePointModel != nil {
			applyChange("charge_point_model", func() { next.ChargePointModel = *req.ChargePointModel })
		}
		if req.ChargePointVendor != nil {
			applyChange("charge_point_vendor", func() { next.ChargePointVendor = *req.ChargePointVendor })
		}
		if req.SecurityProfile != nil {
			if *req.SecurityProfile < 0 || *req.SecurityProfile > 2 {
				writeJSON(w, http.StatusBadRequest, Response{
					Success: false,
					Message: "security_profile must be 0, 1, or 2",
				})
				return
			}
			applyChange("security_profile", func() { next.SecurityProfile = *req.SecurityProfile })
		}
		if req.SkipTLSVerify != nil {
			applyChange("skip_tls_verify", func() { next.SkipTLSVerify = *req.SkipTLSVerify })
		}
		if req.TLSCAPath != nil {
			applyChange("tls_ca_path", func() { next.TLSCAPath = *req.TLSCAPath })
		}
		if req.TLSClientCertPath != nil {
			applyChange("tls_client_cert_path", func() { next.TLSClientCertPath = *req.TLSClientCertPath })
		}
		if req.TLSClientKeyPath != nil {
			applyChange("tls_client_key_path", func() { next.TLSClientKeyPath = *req.TLSClientKeyPath })
		}
		if req.LogMode != nil {
			applyChange("log_mode", func() { next.LogMode = *req.LogMode })
		}
		if req.MultiEVSEMode != nil {
			applyChange("multi_evse_mode", func() { next.MultiEVSEMode = *req.MultiEVSEMode })
		}
		if req.EVBatteryCapacity != nil {
			applyChange("ev_battery_capacity", func() { next.EVBatteryCapacity = *req.EVBatteryCapacity })
		}
		if req.OCPPVersion != nil {
			applyChange("ocpp_version", func() { next.OCPPVersion = *req.OCPPVersion })
		}
		if req.PersistMessageQueue != nil {
			applyChange("persist_message_queue", func() { next.PersistMessageQueue = *req.PersistMessageQueue })
		}
		if req.OCPPPassword != nil {
			applyChange("ocpp_password", func() {
				ocppID := next.OCPPID
				if req.OCPPID == nil {
					ocppID = cfg.OCPPID
				}
				if err := config.SetPassword(ocppID, *req.OCPPPassword); err != nil {
					slog.Warn("failed to store OCPP password in keyring", "err", err)
				}
			})
		}
		if req.RFIDTag != nil {
			applyChange("rfid_tag", func() { next.RFIDTag = req.RFIDTag })
		}

		changed := make([]string, 0, len(changedSet))
		for field := range changedSet {
			changed = append(changed, field)
		}
		sort.Strings(changed)

		*cfg = next
		if req.EVBatteryCapacity != nil {
			e.SetEVBatteryCapacity(*req.EVBatteryCapacity * 1000)
		}

		action := "no-op"
		msg := "Configuration unchanged."
		if len(changed) > 0 {
			action = "applied"
			msg = "Configuration updated in memory."
		}
		if restartRequired {
			action = "restart_required"
			msg = "Configuration updated in memory. Restart the process to apply startup-only changes."
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
func SaveConfig(cfg *config.Config, multiStation bool, stationScoped bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if multiStation && stationScoped {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "station-scoped config save is not supported; use /api/v1/config/save to persist the global config",
			})
			return
		}
		if cfg == nil {
			writeJSON(w, http.StatusInternalServerError, Response{
				Success: false,
				Message: "global configuration is not available for saving",
			})
			return
		}
		path := config.DefaultConfigPath()
		if err := cfg.Save(path); err != nil {
			writeJSON(w, http.StatusInternalServerError, Response{
				Success: false,
				Message: "failed to save: " + err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "Configuration saved to " + path + " (sensitive credentials remain in the keyring)",
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
func CreateReservation(e *engine.Engine, hub *ws.Hub, stationID string) http.HandlerFunc {
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
		if hub != nil {
			hub.BroadcastMessage(ws.Message{
				Type:      "reservation_changed",
				StationID: stationID,
				Data: map[string]interface{}{
					"action":         "created",
					"reservation_id": req.ReservationID,
					"connector_id":   req.ConnectorID,
					"id_tag":         req.IDTag,
				},
			})
		}
		writeJSON(w, http.StatusCreated, Response{Success: true, Message: "Reservation created"})
	}
}

// CancelReservation handles DELETE /api/v1/reservations/{reservation_id}
func CancelReservation(e *engine.Engine, hub *ws.Hub, stationID string) http.HandlerFunc {
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
		if hub != nil {
			hub.BroadcastMessage(ws.Message{
				Type:      "reservation_changed",
				StationID: stationID,
				Data: map[string]interface{}{
					"action":         "cancelled",
					"reservation_id": id,
				},
			})
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "Reservation cancelled"})
	}
}
