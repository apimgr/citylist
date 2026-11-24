package server

import "net/http"

type cliBinaryVersion struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

type autoDiscoverResponse struct {
	Server        autoDiscoverServerInfo      `json:"server"`
	CLIVersions   map[string]cliBinaryVersion `json:"cli_versions"`
	CLIMinVersion string                      `json:"cli_min_version,omitempty"`
}

type autoDiscoverServerInfo struct {
	Name       string      `json:"name"`
	Version    string      `json:"version"`
	Build      HealthBuild `json:"build"`
	APIVersion string      `json:"api_version"`
	Domain     string      `json:"domain,omitempty"`
}

func (s *Server) handleAutoDiscover(w http.ResponseWriter, r *http.Request) {
	response := autoDiscoverResponse{
		Server: autoDiscoverServerInfo{
			Name:       s.brandTitle(),
			Version:    s.version,
			Build:      HealthBuild{Commit: s.commit, Date: s.buildDate},
			APIVersion: s.cfg.Server.APIVersion,
			Domain:     s.cfg.Server.FQDN,
		},
		CLIVersions:   map[string]cliBinaryVersion{},
		CLIMinVersion: s.cfg.Server.Update.CLIMinVersion,
	}
	s.respondJSON(w, http.StatusOK, response)
}
