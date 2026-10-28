package api

import (
	"net/http"
	"strings"
)

// Endpoint is one route, as listed at GET /notus/api/.
type Endpoint struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	About  string `json:"about"`
	// Off says why the endpoint answers with an error on this server, such as
	// a setting that turns it off. Empty when it works.
	Off string `json:"off,omitempty"`
}

// router registers handlers and keeps a line about each one, so the list at
// GET /notus/api/ always matches what is served.
type router struct {
	mux       *http.ServeMux
	endpoints []Endpoint
}

func (r *router) handle(pattern, about string, h http.HandlerFunc) {
	r.mux.HandleFunc(pattern, h)
	method, path, _ := strings.Cut(pattern, " ")
	path = strings.TrimSuffix(path, "{$}") // ServeMux's mark for an exact match
	r.endpoints = append(r.endpoints, Endpoint{Method: method, Path: path, About: about})
}

// DocsURL is where the API is described in full.
const DocsURL = "https://github.com/wispborne/notus-swap/blob/main/docs/api.md"

// index: GET /notus/api/. Lists every endpoint, so a program pointed at the
// server can find what it offers.
func (a *API) index(w http.ResponseWriter, r *http.Request) {
	eps := make([]Endpoint, len(a.endpoints))
	copy(eps, a.endpoints)
	for i := range eps {
		if eps[i].Path == "/notus/api/query" && a.ReadOnly == nil {
			eps[i].Off = "Turned off. Start notus-swap with -sql-api or NOTUS_SQL_API=1."
		}
	}
	writeJSON(w, map[string]any{
		"name":    "notus-swap",
		"version": a.Version,
		"docs":    DocsURL,
		"about": "JSON endpoints used by notus-swap's web UI. There is no login. " +
			"The response shapes can change between releases. Every path outside /notus/ " +
			"is passed on to llama-swap, so the OpenAI-style /v1/ endpoints work here too.",
		"endpoints": eps,
	})
}
