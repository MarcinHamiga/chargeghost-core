package handlers

import (
	"net/http"
	"strconv"

	"github.com/chargeghost/engine/internal/timeline"
)

func GetTimeline(s *timeline.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		if limit == 0 {
			limit = 100
		}
		f := timeline.TimelineFilter{
			Source:    q.Get("source"),
			Direction: q.Get("direction"),
			EventType: q.Get("event_type"),
			Action:    q.Get("action"),
			Search:    q.Get("search"),
			Limit:     limit,
			Offset:    offset,
		}
		if cid := q.Get("connector_id"); cid != "" {
			if id, err := strconv.Atoi(cid); err == nil {
				f.ConnectorID = &id
			}
		}
		if txid := q.Get("transaction_id"); txid != "" {
			if id, err := strconv.Atoi(txid); err == nil {
				f.TransactionID = &id
			}
		}
		events, total := s.Query(f)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"events": events,
			"total":  total,
		})
	}
}

func GetTimelineCount(s *timeline.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]int{"count": s.Count()})
	}
}

func ClearTimeline(s *timeline.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.Clear()
		w.WriteHeader(http.StatusNoContent)
	}
}
