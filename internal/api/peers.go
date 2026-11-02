package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wispborne/notus-swap/internal/peers"
)

func (a *API) registerPeers(r *router) {
	r.handle("GET /notus/api/peers", "The Servers list: this server and the other notus-swap servers it shows, in order, with whether each one answers. The first is the primary.", a.listPeers)
	r.handle("POST /notus/api/peers/test", `Check an address before adding it. Body: {"url": "http://host:8080"}.`, a.testPeer)
	r.handle("POST /notus/api/peers", `Add a server to the end of the list. Body: {"url": "...", "name": "..."}; the name is optional.`, a.addPeer)
	r.handle("PATCH /notus/api/peers/{id}", `Rename a server, change its address, or set its icon. Body: {"name": "..."}, {"url": "..."} or {"icon": "..."}. Only the icon can be set for this server; "" goes back to the default.`, a.editPeer)
	r.handle("DELETE /notus/api/peers/{id}", "Remove a server from the list.", a.removePeer)
	r.handle("PUT /notus/api/peers/order", `Put the list in a new order. Body: {"ids": [...]}, naming every server once.`, a.orderPeers)
	r.handle("/notus/peer/{id}/{path...}", "Pass a call on to another server in the list: its /notus/api/ paths and llama-swap's /logs/ streams.", a.forwardPeer)
}

func (a *API) peersOff(w http.ResponseWriter) bool {
	if a.Peers == nil {
		http.Error(w, "The Servers list isn't available on this server.", http.StatusServiceUnavailable)
		return true
	}
	return false
}

// peerFail answers with a peers.Error's message and code, or a 500.
func (a *API) peerFail(w http.ResponseWriter, err error) {
	var pe *peers.Error
	if errors.As(err, &pe) {
		http.Error(w, pe.Msg, pe.Code)
		return
	}
	a.fail(w, err)
}

func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	b, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil || json.Unmarshal(b, v) != nil {
		http.Error(w, "Expected a JSON request body", http.StatusBadRequest)
		return false
	}
	return true
}

// listPeers: GET /notus/api/peers
func (a *API) listPeers(w http.ResponseWriter, r *http.Request) {
	if a.peersOff(w) {
		return
	}
	writeJSON(w, map[string]any{"self": a.Peers.SelfID, "servers": a.Peers.List()})
}

// testPeer: POST /notus/api/peers/test
func (a *API) testPeer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL string `json:"url"`
	}
	if a.peersOff(w) || !readBody(w, r, &in) {
		return
	}
	p, err := a.Peers.Test(r.Context(), in.URL)
	if err != nil {
		a.peerFail(w, err)
		return
	}
	writeJSON(w, p)
}

// addPeer: POST /notus/api/peers
func (a *API) addPeer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if a.peersOff(w) || !readBody(w, r, &in) {
		return
	}
	s, err := a.Peers.Add(r.Context(), in.URL, in.Name)
	if err != nil {
		a.peerFail(w, err)
		return
	}
	writeJSON(w, s)
}

// editPeer: PATCH /notus/api/peers/{id}
func (a *API) editPeer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name *string `json:"name"`
		URL  *string `json:"url"`
		Icon *string `json:"icon"`
	}
	if a.peersOff(w) || !readBody(w, r, &in) {
		return
	}
	id := r.PathValue("id")
	if in.Icon != nil {
		if err := a.Peers.SetIcon(r.Context(), id, *in.Icon); err != nil {
			a.peerFail(w, err)
			return
		}
	}
	if in.Name != nil || in.URL != nil {
		if err := a.Peers.Edit(r.Context(), id, in.Name, in.URL); err != nil {
			a.peerFail(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// removePeer: DELETE /notus/api/peers/{id}
func (a *API) removePeer(w http.ResponseWriter, r *http.Request) {
	if a.peersOff(w) {
		return
	}
	if err := a.Peers.Remove(r.Context(), r.PathValue("id")); err != nil {
		a.peerFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// orderPeers: PUT /notus/api/peers/order
func (a *API) orderPeers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if a.peersOff(w) || !readBody(w, r, &in) {
		return
	}
	if err := a.Peers.Order(r.Context(), in.IDs); err != nil {
		a.peerFail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// forwardPeer: /notus/peer/{id}/{path...}, any method.
func (a *API) forwardPeer(w http.ResponseWriter, r *http.Request) {
	if a.peersOff(w) {
		return
	}
	a.Peers.ServeHTTP(w, r)
}
