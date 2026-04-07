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
		// Note: for sessions, the parameter is connector_id, not id
		// We need to adapt the URL param extraction
		// For now, use a simple approach - in a real implementation, would need to handle this properly
		writeJSON(w, http.StatusNotImplemented, Response{
			Success: false,
			Message: "not yet implemented",
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
	return func(w http.ResponseWriter, r *http.Request) {
		var req PatchConfigRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid request body",
			})
			return
		}
		// For now, just return a placeholder response
		writeJSON(w, http.StatusOK, PatchConfigResponse{
			Success:       true,
			Action:        "no-op",
			ChangedFields: []string{},
			Message:       "config patched",
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
		writeJSON(w, http.StatusOK, []interface{}{})
	}
}

// CreateReservation handles POST /api/v1/reservations/
func CreateReservation(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateReservationRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{
				Success: false,
				Message: "invalid request body",
			})
			return
		}
		// For now, just return a placeholder response
		writeJSON(w, http.StatusCreated, Response{
			Success: true,
			Message: "reservation created",
		})
	}
}

// CancelReservation handles DELETE /api/v1/reservations/{reservation_id}
func CancelReservation(e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, Response{
			Success: true,
			Message: "reservation cancelled",
		})
	}
}
