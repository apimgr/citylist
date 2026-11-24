package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

type privacySignalKey struct{}

func gpcOptOut(r *http.Request) bool {
	return privacySignal(r) == "gpc"
}

func privacySignalOptOut(r *http.Request) bool {
	return privacySignal(r) != ""
}

func privacySignal(r *http.Request) string {
	signal, _ := r.Context().Value(privacySignalKey{}).(string)
	return signal
}

func (s *Server) gpcMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signal := ""
		if s.cfg.Web.Headers.HonorSecGPC && r.Header.Get("Sec-GPC") == "1" {
			signal = "gpc"
		} else if s.cfg.Web.Headers.HonorDNT && r.Header.Get("DNT") == "1" {
			signal = "dnt"
		}
		if signal != "" {
			r = r.WithContext(context.WithValue(r.Context(), privacySignalKey{}, signal))
			s.auditPrivacySignal(r, signal)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) auditPrivacySignal(r *http.Request, signal string) {
	if s.store == nil {
		return
	}
	details, _ := json.Marshal(map[string]string{"signal": signal})
	action := "compliance.gpc_honored"
	if signal == "dnt" {
		action = "compliance.dnt_honored"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	err := s.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (level, category, action, target_type, details, success) VALUES ('info', 'compliance', ?, 'privacy_signal', ?, 1)`, action, string(details))
		return err
	})
	if err != nil {
		slog.Warn("compliance.gpc_audit_failed", "error", err)
	}
}
