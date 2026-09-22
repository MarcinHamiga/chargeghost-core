package handlers

import "net/http"

type AboutResponse struct {
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	OCPPVersions []string `json:"ocpp_versions"`
	Features     []string `json:"features"`
	License      string   `json:"license"`
	Copyright    string   `json:"copyright"`
}

func GetAbout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, AboutResponse{
			Version:      "0.5.0",
			Description:  "ChargeGhost EVSE Simulator",
			OCPPVersions: []string{"1.6J", "2.0.1"},
			Features: []string{
				"OCPP 1.6J and 2.0.1 charging station simulation",
				"Charging profile management and composite schedules",
				"Local authorization list",
				"Firmware and diagnostics simulation",
				"REST API and WebSocket event streaming",
				"Offline message queue with JSON persistence",
			},
			License:   "AGPL-3.0",
			Copyright: "2026 Marcin Hamiga",
		})
	}
}
