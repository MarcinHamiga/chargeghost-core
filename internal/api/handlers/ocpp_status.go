package handlers

import (
	"net/http"

	"github.com/chargeghost/engine/internal/ocpp"
)

// OCPPStatusProvider exposes the OCPP link health snapshot produced by a
// StatusTracker. It is kept separate from OCPPSendAPI so existing test mocks
// don't have to grow a Status() method just to satisfy the route table.
type OCPPStatusProvider interface {
	Status() ocpp.Status
}

// GetOCPPStatus handles GET /api/v1/ocpp/status and returns the current
// OCPP link health snapshot. The status object is the same shape for OCPP
// 1.6 and 2.0.1; the Version field discriminates.
func GetOCPPStatus(provider OCPPStatusProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if provider == nil {
			writeJSON(w, http.StatusServiceUnavailable, Response{
				Success: false,
				Message: "OCPP bridge is not configured",
			})
			return
		}
		writeJSON(w, http.StatusOK, provider.Status())
	}
}
