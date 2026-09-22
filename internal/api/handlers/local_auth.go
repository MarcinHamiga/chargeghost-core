package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/chargeghost/engine/internal/ocpp"
)

type LocalAuthEntryDTO struct {
	IDTag       string     `json:"id_tag"`
	Status      string     `json:"authorization_status"`
	ExpiryDate  *time.Time `json:"expiry_date"`
	IsExpired   bool       `json:"is_expired"`
	ParentIDTag *string    `json:"parent_id_tag"`
}

type LocalAuthListResponse struct {
	Version    int                 `json:"version"`
	EntryCount int                 `json:"entry_count"`
	MaxEntries int                 `json:"max_entries"`
	Enabled    bool                `json:"enabled"`
	Entries    []LocalAuthEntryDTO `json:"entries"`
}

type UpdateLocalAuthListRequest struct {
	ListVersion int                   `json:"list_version"`
	Entries     []ocpp.LocalAuthEntry `json:"entries"`
	UpdateType  string                `json:"update_type"` // "Full" | "Differential"
}

type UpdateLocalAuthListResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Version int    `json:"version"`
	Count   int    `json:"count"`
}

func GetLocalAuthList(m ocpp.LocalAuthManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		version, count, maxEntries, enabled := m.GetStats()
		entries := m.GetAllEntries()
		dtos := make([]LocalAuthEntryDTO, 0, len(entries))
		for _, e := range entries {
			expired := e.Expiry != nil && time.Now().After(*e.Expiry)
			dtos = append(dtos, LocalAuthEntryDTO{
				IDTag:       e.IDTag,
				Status:      e.Status,
				ExpiryDate:  e.Expiry,
				IsExpired:   expired,
				ParentIDTag: e.ParentIDTag,
			})
		}
		writeJSON(w, http.StatusOK, LocalAuthListResponse{
			Version: version, EntryCount: count, MaxEntries: maxEntries,
			Enabled: enabled, Entries: dtos,
		})
	}
}

func GetLocalAuthEntry(m ocpp.LocalAuthManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idTag := chi.URLParam(r, "id_tag")
		entry := m.GetEntry(idTag)
		if entry == nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "entry not found"})
			return
		}
		expired := entry.Expiry != nil && time.Now().After(*entry.Expiry)
		writeJSON(w, http.StatusOK, LocalAuthEntryDTO{
			IDTag:       entry.IDTag,
			Status:      entry.Status,
			ExpiryDate:  entry.Expiry,
			IsExpired:   expired,
			ParentIDTag: entry.ParentIDTag,
		})
	}
}

func UpdateLocalAuthList(m ocpp.LocalAuthManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req UpdateLocalAuthListRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: "invalid request body"})
			return
		}
		if err := m.UpdateList(req.ListVersion, req.Entries, req.UpdateType); err != nil {
			writeJSON(w, http.StatusBadRequest, Response{Success: false, Message: err.Error()})
			return
		}
		_, count, _, _ := m.GetStats()
		writeJSON(w, http.StatusOK, UpdateLocalAuthListResponse{
			Success: true, Message: "List updated to version " + strconv.Itoa(req.ListVersion),
			Version: req.ListVersion, Count: count,
		})
	}
}

func DeleteLocalAuthEntry(m ocpp.LocalAuthManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idTag := chi.URLParam(r, "id_tag")
		if err := m.RemoveEntry(idTag); err != nil {
			writeJSON(w, http.StatusNotFound, Response{Success: false, Message: "entry not found"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func ClearLocalAuthList(m ocpp.LocalAuthManager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.Clear()
		writeJSON(w, http.StatusOK, Response{Success: true, Message: "Local auth list cleared"})
	}
}
