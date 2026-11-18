package main

import (
	"crypto/subtle"
	"embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The web page: index.html plus its scripts.
//
//go:embed web
var webFiles embed.FS

const minTokenLen = 12

type server struct {
	opts       options
	ownerToken string
	owner      *space // the host's own feed
	reg        *registry
	newSpaces  rateLimit
}

type options struct {
	dir        string
	token      string // the host's token; generated and saved if empty
	maxUpload  int64
	ttl        time.Duration
	mode       string        // who may make a space: private, invite or public
	spaceMB    int64         // storage limit for each guest space; 0 = none
	maxSpaces  int           // most guest spaces; 0 = no limit
	idle       time.Duration // delete guest spaces unused this long; 0 = never
	trustProxy bool          // take the client's address from X-Forwarded-For
}

func newServer(o options) (*server, error) {
	store, err := OpenStore(o.dir)
	if err != nil {
		return nil, fmt.Errorf("cannot open data directory %q: %w", o.dir, err)
	}
	tok, err := loadToken(o.dir, o.token)
	if err != nil {
		return nil, fmt.Errorf("cannot set up access token: %w", err)
	}
	s := &server{opts: o, ownerToken: tok, owner: newSpace("", true, store, 0)}
	if s.reg, err = openRegistry(o.dir, s.limitFor); err != nil {
		return nil, err
	}
	return s, nil
}

// limitFor turns a space's own limit (0 = the hub's default) into bytes.
func (s *server) limitFor(mb int64) int64 {
	if mb > 0 {
		return mb << 20
	}
	return s.opts.spaceMB << 20
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", envOr("FETCH_ADDR", ":8787"), "listen address")
	dir := fs.String("data", envOr("FETCH_DATA", "./data"), "data directory")
	token := fs.String("token", os.Getenv("FETCH_TOKEN"), "your own access token (generated and saved if empty)")
	ttl := fs.Duration("ttl", envDuration("FETCH_TTL", 7*24*time.Hour), "delete unpinned items after this long (0 = keep forever)")
	maxMB := fs.Int64("max-mb", envInt("FETCH_MAX_MB", 500), "largest accepted upload in MB")
	mode := fs.String("mode", envOr("FETCH_MODE", modeInvite), "who may make their own space here: private, invite (with a code from `fetch invite`) or public")
	spaceMB := fs.Int64("space-mb", envInt("FETCH_SPACE_MB", 500), "storage for each space you hand out, in MB (0 = no limit)")
	maxSpaces := fs.Int("max-spaces", int(envInt("FETCH_MAX_SPACES", 50)), "most spaces this hub holds besides your own (0 = no limit)")
	idle := fs.Duration("space-idle", envDuration("FETCH_SPACE_IDLE", 90*24*time.Hour), "delete a space nobody has used this long (0 = never; never applies to your own)")
	trustProxy := fs.Bool("trust-proxy", envBool("FETCH_TRUST_PROXY"), "behind a reverse proxy: take visitors' addresses from X-Forwarded-For")
	cert := fs.String("cert", os.Getenv("FETCH_CERT"), "TLS certificate file; with -key, serves https directly")
	key := fs.String("key", os.Getenv("FETCH_KEY"), "TLS private key file")
	fs.Parse(args)
	if (*cert == "") != (*key == "") {
		log.Fatal("-cert and -key have to be given together")
	}
	switch *mode {
	case modePrivate, modeInvite, modePublic:
	default:
		log.Fatalf("-mode must be private, invite or public, not %q", *mode)
	}

	s, err := newServer(options{
		dir: *dir, token: *token, maxUpload: *maxMB << 20, ttl: *ttl,
		mode: *mode, spaceMB: *spaceMB, maxSpaces: *maxSpaces, idle: *idle, trustProxy: *trustProxy,
	})
	if err != nil {
		log.Fatal(err)
	}
	go s.janitor()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("cannot listen on %s: %v", *addr, err)
	}
	fmt.Printf("fetch hub %s (end-to-end encrypted: this hub can't read what it stores)\n", version)
	scheme := "http"
	if *cert != "" {
		scheme = "https"
	}
	for _, u := range localURLs(scheme, ln.Addr()) {
		fmt.Printf("  open   %s\n", u)
	}
	fmt.Printf("  token  %s\n", s.ownerToken)
	if s.opts.ttl > 0 {
		fmt.Printf("  unpinned items are deleted after %s\n", s.opts.ttl)
	}
	limit := "no storage limit"
	if s.opts.spaceMB > 0 {
		limit = fmt.Sprintf("%d MB each", s.opts.spaceMB)
	}
	switch s.opts.mode {
	case modePrivate:
		fmt.Println("  spaces private: only your own feed")
	case modeInvite:
		fmt.Printf("  spaces by invite (%s, %d in use): make a code with `fetch invite` or from the web page\n", limit, s.reg.count())
	case modePublic:
		fmt.Printf("  spaces public: anyone can make one (%s, %d in use)\n", limit, s.reg.count())
	}
	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 15 * time.Second}
	if *cert != "" {
		log.Fatal(srv.ServeTLS(ln, *cert, *key))
	}
	log.Fatal(srv.Serve(ln))
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", s.webHandler())
	mux.HandleFunc("OPTIONS /api/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /api/hub", s.handleHub)
	mux.HandleFunc("POST /api/spaces", s.handleNewSpace)
	mux.HandleFunc("GET /api/spaces", s.ownerOnly(s.handleListSpaces))
	mux.HandleFunc("DELETE /api/spaces/{id}", s.ownerOnly(s.handleDeleteSpace))
	mux.HandleFunc("DELETE /api/space", s.auth(s.handleLeave))
	mux.HandleFunc("POST /api/invites", s.ownerOnly(s.handleNewInvite))
	mux.HandleFunc("GET /api/invites", s.ownerOnly(s.handleListInvites))
	mux.HandleFunc("DELETE /api/invites/{code}", s.ownerOnly(s.handleRevokeInvite))
	mux.HandleFunc("GET /api/info", s.auth(s.handleInfo))
	mux.HandleFunc("GET /api/vault", s.auth(s.handleVault))
	mux.HandleFunc("PUT /api/vault", s.auth(s.handleSetVault))
	mux.HandleFunc("DELETE /api/vault", s.auth(s.handleResetVault))
	mux.HandleFunc("GET /api/items", s.auth(s.handleList))
	mux.HandleFunc("POST /api/items", s.auth(s.handleCreate))
	mux.HandleFunc("DELETE /api/items", s.auth(s.handleClear))
	mux.HandleFunc("GET /api/items/{id}/raw", s.auth(s.handleRaw))
	mux.HandleFunc("PUT /api/items/{id}/pin", s.auth(s.handlePin))
	mux.HandleFunc("DELETE /api/items/{id}", s.auth(s.handleDelete))
	mux.HandleFunc("GET /api/events", s.auth(s.handleEvents))
	return cors(mux)
}

// cors lets the desktop app's built-in page talk to the hub. Access needs the
// token in the Authorization header, never a cookie, so allowing any origin
// opens nothing up.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE")
			h.Set("Access-Control-Max-Age", "600")
		}
		next.ServeHTTP(w, r)
	})
}

// webHandler serves the page. Its scripts hold the key once a browser is
// paired, so the policy keeps them from loading code from anywhere else or
// sending anything to any other address.
func (s *server) webHandler() http.Handler {
	sub, _ := fs.Sub(webFiles, "web")
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-cache")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' blob: data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		files.ServeHTTP(w, r)
	})
}

// ---- auth ----

// A handler that works inside the space the request's token opens.
type spaceHandler func(http.ResponseWriter, *http.Request, *space)

// spaceFor finds the space a token opens: the host's own, or a guest's.
func (s *server) spaceFor(token string) *space {
	if token == "" {
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.ownerToken)) == 1 {
		return s.owner
	}
	return s.reg.find(token)
}

func (s *server) auth(next spaceHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sp := s.spaceFor(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if sp == nil {
			time.Sleep(300 * time.Millisecond) // blunt guessing
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		sp.lastUsed.Store(time.Now().UnixMilli())
		next(w, r, sp)
	}
}

// ownerOnly is for running the hub: handing out invites, looking after spaces.
func (s *server) ownerOnly(next spaceHandler) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request, sp *space) {
		if !sp.owner {
			http.Error(w, "only whoever runs this hub can do that", http.StatusForbidden)
			return
		}
		next(w, r, sp)
	})
}

// ---- handlers ----

func (s *server) handleInfo(w http.ResponseWriter, r *http.Request, sp *space) {
	used, _ := sp.store.Usage()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    version,
		"format":     formatVersion,
		"ttl_hours":  s.opts.ttl.Hours(),
		"max_mb":     s.opts.maxUpload >> 20,
		"owner":      sp.owner,
		"mode":       s.opts.mode,
		"limit_mb":   sp.limit >> 20,
		"used_bytes": used,
	})
}

// The vault is just the fingerprint of the key the devices share. It tells a
// new device whether it is the first (no fingerprint yet) and lets every
// device confirm it has the same key as the others.
func (s *server) handleVault(w http.ResponseWriter, r *http.Request, sp *space) {
	writeJSON(w, http.StatusOK, map[string]any{"check": sp.store.Check()})
}

var checkFormat = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (s *server) handleSetVault(w http.ResponseWriter, r *http.Request, sp *space) {
	var body struct {
		Check string `json:"check"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	if !checkFormat.MatchString(body.Check) {
		http.Error(w, "check must be 32 lowercase hex characters", http.StatusBadRequest)
		return
	}
	switch err := sp.store.SetCheck(body.Check); {
	case errors.Is(err, errOtherKey):
		http.Error(w, "this hub already uses a different key; pair this device from one that has it", http.StatusConflict)
	case err != nil:
		log.Printf("saving the key fingerprint failed: %v", err)
		http.Error(w, "could not save the change", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) handleResetVault(w http.ResponseWriter, r *http.Request, sp *space) {
	err := sp.store.Reset()
	s.finishChange(w, r, sp, true, err)
}

func (s *server) handleList(w http.ResponseWriter, r *http.Request, sp *space) {
	writeJSON(w, http.StatusOK, map[string]any{"items": sp.store.List()})
}

// handleCreate stores one encrypted item. The body is the length of the
// metadata envelope (4 bytes, big-endian), the envelope, then the encrypted
// data. See spec/encryption.md.
func (s *server) handleCreate(w http.ResponseWriter, r *http.Request, sp *space) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != sealedType {
		http.Error(w, "This hub only stores end-to-end encrypted items. Update your Fetch app.", http.StatusUnsupportedMediaType)
		return
	}
	body := http.MaxBytesReader(w, r.Body, 4+maxMeta+sealedSize(s.opts.maxUpload))
	var head [4]byte
	if _, err := io.ReadFull(body, head[:]); err != nil {
		s.uploadFailed(w, err)
		return
	}
	n := binary.BigEndian.Uint32(head[:])
	if n < metaOverhead || n > maxMeta {
		s.uploadFailed(w, errBadUpload)
		return
	}
	meta := make([]byte, n)
	if _, err := io.ReadFull(body, meta); err != nil {
		s.uploadFailed(w, err)
		return
	}
	// The size limit is on what you send, so it counts the data alone. A
	// space with a storage limit may have less room left than that.
	limit, full := sealedSize(s.opts.maxUpload), false
	if sp.limit > 0 {
		used, _ := sp.store.Usage()
		if room := sp.limit - used - int64(len(meta)); room < limit {
			limit, full = room, true
		}
		if limit < 0 {
			s.spaceFull(w, sp)
			return
		}
	}
	data := http.MaxBytesReader(w, io.NopCloser(body), limit)
	it, err := sp.store.Add(meta, data)
	var tooBig *http.MaxBytesError
	if full && errors.As(err, &tooBig) {
		s.spaceFull(w, sp)
		return
	}
	if err != nil {
		s.uploadFailed(w, err)
		return
	}
	sp.broadcast()
	writeJSON(w, http.StatusCreated, map[string]any{"item": it})
}

func (s *server) spaceFull(w http.ResponseWriter, sp *space) {
	http.Error(w, fmt.Sprintf("Your space on this hub is full (%d MB). Delete something, or unpin it so it can expire.", sp.limit>>20), http.StatusInsufficientStorage)
}

func (s *server) uploadFailed(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		http.Error(w, fmt.Sprintf("too large: the limit is %d MB", s.opts.maxUpload>>20), http.StatusRequestEntityTooLarge)
	case errors.Is(err, errBadUpload), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		http.Error(w, errBadUpload.Error(), http.StatusBadRequest)
	default:
		log.Printf("upload failed: %v", err)
		http.Error(w, "could not store the upload", http.StatusInternalServerError)
	}
}

// handleRaw sends an item's encrypted data. Only a device with the key can
// make anything of it.
func (s *server) handleRaw(w http.ResponseWriter, r *http.Request, sp *space) {
	it, ok := sp.store.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if it.Size == 0 {
		w.Header().Set("Content-Length", "0")
		return
	}
	f, err := os.Open(sp.store.Path(it.ID))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	http.ServeContent(w, r, "", time.UnixMilli(it.Created), f)
}

func (s *server) handlePin(w http.ResponseWriter, r *http.Request, sp *space) {
	on := r.URL.Query().Get("on") != "0"
	ok, err := sp.store.SetPinned(r.PathValue("id"), on)
	s.finishChange(w, r, sp, ok, err)
}

func (s *server) handleDelete(w http.ResponseWriter, r *http.Request, sp *space) {
	ok, err := sp.store.Delete(r.PathValue("id"))
	s.finishChange(w, r, sp, ok, err)
}

func (s *server) handleClear(w http.ResponseWriter, r *http.Request, sp *space) {
	_, err := sp.store.Clear()
	s.finishChange(w, r, sp, true, err)
}

func (s *server) finishChange(w http.ResponseWriter, r *http.Request, sp *space, found bool, err error) {
	if err != nil {
		log.Printf("%s %s failed: %v", r.Method, r.URL.Path, err)
		http.Error(w, "could not save the change", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	sp.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

// ---- live updates (server-sent events) ----

func (sp *space) broadcast() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	for ch := range sp.subs {
		select {
		case ch <- struct{}{}:
		default: // a refresh is already pending for this client
		}
	}
}

// close ends every live connection to a space that was just deleted; the
// devices reconnect, find their token refused, and say so.
func (sp *space) close() {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	for ch := range sp.subs {
		close(ch)
		delete(sp.subs, ch)
	}
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request, sp *space) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := make(chan struct{}, 1)
	sp.mu.Lock()
	sp.subs[ch] = struct{}{}
	sp.mu.Unlock()
	defer func() {
		sp.mu.Lock()
		delete(sp.subs, ch)
		sp.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // keep nginx from buffering the stream
	fmt.Fprint(w, "retry: 2000\ndata: hello\n\n")
	flusher.Flush()

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case _, open := <-ch:
			if !open {
				return
			}
			fmt.Fprint(w, "data: changed\n\n")
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		flusher.Flush()
	}
}

// janitor deletes expired items and spaces nobody uses any more, and notes
// when each space was last used.
func (s *server) janitor() {
	for {
		s.tidy()
		time.Sleep(5 * time.Minute)
	}
}

func (s *server) tidy() {
	if s.opts.idle > 0 {
		cutoff := time.Now().Add(-s.opts.idle).UnixMilli()
		for _, id := range s.reg.idle(cutoff) {
			if sp, err := s.reg.remove(id); err != nil {
				log.Printf("deleting unused space %s failed: %v", id, err)
			} else {
				sp.close()
				log.Printf("deleted space %s: unused for %s", id, s.opts.idle)
			}
		}
	}
	if s.opts.ttl > 0 {
		for _, sp := range append(s.reg.all(), s.owner) {
			if n, err := sp.store.Expire(s.opts.ttl); err != nil {
				log.Printf("expiry failed: %v", err)
			} else if n > 0 {
				sp.broadcast()
			}
		}
	}
	if err := s.reg.save(); err != nil {
		log.Printf("saving spaces.json failed: %v", err)
	}
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func loadToken(dir, given string) (string, error) {
	if given = strings.TrimSpace(given); given != "" {
		// The token is what stands between the internet and your hub.
		if len(given) < minTokenLen {
			return "", fmt.Errorf("the token needs at least %d characters; leave it empty to have one generated", minTokenLen)
		}
		return given, nil
	}
	path := filepath.Join(dir, "token")
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, nil
		}
	}
	tok, err := newToken()
	if err != nil {
		return "", err
	}
	return tok, os.WriteFile(path, []byte(tok+"\n"), 0o600)
}

func localURLs(scheme string, addr net.Addr) []string {
	host, port, _ := net.SplitHostPort(addr.String())
	scheme += "://"
	urls := []string{scheme + "localhost:" + port}
	if ip := net.ParseIP(host); ip != nil && !ip.IsUnspecified() {
		if !ip.IsLoopback() {
			urls = []string{scheme + net.JoinHostPort(host, port)}
		}
		return urls
	}
	ifaces, _ := net.InterfaceAddrs()
	for _, a := range ifaces {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			urls = append(urls, scheme+ipn.IP.String()+":"+port)
		}
	}
	return urls
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		log.Fatalf("%s=%q is not a whole number", key, v)
	}
	return n
}

func envBool(key string) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Fatalf("%s=%q is not a duration (try 168h, or 0 to keep everything)", key, v)
	}
	return d
}
