package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleGetZipcode(w http.ResponseWriter, r *http.Request) {
	if s.zipSvc == nil {
		s.respondError(w, http.StatusServiceUnavailable, "MAINTENANCE", "US postal data is unavailable")
		return
	}
	zipCode := chi.URLParam(r, "zip")
	if !validUSZIP(zipCode) {
		s.respondError(w, http.StatusBadRequest, "INVALID_ZIP", "ZIP code must contain five digits")
		return
	}
	record, ok := s.zipSvc.Lookup(zipCode)
	if !ok {
		s.respondError(w, http.StatusNotFound, "NOT_FOUND", "ZIP code was not found")
		return
	}
	s.respondSuccess(w, record, 1)
}

func validUSZIP(value string) bool {
	if len(value) != 5 {
		return false
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func (s *Server) handleSearchZipcodes(w http.ResponseWriter, r *http.Request) {
	if s.zipSvc == nil {
		s.respondError(w, http.StatusServiceUnavailable, "MAINTENANCE", "US postal data is unavailable")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 || len(q) > 100 {
		s.respondError(w, http.StatusBadRequest, "INVALID_QUERY", "q must contain between 2 and 100 characters")
		return
	}
	limit, err := parseBoundedInt(r.URL.Query().Get("limit"), 20, 1, 100)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be between 1 and 100")
		return
	}
	results := s.zipSvc.Search(q, r.URL.Query().Get("state"), limit)
	s.respondSuccess(w, results, len(results))
}

func (s *Server) handleGetZipcodesByState(w http.ResponseWriter, r *http.Request) {
	if s.zipSvc == nil {
		s.respondError(w, http.StatusServiceUnavailable, "MAINTENANCE", "US postal data is unavailable")
		return
	}
	state := strings.ToUpper(chi.URLParam(r, "state"))
	if len(state) != 2 || state[0] < 'A' || state[0] > 'Z' || state[1] < 'A' || state[1] > 'Z' {
		s.respondError(w, http.StatusBadRequest, "INVALID_STATE", "state must be a two-letter abbreviation")
		return
	}
	page, err := parseBoundedInt(r.URL.Query().Get("page"), 1, 1, 100000)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_PAGE", "page must be a positive integer")
		return
	}
	limit, err := parseBoundedInt(r.URL.Query().Get("limit"), 100, 1, 1000)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_LIMIT", "limit must be between 1 and 1000")
		return
	}
	rows, total := s.zipSvc.ByState(state, page, limit)
	s.respondJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "data": rows, "count": len(rows), "total": total})
}

func (s *Server) handleNearbyZipcodes(w http.ResponseWriter, r *http.Request) {
	if s.zipSvc == nil {
		s.respondError(w, http.StatusServiceUnavailable, "MAINTENANCE", "US postal data is unavailable")
		return
	}
	lat, errLat := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lon, errLon := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	radius, errRadius := strconv.ParseFloat(r.URL.Query().Get("radius_km"), 64)
	limit, errLimit := parseBoundedInt(r.URL.Query().Get("limit"), 10, 1, 100)
	if errLat != nil || errLon != nil || errRadius != nil || errLimit != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_PARAMETERS", "lat, lon, radius_km and limit must be valid numbers")
		return
	}
	results, err := s.zipSvc.Nearby(lat, lon, radius, limit)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "INVALID_PARAMETERS", err.Error())
		return
	}
	s.respondSuccess(w, results, len(results))
}

func parseBoundedInt(raw string, defaultValue, minValue, maxValue int) (int, error) {
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || value > maxValue {
		return 0, strconv.ErrSyntax
	}
	return value, nil
}
