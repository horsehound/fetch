package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"
)

// handleHub tells an app, before it has a token, what kind of hub this is:
// whether it can make itself a space here, and if it needs an invite code.
func (s *server) handleHub(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"fetch":    true,
		"version":  version,
		"format":   formatVersion,
		"mode":     s.opts.mode,
		"space_mb": s.opts.spaceMB,
	})
}

// handleNewSpace makes a space for a new person and hands back its token.
// The app then creates the key on the device and records its check, the same
// as setting up the first device of any feed.
func (s *server) handleNewSpace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Invite string `json:"invite"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)

	switch s.opts.mode {
	case modePrivate:
		http.Error(w, "This hub is private: it only holds the feed of whoever runs it.", http.StatusForbidden)
		return
	case modeInvite:
		if body.Invite == "" {
			http.Error(w, "This hub needs an invite code. Ask whoever runs it for one.", http.StatusForbidden)
			return
		}
	case modePublic:
		if body.Invite == "" && !s.newSpaces.allow(clientIP(r, s.opts.trustProxy)) {
			http.Error(w, "Too many new spaces from your address today. Try again tomorrow.", http.StatusTooManyRequests)
			return
		}
	}

	token, sp, err := s.reg.create(body.Invite, s.opts.maxSpaces, s.limitFor)
	switch {
	case errors.Is(err, errBadInvite):
		time.Sleep(300 * time.Millisecond) // blunt guessing
		http.Error(w, "That invite code isn't valid. It may have been used already, or expired.", http.StatusForbidden)
	case errors.Is(err, errHubFull):
		http.Error(w, "This hub is full and can't take any more spaces right now.", http.StatusServiceUnavailable)
	case err != nil:
		log.Printf("making a space failed: %v", err)
		http.Error(w, "could not make a space", http.StatusInternalServerError)
	default:
		log.Printf("made space %s", sp.id)
		writeJSON(w, http.StatusCreated, map[string]any{"token": token, "limit_mb": sp.limit >> 20})
	}
}

// handleLeave deletes the caller's own space with everything in it. The
// host's own feed can't go this way; "start over" empties it instead.
func (s *server) handleLeave(w http.ResponseWriter, r *http.Request, sp *space) {
	if sp.owner {
		http.Error(w, "This is the host's own feed; it can't be deleted. Start over instead to empty it.", http.StatusForbidden)
		return
	}
	if _, err := s.reg.remove(sp.id); err != nil && !errors.Is(err, errNoSuchSpace) {
		log.Printf("deleting space %s failed: %v", sp.id, err)
		http.Error(w, "could not delete the space", http.StatusInternalServerError)
		return
	}
	log.Printf("space %s was deleted by its owner", sp.id)
	sp.close()
	w.WriteHeader(http.StatusNoContent)
}

// ---- for whoever runs the hub ----

func (s *server) handleListSpaces(w http.ResponseWriter, r *http.Request, own *space) {
	used, items := own.store.Usage()
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":       s.opts.mode,
		"space_mb":   s.opts.spaceMB,
		"max_spaces": s.opts.maxSpaces,
		"own":        map[string]any{"used_bytes": used, "items": items},
		"spaces":     s.reg.list(),
	})
}

func (s *server) handleDeleteSpace(w http.ResponseWriter, r *http.Request, _ *space) {
	sp, err := s.reg.remove(r.PathValue("id"))
	switch {
	case errors.Is(err, errNoSuchSpace):
		http.NotFound(w, r)
	case err != nil:
		log.Printf("deleting space %s failed: %v", r.PathValue("id"), err)
		http.Error(w, "could not delete the space", http.StatusInternalServerError)
	default:
		log.Printf("deleted space %s", sp.id)
		sp.close()
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) handleNewInvite(w http.ResponseWriter, r *http.Request, _ *space) {
	var body struct {
		Days int   `json:"days"` // how long the code works; default 7
		MB   int64 `json:"mb"`   // storage for the space it makes; default the hub's
	}
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	if body.Days <= 0 {
		body.Days = 7
	}
	if body.Days > 365 || body.MB < 0 {
		http.Error(w, "an invite works for 1 to 365 days, with a storage limit of 0 MB or more", http.StatusBadRequest)
		return
	}
	if s.opts.mode == modePrivate {
		http.Error(w, "This hub is private, so invites wouldn't work. Run it with -mode invite (or FETCH_MODE=invite) to let people in.", http.StatusConflict)
		return
	}
	inv, err := s.reg.newInvite(time.Duration(body.Days)*24*time.Hour, body.MB)
	if err != nil {
		log.Printf("making an invite failed: %v", err)
		http.Error(w, "could not make an invite", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, s.inviteJSON(inv))
}

func (s *server) inviteJSON(inv invite) map[string]any {
	mb := inv.MB
	if mb == 0 {
		mb = s.opts.spaceMB
	}
	return map[string]any{"code": inv.Code, "created": inv.Created, "expires": inv.Expires, "limit_mb": mb}
}

func (s *server) handleListInvites(w http.ResponseWriter, r *http.Request, _ *space) {
	out := []map[string]any{}
	for _, inv := range s.reg.listInvites() {
		out = append(out, s.inviteJSON(inv))
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": out})
}

func (s *server) handleRevokeInvite(w http.ResponseWriter, r *http.Request, _ *space) {
	if !s.reg.revokeInvite(r.PathValue("code")) {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
