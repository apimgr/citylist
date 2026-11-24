package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

func (s *Server) handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	kind, _, valid := s.resolveBearerToken(r, false)
	if !valid {
		s.respondError(w, http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required")
		return
	}
	s.respondSuccess(w, map[string]string{"kind": kind}, 1)
}

func (s *Server) resolveBearerToken(r *http.Request, updateUse bool) (kind, ownerID string, ok bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", "", false
	}
	providedHash := sha256.Sum256([]byte(parts[1]))
	if s.cfg.Server.Token != "" {
		wantedHash := sha256.Sum256([]byte(s.cfg.Server.Token))
		if subtle.ConstantTimeCompare(providedHash[:], wantedHash[:]) == 1 {
			return "operator", "", true
		}
	}
	identity, valid := s.lookupOwnerToken(r.Context(), parts[1], updateUse)
	if !valid {
		return "", "", false
	}
	return "owner", identity.ID, true
}
