package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// A hub holds spaces. Each space is one feed with its own token, key check
// and items: the devices of one person. The host's own space lives in the
// data directory itself (as it always has); spaces made for other people live
// under data/spaces/<id>/. Nobody can see into another space, and the hub
// can't read any of them: every space is encrypted with a key only its
// devices have.

// Who may make a new space.
const (
	modePrivate = "private" // only the host's own space
	modeInvite  = "invite"  // with an invite code from the host
	modePublic  = "public"  // anyone
)

type space struct {
	id    string // "" for the host's own space
	owner bool
	store *Store
	limit int64 // storage limit in bytes; 0 = none

	lastUsed atomic.Int64 // unix ms

	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func newSpace(id string, owner bool, store *Store, limit int64) *space {
	sp := &space{id: id, owner: owner, store: store, limit: limit, subs: map[chan struct{}]struct{}{}}
	sp.lastUsed.Store(time.Now().UnixMilli())
	return sp
}

// spaceRecord is what spaces.json keeps about a guest space. Only a hash of
// the token is stored, so the file alone doesn't let anyone in.
type spaceRecord struct {
	ID        string `json:"id"`
	TokenHash string `json:"token_hash"`
	Created   int64  `json:"created"`
	LastUsed  int64  `json:"last_used"`
	MB        int64  `json:"mb,omitempty"` // storage limit; 0 = the hub's default
}

type invite struct {
	Code    string `json:"code"`
	Created int64  `json:"created"`
	Expires int64  `json:"expires"`
	MB      int64  `json:"mb,omitempty"` // storage limit for the space it makes; 0 = the hub's default
}

type registry struct {
	dir string

	mu      sync.Mutex
	records map[string]*spaceRecord // by id
	spaces  map[string]*space       // by id, opened
	byHash  map[string]*space       // by token hash
	invites []invite
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (r *registry) spacesPath() string  { return filepath.Join(r.dir, "spaces.json") }
func (r *registry) invitesPath() string { return filepath.Join(r.dir, "invites.json") }
func (r *registry) spaceDir(id string) string {
	return filepath.Join(r.dir, "spaces", id)
}

func openRegistry(dir string, defaultLimit func(mb int64) int64) (*registry, error) {
	r := &registry{dir: dir, records: map[string]*spaceRecord{}, spaces: map[string]*space{}, byHash: map[string]*space{}}
	var recs []*spaceRecord
	if b, err := os.ReadFile(r.spacesPath()); err == nil {
		if err := json.Unmarshal(b, &recs); err != nil {
			return nil, fmt.Errorf("spaces.json is corrupt: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, rec := range recs {
		st, err := OpenStore(r.spaceDir(rec.ID))
		if err != nil {
			return nil, fmt.Errorf("space %s: %w", rec.ID, err)
		}
		sp := newSpace(rec.ID, false, st, defaultLimit(rec.MB))
		sp.lastUsed.Store(rec.LastUsed)
		r.records[rec.ID] = rec
		r.spaces[rec.ID] = sp
		r.byHash[rec.TokenHash] = sp
	}
	if b, err := os.ReadFile(r.invitesPath()); err == nil {
		if err := json.Unmarshal(b, &r.invites); err != nil {
			return nil, fmt.Errorf("invites.json is corrupt: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	// Folders under spaces/ that no record points to are leftovers of a
	// space that was half made or half deleted.
	entries, _ := os.ReadDir(filepath.Join(dir, "spaces"))
	for _, e := range entries {
		if _, ok := r.records[e.Name()]; !ok {
			os.RemoveAll(filepath.Join(dir, "spaces", e.Name()))
		}
	}
	return r, nil
}

// saveLocked writes spaces.json; r.mu must be held.
func (r *registry) saveLocked() error {
	recs := make([]*spaceRecord, 0, len(r.records))
	for id, rec := range r.records {
		rec.LastUsed = r.spaces[id].lastUsed.Load()
		recs = append(recs, rec)
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Created < recs[j].Created })
	return writeAtomic(r.spacesPath(), recs)
}

func (r *registry) saveInvitesLocked() error {
	inv := r.invites
	if inv == nil {
		inv = []invite{}
	}
	return writeAtomic(r.invitesPath(), inv)
}

func (r *registry) find(token string) *space {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byHash[hashToken(token)]
}

func (r *registry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.records)
}

var (
	errBadInvite   = errors.New("that invite code isn't valid; it may have been used already or expired")
	errHubFull     = errors.New("this hub can't take any more spaces right now")
	errNoSuchSpace = errors.New("no such space")
)

// create makes a guest space and returns its token. With code != "", the
// invite is used up (and its storage limit applies).
func (r *registry) create(code string, maxSpaces int, limit func(mb int64) int64) (string, *space, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var mb int64
	inv := -1
	if code != "" {
		if inv = r.inviteIndexLocked(code); inv < 0 {
			return "", nil, errBadInvite
		}
		mb = r.invites[inv].MB
	}
	if maxSpaces > 0 && len(r.records) >= maxSpaces {
		return "", nil, errHubFull
	}
	id := randomHex(6)
	token, err := newToken()
	if err != nil {
		return "", nil, err
	}
	st, err := OpenStore(r.spaceDir(id))
	if err != nil {
		return "", nil, err
	}
	now := time.Now().UnixMilli()
	rec := &spaceRecord{ID: id, TokenHash: hashToken(token), Created: now, LastUsed: now, MB: mb}
	sp := newSpace(id, false, st, limit(mb))
	r.records[id] = rec
	r.spaces[id] = sp
	r.byHash[rec.TokenHash] = sp
	if err := r.saveLocked(); err != nil {
		delete(r.records, id)
		delete(r.spaces, id)
		delete(r.byHash, rec.TokenHash)
		os.RemoveAll(r.spaceDir(id))
		return "", nil, err
	}
	if inv >= 0 { // used up
		r.invites = append(r.invites[:inv], r.invites[inv+1:]...)
		if err := r.saveInvitesLocked(); err != nil {
			log.Printf("saving invites failed: %v", err)
		}
	}
	return token, sp, nil
}

// remove deletes a guest space and everything in it.
func (r *registry) remove(id string) (*space, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.records[id]
	if !ok {
		return nil, errNoSuchSpace
	}
	sp := r.spaces[id]
	delete(r.records, id)
	delete(r.spaces, id)
	delete(r.byHash, rec.TokenHash)
	if err := r.saveLocked(); err != nil {
		return nil, err
	}
	return sp, os.RemoveAll(r.spaceDir(id))
}

// list describes every guest space, oldest first.
func (r *registry) list() []spaceInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]spaceInfo, 0, len(r.records))
	for id, rec := range r.records {
		sp := r.spaces[id]
		used, items := sp.store.Usage()
		out = append(out, spaceInfo{ID: id, Created: rec.Created, LastUsed: sp.lastUsed.Load(), UsedBytes: used, Items: items, LimitMB: sp.limit >> 20})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	return out
}

type spaceInfo struct {
	ID        string `json:"id"`
	Created   int64  `json:"created"`
	LastUsed  int64  `json:"last_used"`
	UsedBytes int64  `json:"used_bytes"`
	Items     int    `json:"items"`
	LimitMB   int64  `json:"limit_mb"`
}

// idle returns the guest spaces nobody has used since before cutoff.
func (r *registry) idle(cutoff int64) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for id, sp := range r.spaces {
		if sp.lastUsed.Load() < cutoff {
			ids = append(ids, id)
		}
	}
	return ids
}

func (r *registry) all() []*space {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*space, 0, len(r.spaces))
	for _, sp := range r.spaces {
		out = append(out, sp)
	}
	return out
}

func (r *registry) save() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.records) == 0 {
		if _, err := os.Stat(r.spacesPath()); os.IsNotExist(err) {
			return nil // nothing to write yet
		}
	}
	return r.saveLocked()
}

// ---- invites ----

func (r *registry) inviteIndexLocked(code string) int {
	now := time.Now().UnixMilli()
	code = normalizeCode(code)
	for i, inv := range r.invites {
		if inv.Code == code && (inv.Expires == 0 || inv.Expires > now) {
			return i
		}
	}
	return -1
}

func (r *registry) newInvite(valid time.Duration, mb int64) (invite, error) {
	code, err := newCode(12)
	if err != nil {
		return invite{}, err
	}
	now := time.Now()
	inv := invite{Code: code, Created: now.UnixMilli(), MB: mb}
	if valid > 0 {
		inv.Expires = now.Add(valid).UnixMilli()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropExpiredLocked()
	r.invites = append(r.invites, inv)
	return inv, r.saveInvitesLocked()
}

func (r *registry) listInvites() []invite {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropExpiredLocked()
	return append([]invite{}, r.invites...)
}

func (r *registry) revokeInvite(code string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.inviteIndexLocked(code)
	if i < 0 {
		return false
	}
	r.invites = append(r.invites[:i], r.invites[i+1:]...)
	r.saveInvitesLocked()
	return true
}

func (r *registry) dropExpiredLocked() {
	now := time.Now().UnixMilli()
	kept := r.invites[:0]
	for _, inv := range r.invites {
		if inv.Expires == 0 || inv.Expires > now {
			kept = append(kept, inv)
		}
	}
	r.invites = kept
}

// ---- codes and tokens ----

// An unambiguous alphabet: easy to read off a screen and type on a phone.
const codeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// newCode makes n random characters in groups of four or five.
func newCode(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	group := 4
	if n%5 == 0 {
		group = 5
	}
	var sb strings.Builder
	for i, b := range raw {
		if i > 0 && i%group == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(codeAlphabet[int(b)%len(codeAlphabet)])
	}
	return sb.String(), nil
}

// newToken makes an access token: 20 characters, about 99 bits.
func newToken() (string, error) { return newCode(20) }

// normalizeCode forgives how people type codes: case, spaces, missing dashes.
func normalizeCode(code string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(code) {
		if strings.ContainsRune(codeAlphabet, c) {
			b.WriteRune(c)
		}
	}
	s := b.String()
	if len(s) != 12 {
		return strings.ToLower(strings.TrimSpace(code))
	}
	return s[:4] + "-" + s[4:8] + "-" + s[8:]
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- making spaces in public mode: a few per address per day ----

type rateLimit struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

const (
	publicSpacesPerDay = 5
)

func (l *rateLimit) allow(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	recent := l.hits[addr][:0]
	for _, t := range l.hits[addr] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= publicSpacesPerDay {
		l.hits[addr] = recent
		return false
	}
	l.hits[addr] = append(recent, time.Now())
	// Forget addresses that have gone quiet, so the map doesn't grow forever.
	if len(l.hits) > 10000 {
		for a, ts := range l.hits {
			if len(ts) == 0 || ts[len(ts)-1].Before(cutoff) {
				delete(l.hits, a)
			}
		}
	}
	return true
}

// clientIP is the address a request came from. Behind a reverse proxy every
// request comes from the proxy, so with trustProxy the address the proxy
// adds to X-Forwarded-For (the last one) is used instead.
func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
