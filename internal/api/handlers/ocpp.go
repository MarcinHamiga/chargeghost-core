package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/chargeghost/engine/internal/ocpp"
)

func GetOCPPConfigKeys(m *ocpp.ConfigKeyManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.GetConfigKeyInfo())
	}
}

func PatchOCPPConfigKey(m *ocpp.ConfigKeyManager) http.HandlerFunc {
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
