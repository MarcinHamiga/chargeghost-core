package api

import (
	"context"
	"net/http"
	"time"

	"github.com/chargeghost/engine/internal/config"
	"github.com/go-chi/chi/v5"
)

// Fleet handlers

func ListStations(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, fleet.AllSnapshots())
	}
}

func GetFleetStatus(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, FleetStatusResponse{Stations: fleet.AllSnapshots()})
	}
}

func GetFleetConfig(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, FleetConfigResponse{Config: fleet.Config()})
	}
}

func ListOperations(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, fleet.Operations())
	}
}

func GetOperation(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "operation_id")
		op, ok := fleet.Operation(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "operation not found"})
			return
		}
		writeJSON(w, http.StatusOK, op)
	}
}

func ReloadFleet(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := fleet.Reload(ctx); err != nil {
			writeJSON(w, http.StatusInternalServerError, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "fleet reloaded"})
	}
}

func CreateStation(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateStationRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}
		if req.OCPPID == "" {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "ocpp_id is required"})
			return
		}
		if req.ID == "" {
			req.ID = req.OCPPID
		}
		if req.Connectors == nil || len(req.Connectors) == 0 {
			req.Connectors = []config.ConnectorConfig{{Voltage: 230, Current: 32, Phase: 1}}
		}
		if req.OCPPVersion == "" {
			req.OCPPVersion = "1.6"
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		snapshot, opID, err := fleet.CreateStation(ctx, req)
		if err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, OperationResponse{Success: true, OperationID: opID, Message: "station created", Snapshot: snapshot})
	}
}

func GetStationStatus(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		snapshot, ok := fleet.Snapshot(id)
		if !ok {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "station not found"})
			return
		}
		writeJSON(w, http.StatusOK, snapshot)
	}
}

// PatchStationConfig handles PATCH /api/v1/stations/{id}/config.
func PatchStationConfig(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		var req PatchStationConfigRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}
		if req.Connectors != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "connectors cannot be modified via PATCH /config; use the /connectors endpoints"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		result, err := fleet.UpdateStation(ctx, id, req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: err.Error()})
			return
		}
		action := "applied"
		msg := "Configuration updated in memory."
		if result.Restarted {
			action = "restarted"
			msg = "Configuration updated and station restarted."
		} else if result.RestartRequired {
			action = "restart_required"
			msg = "Configuration updated in memory. Restart the station to apply startup-only changes."
		}
		writeJSON(w, http.StatusOK, PatchStationResponse{
			Success:         true,
			Action:          action,
			ChangedFields:   result.ChangedFields,
			RestartRequired: result.RestartRequired,
			OperationID:     result.OperationID,
			Message:         msg,
		})
	}
}

// PatchDefaultStationConfig handles PATCH /api/v1/config when running under
// the fleet router. Unlike the legacy single-station PatchConfig (which
// mutates an in-memory Config clone that is lost on restart and can never be
// persisted — POST /config/save has nothing durable to write), this writes
// through to the default station's entry in the global config via the same
// fleet.UpdateStation path used by the station-scoped PATCH, so the change
// is real, restart-surviving, and persistable.
func PatchDefaultStationConfig(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req PatchStationConfigRequest
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}
		if req.Connectors != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "connectors cannot be modified via PATCH /config; use the /connectors endpoints"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		result, err := fleet.UpdateStation(ctx, fleet.DefaultStationID(), req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: err.Error()})
			return
		}
		action := "no-op"
		msg := "Configuration unchanged."
		if len(result.ChangedFields) > 0 {
			action = "applied"
			msg = "Configuration updated in memory."
		}
		if result.Restarted {
			action = "restarted"
			msg = "Configuration updated and station restarted."
		} else if result.RestartRequired {
			action = "restart_required"
			msg = "Configuration updated in memory. Restart the station to apply startup-only changes."
		}
		writeJSON(w, http.StatusOK, PatchConfigResponse{
			Success:       true,
			Action:        action,
			ChangedFields: result.ChangedFields,
			Message:       msg,
		})
	}
}

func DeleteStation(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		q := r.URL.Query()
		opts := DeleteStationOptions{
			Force:         q.Get("force") == "true",
			DeleteState:   q.Get("delete_state") == "true",
			ClearPassword: q.Get("clear_password") == "true",
			NewDefaultID:  q.Get("new_default_id"),
			AllowEmpty:    q.Get("allow_empty") == "true",
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := fleet.DeleteStation(ctx, id, opts); err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "station deleted"})
	}
}

func StartStation(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		opID, err := fleet.StartStation(ctx, id)
		if err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, OperationResponse{Success: true, OperationID: opID, Message: "station start requested"})
	}
}

func StopStation(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		opID, err := fleet.StopStation(ctx, id)
		if err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, OperationResponse{Success: true, OperationID: opID, Message: "station stop requested"})
	}
}

func RestartStation(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		opID, err := fleet.RestartStation(ctx, id)
		if err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, OperationResponse{Success: true, OperationID: opID, Message: "station restart requested"})
	}
}

func EnableStation(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		opID, err := fleet.EnableStation(ctx, id)
		if err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, OperationResponse{Success: true, OperationID: opID, Message: "station enabled"})
	}
}

func DisableStation(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		opID, err := fleet.DisableStation(ctx, id)
		if err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, OperationResponse{Success: true, OperationID: opID, Message: "station disabled"})
	}
}

func ReloadStation(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		if _, ok := fleet.Snapshot(id); !ok {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "station not found"})
			return
		}
		// For now, reload the whole fleet and reconcile.
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := fleet.Reload(ctx); err != nil {
			writeJSON(w, http.StatusInternalServerError, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "station reloaded"})
	}
}

func ReconnectStation(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		opID, err := fleet.RestartStation(ctx, id)
		if err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, OperationResponse{Success: true, OperationID: opID, Message: "station reconnect requested (full restart)", Scope: "station_restart"})
	}
}

func SetOCPPPassword(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		var req struct {
			Password string `json:"password"`
		}
		if err := parseJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}
		if req.Password == "" {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "password is required"})
			return
		}
		if err := fleet.SetOCPPPassword(id, req.Password); err != nil {
			writeJSON(w, http.StatusInternalServerError, Response{Success: false, Message: err.Error()})
			return
		}
		// The OCPP client reads the password once at bridge construction, so
		// a running station keeps its old auth until it is reconnected.
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "password stored — reconnect the station to apply it"})
	}
}

func ClearOCPPPassword(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		if err := fleet.ClearOCPPPassword(id); err != nil {
			writeJSON(w, http.StatusInternalServerError, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "password cleared — reconnect the station to apply it"})
	}
}

func TestCredentials(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		if err := fleet.TestCredentials(id); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "credentials present"})
	}
}

func GetQueueStatus(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		status, err := fleet.QueueStatus(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, status)
	}
}

func DrainQueue(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		opID, err := fleet.QueueDrain(id)
		if err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusAccepted, OperationResponse{Success: true, OperationID: opID, Message: "queue drain started"})
	}
}

func ClearQueue(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		var req struct {
			Confirm bool `json:"confirm"`
		}
		if err := parseJSON(r, &req); err != nil || !req.Confirm {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "confirm=true is required"})
			return
		}
		if err := fleet.QueueClear(id); err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "queue cleared"})
	}
}

func GetDeadLetter(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		entries, err := fleet.QueueDeadLetter(id)
		if err != nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, entries)
	}
}

func ClearDeadLetter(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		if err := fleet.QueueDeadLetterClear(id); err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "dead-letter cleared"})
	}
}

func PersistStation(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "station_id")
		if err := fleet.PersistStation(id); err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "station state persisted"})
	}
}

func SaveFleetConfig(fleet FleetManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fleet.Save(); err != nil {
			writeJSON(w, http.StatusInternalServerError, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "global config saved"})
	}
}
