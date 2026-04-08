package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	engine "github.com/chargeghost/engine/internal/engine"
	ocpp "github.com/chargeghost/engine/internal/ocpp"
)

func ListChargingProfiles(pm ocpp.ChargingProfileManagerAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, pm.GetChargingProfiles())
	}
}

func GetChargingProfile(pm ocpp.ChargingProfileManagerAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(chi.URLParam(r, "profile_id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid profile_id"})
			return
		}
		connectorID, _ := strconv.Atoi(r.URL.Query().Get("connector_id"))
		p, ok := pm.GetChargingProfile(connectorID, id)
		if !ok {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "profile not found"})
			return
		}
		writeJSON(w, http.StatusOK, p)
	}
}

func InstallChargingProfile(pm ocpp.ChargingProfileManagerAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ConnectorID int                    `json:"connector_id"`
			Profile     engine.ChargingProfile `json:"profile"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}
		if err := pm.SetChargingProfile(req.ConnectorID, req.Profile); err != nil {
			writeJSON(w, http.StatusConflict, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "Profile installed"})
	}
}

func ClearChargingProfiles(pm ocpp.ChargingProfileManagerAPI) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var profileID, connectorID *int
		var purpose *string
		if s := q.Get("profile_id"); s != "" {
			if id, err := strconv.Atoi(s); err == nil {
				profileID = &id
			}
		}
		if s := q.Get("connector_id"); s != "" {
			if id, err := strconv.Atoi(s); err == nil {
				connectorID = &id
			}
		}
		if s := q.Get("purpose"); s != "" {
			purpose = &s
		}
		_ = pm.ClearChargingProfile(connectorID, profileID, purpose, nil)
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "Profiles cleared"})
	}
}

func GetCompositeScheduleHandler(pm ocpp.ChargingProfileManagerAPI, e *engine.Engine) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ConnectorID int `json:"connector_id"`
			Duration    int `json:"duration"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request"})
			return
		}
		c := e.GetConnector(req.ConnectorID)
		if c == nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "connector not found"})
			return
		}
		session := e.GetSession(req.ConnectorID)
		var txStart *time.Time
		var txID int
		if session != nil {
			t := session.StartTime
			txStart = &t
			txID = session.TransactionID
		}
		periods, err := pm.GetCompositeSchedule(req.ConnectorID, txID, time.Now(), req.Duration, c.Voltage, txStart, c.Phase)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, Response{Success: false, Message: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"periods": periods})
	}
}
