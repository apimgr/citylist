package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"

	"github.com/apimgr/citylist/src/common/i18n"
	"github.com/apimgr/citylist/src/overlay/tor"
	"github.com/go-chi/chi/v5"
)

type torConfirmRequest struct {
	Confirm bool `json:"confirm"`
}

type torVanityStartRequest struct {
	Prefix  string `json:"prefix"`
	Workers int    `json:"workers"`
}

type torVanityApplyRequest struct {
	Address string `json:"address"`
	Confirm bool   `json:"confirm"`
}

type torImportRequest struct {
	Path    string `json:"path"`
	Confirm bool   `json:"confirm"`
}

func (s *Server) registerTorControlRoutes(router chi.Router) {
	router.Get("/server/tor/status", s.torControl(s.handleTorStatus))
	router.Post("/server/tor/validate", s.torControl(s.handleTorValidate))
	router.Post("/server/tor/restart", s.torControl(s.handleTorRestart))
	router.Post("/server/tor/regenerate", s.torControl(s.handleTorRegenerate))
	router.Post("/server/tor/vanity/start", s.torControl(s.handleTorVanityStart))
	router.Post("/server/tor/vanity/stop", s.torControl(s.handleTorVanityStop))
	router.Post("/server/tor/vanity/apply", s.torControl(s.handleTorVanityApply))
	router.Post("/server/tor/import-keys", s.torControl(s.handleTorImportKeys))
}

func (s *Server) torControl(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isTorControlLoopback(r) {
			s.writeTorError(w, r, http.StatusNotFound, "NOT_FOUND")
			return
		}
		next(w, r)
	}
}

func isTorControlLoopback(r *http.Request) bool {
	peer, _, err := net.SplitHostPort(originalPeer(r))
	if err != nil {
		peer = originalPeer(r)
	}
	ip := net.ParseIP(strings.Trim(peer, "[]"))
	return ip != nil && ip.IsLoopback()
}

func (s *Server) decodeTorControlJSON(w http.ResponseWriter, r *http.Request, target any, required bool) bool {
	if r.Body == nil || r.Body == http.NoBody {
		if required {
			s.writeTorError(w, r, http.StatusBadRequest, "BAD_REQUEST")
			return false
		}
		return true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16*1024))
	if err != nil {
		s.writeTorError(w, r, http.StatusBadRequest, "BAD_REQUEST")
		return false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		if required {
			s.writeTorError(w, r, http.StatusBadRequest, "BAD_REQUEST")
			return false
		}
		return true
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		s.writeTorError(w, r, http.StatusBadRequest, "BAD_REQUEST")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) && !required {
			return true
		}
		s.writeTorError(w, r, http.StatusBadRequest, "BAD_REQUEST")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		s.writeTorError(w, r, http.StatusBadRequest, "BAD_REQUEST")
		return false
	}
	return true
}

func (s *Server) writeTorError(w http.ResponseWriter, r *http.Request, status int, code string) {
	translationKey := "errors." + strings.ToLower(code)
	message := i18n.Translate(i18n.LangFromRequest(r), translationKey)
	if message == translationKey {
		message = i18n.Translate(i18n.LangFromRequest(r), "errors.bad_request")
	}
	s.respondJSON(w, status, APIResponse{Success: false, Error: code, Message: message})
}

func (s *Server) handleTorStatus(w http.ResponseWriter, _ *http.Request) {
	var snapshot struct {
		Running            bool   `json:"running"`
		PID                int    `json:"pid,omitempty"`
		Version            string `json:"version"`
		OnionAddress       string `json:"onion_address,omitempty"`
		BootstrapPercent   int    `json:"bootstrap_percent"`
		CircuitEstablished bool   `json:"circuit_established"`
	}
	var progress any = map[string]any{"state": "idle", "candidates": []string{}}
	if s.torManager != nil {
		state := s.torManager.Snapshot()
		snapshot.Running = state.Running
		snapshot.PID = state.PID
		snapshot.Version = state.Version
		snapshot.OnionAddress = state.Hostname
		snapshot.BootstrapPercent = state.BootstrapPercent
		snapshot.CircuitEstablished = state.CircuitEstablished
		progress = s.torManager.VanityStatus()
	} else {
		snapshot.Version = "unavailable"
	}
	s.respondSuccess(w, map[string]any{"running": snapshot.Running, "pid": snapshot.PID,
		"version": snapshot.Version, "onion_address": snapshot.OnionAddress,
		"bootstrap_percent": snapshot.BootstrapPercent, "circuit_established": snapshot.CircuitEstablished,
		"vanity": progress}, 1)
}

func (s *Server) handleTorValidate(w http.ResponseWriter, r *http.Request) {
	if !s.decodeTorControlJSON(w, r, &struct{}{}, false) {
		return
	}
	if s.torManager == nil {
		s.respondSuccess(w, map[string]any{"valid": !s.torStartupFailed, "errors": []any{}}, 1)
		return
	}
	if err := s.torManager.Validate(); err != nil {
		s.respondSuccess(w, map[string]any{"valid": false, "errors": []map[string]string{{"field": "server.tor", "message": i18n.Translate(i18n.LangFromRequest(r), "errors.bad_request")}}}, 1)
		return
	}
	s.respondSuccess(w, map[string]any{"valid": true, "errors": []any{}}, 1)
}

func (s *Server) handleTorRestart(w http.ResponseWriter, r *http.Request) {
	if !s.decodeTorControlJSON(w, r, &struct{}{}, false) {
		return
	}
	if s.torManager == nil {
		s.writeTorUnavailable(w, r)
		return
	}
	if err := s.torManager.Restart(r.Context()); err != nil {
		s.writeTorError(w, r, http.StatusInternalServerError, "SERVER_ERROR")
		return
	}
	s.respondSuccess(w, map[string]any{"restarted": true, "onion_address": s.torManager.Snapshot().Hostname}, 1)
}

func (s *Server) handleTorRegenerate(w http.ResponseWriter, r *http.Request) {
	var request torConfirmRequest
	if !s.decodeTorControlJSON(w, r, &request, true) {
		return
	}
	if !request.Confirm {
		s.writeTorError(w, r, http.StatusBadRequest, "VALIDATION_FAILED")
		return
	}
	if s.torManager == nil {
		s.writeTorUnavailable(w, r)
		return
	}
	address, previous, err := s.torManager.Regenerate(r.Context())
	if err != nil {
		s.writeTorError(w, r, http.StatusInternalServerError, "SERVER_ERROR")
		return
	}
	s.respondSuccess(w, map[string]string{"onion_address": address, "previous_address": previous}, 1)
}

func (s *Server) handleTorVanityStart(w http.ResponseWriter, r *http.Request) {
	var request torVanityStartRequest
	if !s.decodeTorControlJSON(w, r, &request, true) {
		return
	}
	if s.torManager == nil {
		s.writeTorUnavailable(w, r)
		return
	}
	if err := s.torManager.StartVanitySearch(request.Prefix, request.Workers); err != nil {
		code, status := "VALIDATION_FAILED", http.StatusBadRequest
		if strings.Contains(err.Error(), "already running") {
			code, status = "CONFLICT", http.StatusConflict
		}
		s.writeTorError(w, r, status, code)
		return
	}
	progress := s.torManager.VanityStatus()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": progress})
}

func (s *Server) handleTorVanityStop(w http.ResponseWriter, r *http.Request) {
	if !s.decodeTorControlJSON(w, r, &struct{}{}, false) {
		return
	}
	var candidates []string
	if s.torManager != nil {
		candidates = s.torManager.StopVanitySearch()
	} else {
		candidates = []string{}
	}
	s.respondSuccess(w, map[string]any{"stopped": true, "candidates": candidates}, 1)
}

func (s *Server) handleTorVanityApply(w http.ResponseWriter, r *http.Request) {
	var request torVanityApplyRequest
	if !s.decodeTorControlJSON(w, r, &request, true) {
		return
	}
	if !request.Confirm {
		s.writeTorError(w, r, http.StatusBadRequest, "VALIDATION_FAILED")
		return
	}
	if s.torManager == nil {
		s.writeTorUnavailable(w, r)
		return
	}
	address, err := s.torManager.ApplyVanityCandidate(r.Context(), request.Address)
	if err != nil {
		status, code := http.StatusInternalServerError, "SERVER_ERROR"
		switch {
		case errors.Is(err, tor.ErrCandidateNotFound):
			status, code = http.StatusNotFound, "NOT_FOUND"
		case errors.Is(err, tor.ErrCandidateAmbiguous):
			status, code = http.StatusConflict, "CONFLICT"
		case errors.Is(err, tor.ErrInvalidIdentity):
			status, code = http.StatusBadRequest, "VALIDATION_FAILED"
		}
		s.writeTorError(w, r, status, code)
		return
	}
	s.respondSuccess(w, map[string]string{"onion_address": address}, 1)
}

func (s *Server) handleTorImportKeys(w http.ResponseWriter, r *http.Request) {
	var request torImportRequest
	if !s.decodeTorControlJSON(w, r, &request, true) {
		return
	}
	if !request.Confirm {
		s.writeTorError(w, r, http.StatusBadRequest, "VALIDATION_FAILED")
		return
	}
	if s.torManager == nil {
		s.writeTorUnavailable(w, r)
		return
	}
	address, err := s.torManager.ImportIdentity(r.Context(), request.Path)
	if err != nil {
		status, code := http.StatusInternalServerError, "SERVER_ERROR"
		if errors.Is(err, tor.ErrInvalidIdentity) {
			status, code = http.StatusBadRequest, "VALIDATION_FAILED"
		}
		s.writeTorError(w, r, status, code)
		return
	}
	s.respondSuccess(w, map[string]string{"onion_address": address}, 1)
}

func (s *Server) writeTorUnavailable(w http.ResponseWriter, r *http.Request) {
	s.writeTorError(w, r, http.StatusServiceUnavailable, "SERVER_ERROR")
}
