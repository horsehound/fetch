package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

//go:embed web/index.html
var indexHTML []byte

const maxText = 1 << 20 // text items above 1 MB are stored as files instead

const minTokenLen = 12

var errEmpty = errors.New("nothing to store: the upload was empty")

type server struct {
	store     *Store
	token     string
	maxUpload int64
	ttl       time.Duration

	mu   sync.Mutex
	subs map[chan struct{}]struct{}

	code analyser
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
	fmt.Printf("fetch hub %s\n", version)
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
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /api/info", s.auth(s.handleInfo))
	mux.HandleFunc("GET /api/items", s.auth(s.handleList))
	mux.HandleFunc("POST /api/items", s.auth(s.handleCreate))
	mux.HandleFunc("DELETE /api/items", s.auth(s.handleClear))
	mux.HandleFunc("GET /api/items/{id}/raw", s.auth(s.handleRaw))
	mux.HandleFunc("PUT /api/items/{id}/pin", s.auth(s.handlePin))
	mux.HandleFunc("DELETE /api/items/{id}", s.auth(s.handleDelete))
	mux.HandleFunc("GET /api/events", s.auth(s.handleEvents))
	return mux
}

// ---- auth ----

func (s *server) tokenOK(got string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			if c, err := r.Cookie("fetch_token"); err == nil {
				got = c.Value
			}
		}
		if !s.tokenOK(got) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	if !s.tokenOK(strings.TrimSpace(body.Token)) {
		time.Sleep(500 * time.Millisecond) // blunt guessing
		http.Error(w, "wrong token", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "fetch_token", Value: s.token, Path: "/",
		MaxAge: 365 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	w.WriteHeader(http.StatusNoContent)
}

// ---- handlers ----

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Write(indexHTML)
}

func (s *server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":   version,
		"ttl_hours": s.ttl.Hours(),
		"max_mb":    s.maxUpload >> 20,
	})
}

func (s *server) handleList(w http.ResponseWriter, r *http.Request) {
	items := s.store.List()
	s.code.annotate(items)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleCreate accepts three shapes:
//   - text/plain body                      -> text item
//   - any other body, optional ?name=      -> image or file item
//   - multipart/form-data ("file", "text") -> one item per part
//
// A body with no useful Content-Type and no name (what curl -d sends) becomes
// a text item when it is short valid UTF-8, and a file otherwise.
func (s *server) handleCreate(w http.ResponseWriter, r *http.Request) {
	device := deviceName(r)
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload)
	ct := baseType(r.Header.Get("Content-Type"))
	name := r.URL.Query().Get("name")

	var created []Item
	var err error
	switch {
	case ct == "multipart/form-data":
		created, err = s.createMultipart(r, device)
	case name == "" && (ct == "text/plain" || isVague(ct)):
		var it Item
		it, err = s.createMaybeText(r.Body, ct, device)
		created = []Item{it}
	default:
		var it Item
		it, err = s.store.AddFile(r.Body, name, ct, device)
		created = []Item{it}
	}
	if err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			http.Error(w, fmt.Sprintf("too large: the limit is %d MB", s.maxUpload>>20), http.StatusRequestEntityTooLarge)
		case errors.Is(err, errEmpty):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			log.Printf("upload failed: %v", err)
			http.Error(w, "could not store the upload", http.StatusInternalServerError)
		}
		return
	}
	s.broadcast()
	writeJSON(w, http.StatusCreated, map[string]any{"items": created})
}

func (s *server) createMaybeText(body io.Reader, ct, device string) (Item, error) {
	buf, err := io.ReadAll(io.LimitReader(body, maxText+1))
	if err != nil {
		return Item{}, err
	}
	fits := len(buf) <= maxText
	looksText := utf8.Valid(buf) && !bytes.ContainsRune(buf, 0)
	if fits && looksText {
		text := strings.ReplaceAll(string(buf), "\r\n", "\n")
		if ct != "text/plain" {
			// Piped command output (echo, wl-paste) ends in a newline nobody meant to send.
			text = strings.TrimSuffix(text, "\n")
		}
		if strings.TrimSpace(text) == "" {
			return Item{}, errEmpty
		}
		return s.store.AddText(text, device)
	}
	// Too long, or not text at all: keep it as a file.
	name := ""
	if ct == "text/plain" {
		name = "text-" + time.Now().Format("20060102-150405") + ".txt"
	}
	return s.store.AddFile(io.MultiReader(bytes.NewReader(buf), body), name, ct, device)
}

func (s *server) createMultipart(r *http.Request, device string) ([]Item, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, errEmpty
	}
	var out []Item
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		it, err := s.createPart(part, device)
		if errors.Is(err, errEmpty) {
			continue
		}
		if err != nil {
			return out, err
		}
		out = append(out, it)
	}
	if len(out) == 0 {
		return nil, errEmpty
	}
	return out, nil
}

func (s *server) createPart(part *multipart.Part, device string) (Item, error) {
	if part.FileName() == "" {
		return s.createMaybeText(part, "text/plain", device)
	}
	return s.store.AddFile(part, part.FileName(), part.Header.Get("Content-Type"), device)
}

func (s *server) handleRaw(w http.ResponseWriter, r *http.Request) {
	it, ok := s.store.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Uploaded content is never allowed to run as a page on this origin.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")

	if it.Kind == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, it.Text)
		return
	}
	f, err := os.Open(s.store.Path(it.ID))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	disposition := "attachment"
	if it.Kind == "image" && r.URL.Query().Get("dl") == "" {
		disposition = "inline"
	}
	if full := mime.FormatMediaType(disposition, map[string]string{"filename": it.Name}); full != "" {
		disposition = full
	}
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Type", it.Mime)
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

// deviceName reads the percent-encoded X-Device header clients send so the
// feed can show where an item came from.
func deviceName(r *http.Request) string {
	d := r.Header.Get("X-Device")
	if u, err := url.PathUnescape(d); err == nil {
		d = u
	}
	d = cleanName(d)
	if rs := []rune(d); len(rs) > 40 {
		d = string(rs[:40])
	}
	return d
}

func loadToken(dir, given string) (string, error) {
	if given = strings.TrimSpace(given); given != "" {
		// The token is the only thing between the internet and your feed.
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
