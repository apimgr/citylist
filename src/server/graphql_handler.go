package server

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"

	"github.com/apimgr/citylist/src/common/i18n"
)

type graphQLRequest struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName"`
	Variables     map[string]interface{} `json:"variables"`
}

func (s *Server) handleGraphQL(w http.ResponseWriter, r *http.Request) {
	var request graphQLRequest
	switch r.Method {
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil {
			s.respondError(w, http.StatusBadRequest, "INVALID_GRAPHQL_REQUEST", i18n.Translate(i18n.LangFromRequest(r), "errors.bad_request"))
			return
		}
		switch mediaType {
		case "application/json":
			decoder := json.NewDecoder(r.Body)
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&request); err != nil {
				s.respondError(w, http.StatusBadRequest, "INVALID_GRAPHQL_REQUEST", i18n.Translate(i18n.LangFromRequest(r), "errors.bad_request"))
				return
			}
			var extra interface{}
			if err := decoder.Decode(&extra); err != io.EOF {
				s.respondError(w, http.StatusBadRequest, "INVALID_GRAPHQL_REQUEST", i18n.Translate(i18n.LangFromRequest(r), "errors.bad_request"))
				return
			}
		case "application/x-www-form-urlencoded":
			if err := r.ParseForm(); err != nil {
				s.respondError(w, http.StatusBadRequest, "INVALID_GRAPHQL_REQUEST", i18n.Translate(i18n.LangFromRequest(r), "errors.bad_request"))
				return
			}
			for key := range r.PostForm {
				if key != "query" && key != "operationName" {
					s.respondError(w, http.StatusBadRequest, "INVALID_GRAPHQL_REQUEST", i18n.Translate(i18n.LangFromRequest(r), "errors.bad_request"))
					return
				}
			}
			request.Query = r.PostForm.Get("query")
			request.OperationName = r.PostForm.Get("operationName")
		default:
			s.respondError(w, http.StatusBadRequest, "INVALID_GRAPHQL_REQUEST", i18n.Translate(i18n.LangFromRequest(r), "errors.bad_request"))
			return
		}
	default:
		w.Header().Set("Allow", http.MethodPost)
		s.respondError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", i18n.Translate(i18n.LangFromRequest(r), "errors.method_not_allowed"))
		return
	}
	if request.Query == "" {
		s.respondError(w, http.StatusBadRequest, "MISSING_QUERY", i18n.Translate(i18n.LangFromRequest(r), "errors.bad_request"))
		return
	}
	schema := s.graphQL
	if localized := s.graphQLByLanguage[i18n.LangFromRequest(r)]; localized != nil {
		schema = localized
	}
	result := schema.Exec(r.Context(), request.Query, request.OperationName, request.Variables)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

func (s *Server) handleGraphQLDocs(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, r, "graphql.html", nil)
}
