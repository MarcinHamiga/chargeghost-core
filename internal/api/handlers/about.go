package handlers

import "net/http"

func GetAbout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"version":       "0.5.0",
			"description":   "ChargeGhost EVSE Simulator",
			"ocpp_versions": []string{"1.6J"},
			"features": []string{
				"OCPP 1.6J charging station simulation",
				"Smart charging profiles (TxDefaultProfile, TxProfile, ChargePointMaxProfile)",
				"Local authorization list",
				"Firmware and diagnostics simulation",
				"REST API and WebSocket event streaming",
				"Offline message queue with JSON persistence",
			},
			"license":   "MIT",
			"copyright": "2025 ChargeGhost",
		})
	}
}
