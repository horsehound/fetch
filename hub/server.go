package main

import (
	"crypto/rand"
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
	"strings"
	"sync"
	"time"
)

// The web page: index.html plus its scripts.
//
//go:embed web
var webFiles embed.FS

const minTokenLen = 12

type server struct {
	store     *Store
	token     string
	maxUpload int64
	ttl       time.Duration

	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", envOr("FETCH_ADDR", ":8787"), "listen address")
	dir := fs.String("data", envOr("FETCH_DATA", "./data"), "data directory")
	token := fs.String("token", os.Getenv("FETCH_TOKEN"), "access token (generated and saved if empty)")
	ttl := fs.Duration("ttl", envDuration("FETCH_TTL", 7*24*time.Hour), "delete unpinned items after this long (0 = keep forever)")
	maxMB := fs.Int64("max-mb", 500, "largest accepted upload in MB")
	cert := fs.String("cert", os.Getenv("FETCH_CERT"), "TLS certificate file; with -key, serves https directly")
	key := fs.String("key", os.Getenv("FETCH_KEY"), "TLS private key file")
	fs.Parse(args)
	if (*cert == "") != (*key == "") {
		log.Fatal("-cert and -key have to be given together")
	}

	store, err := OpenStore(*dir)
	if err != nil {
		log.Fatalf("cannot open data directory %q: %v", *dir, err)
	}
	tok, err := loadToken(*dir, *token)
	if err != nil {
		log.Fatalf("cannot set up access token: %v", err)
	}
	s := &server{
		store: store, token: tok, maxUpload: *maxMB << 20, ttl: *ttl,
		subs: map[chan struct{}]struct{}{},
	}
	if s.ttl > 0 {
		go s.janitor()
	}

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
	fmt.Printf("  token  %s\n", tok)
	if s.ttl > 0 {
		fmt.Printf("  unpinned items are deleted after %s\n", s.ttl)
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

func (s *server) tokenOK(got string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.tokenOK(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
			time.Sleep(300 * time.Millisecond) // blunt guessing
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// ---- handlers ----

func (s *server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":   version,
		"format":    formatVersion,
		"ttl_hours": s.ttl.Hours(),
		"max_mb":    s.maxUpload >> 20,
	})
}

// The vault is just the fingerprint of the key the devices share. It tells a
// new device whether it is the first (no fingerprint yet) and lets every
// device confirm it has the same key as the others.
func (s *server) handleVault(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"check": s.store.Check()})
}

var checkFormat = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (s *server) handleSetVault(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Check string `json:"check"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	if !checkFormat.MatchString(body.Check) {
		http.Error(w, "check must be 32 lowercase hex characters", http.StatusBadRequest)
		return
	}
	switch err := s.store.SetCheck(body.Check); {
	case errors.Is(err, errOtherKey):
		http.Error(w, "this hub already uses a different key; pair this device from one that has it", http.StatusConflict)
	case err != nil:
		log.Printf("saving the key fingerprint failed: %v", err)
		http.Error(w, "could not save the change", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) handleResetVault(w http.ResponseWriter, r *http.Request) {
	err := s.store.Reset()
	s.finishChange(w, r, true, err)
}

func (s *server) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": s.store.List()})
}

// handleCreate stores one encrypted item. The body is the length of the
// metadata envelope (4 bytes, big-endian), the envelope, then the encrypted
// data. See spec/encryption.md.
func (s *server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != sealedType {
		http.Error(w, "This hub only stores end-to-end encrypted items. Update your Fetch app.", http.StatusUnsupportedMediaType)
		return
	}
	body := http.MaxBytesReader(w, r.Body, 4+maxMeta+sealedSize(s.maxUpload))
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
	// The size limit is on what you send, so it counts the data alone.
	data := http.MaxBytesReader(w, io.NopCloser(body), sealedSize(s.maxUpload))
	it, err := s.store.Add(meta, data)
	if err != nil {
		s.uploadFailed(w, err)
		return
	}
	s.broadcast()
	writeJSON(w, http.StatusCreated, map[string]any{"item": it})
}

func (s *server) uploadFailed(w http.ResponseWriter, err error) {
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		http.Error(w, fmt.Sprintf("too large: the limit is %d MB", s.maxUpload>>20), http.StatusRequestEntityTooLarge)
	case errors.Is(err, errBadUpload), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		http.Error(w, errBadUpload.Error(), http.StatusBadRequest)
	default:
		log.Printf("upload failed: %v", err)
		http.Error(w, "could not store the upload", http.StatusInternalServerError)
	}
}

// handleRaw sends an item's encrypted data. Only a device with the key can
// make anything of it.
func (s *server) handleRaw(w http.ResponseWriter, r *http.Request) {
	it, ok := s.store.Get(r.PathValue("id"))
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
	f, err := os.Open(s.store.Path(it.ID))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	http.ServeContent(w, r, "", time.UnixMilli(it.Created), f)
}

func (s *server) handlePin(w http.ResponseWriter, r *http.Request) {
	on := r.URL.Query().Get("on") != "0"
	ok, err := s.store.SetPinned(r.PathValue("id"), on)
	s.finishChange(w, r, ok, err)
}

func (s *server) handleDelete(w http.ResponseWriter, r *http.Request) {
	ok, err := s.store.Delete(r.PathValue("id"))
	s.finishChange(w, r, ok, err)
}

func (s *server) handleClear(w http.ResponseWriter, r *http.Request) {
	_, err := s.store.Clear()
	s.finishChange(w, r, true, err)
}

func (s *server) finishChange(w http.ResponseWriter, r *http.Request, found bool, err error) {
	if err != nil {
		log.Printf("%s %s failed: %v", r.Method, r.URL.Path, err)
		http.Error(w, "could not save the change", http.StatusInternalServerError)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	s.broadcast()
	w.WriteHeader(http.StatusNoContent)
}

// ---- live updates (server-sent events) ----

func (s *server) broadcast() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default: // a refresh is already pending for this client
		}
	}
}

func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.subs, ch)
		s.mu.Unlock()
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
		case <-ch:
			fmt.Fprint(w, "data: changed\n\n")
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		}
		flusher.Flush()
	}
}

func (s *server) janitor() {
	for {
		if n, err := s.store.Expire(s.ttl); err != nil {
			log.Printf("expiry failed: %v", err)
		} else if n > 0 {
			s.broadcast()
		}
		time.Sleep(5 * time.Minute)
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
	// 20 characters from an unambiguous alphabet: easy to type on a phone once.
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, b := range raw {
		if i > 0 && i%5 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(alphabet[int(b)%len(alphabet)])
	}
	return sb.String(), os.WriteFile(path, []byte(sb.String()+"\n"), 0o600)
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
