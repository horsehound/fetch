package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A 1x1 PNG, enough for content sniffing.
var tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89")

type hub struct {
	t   *testing.T
	srv *httptest.Server
	s   *server
	dir string
}

func newHub(t *testing.T) *hub {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: store, token: "secret", maxUpload: 1 << 20, ttl: time.Hour, subs: map[chan struct{}]struct{}{}}
	srv := httptest.NewServer(s.routes())
	t.Cleanup(srv.Close)
	return &hub{t: t, srv: srv, s: s, dir: dir}
}

func (h *hub) do(method, path, contentType string, body []byte) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func (h *hub) create(path, contentType string, body []byte) Item {
	h.t.Helper()
	resp := h.do("POST", path, contentType, body)
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("POST %s: %s %s", path, resp.Status, msg)
	}
	var out struct{ Items []Item }
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Items) != 1 {
		h.t.Fatalf("POST %s: got %d items, want 1", path, len(out.Items))
	}
	return out.Items[0]
}

func (h *hub) list() []Item {
	h.t.Helper()
	var out struct{ Items []Item }
	json.NewDecoder(h.do("GET", "/api/items", "", nil).Body).Decode(&out)
	return out.Items
}

func TestRejectsMissingOrWrongToken(t *testing.T) {
	h := newHub(t)
	for _, auth := range []string{"", "Bearer nope"} {
		req, _ := http.NewRequest("GET", h.srv.URL+"/api/items", nil)
		req.Header.Set("Authorization", auth)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("auth %q: got %d, want 401", auth, resp.StatusCode)
		}
	}
}

func TestTextRoundTrip(t *testing.T) {
	h := newHub(t)
	it := h.create("/api/items", "text/plain; charset=utf-8", []byte("grüße\nline two"))
	if it.Kind != "text" || it.Text != "grüße\nline two" {
		t.Fatalf("got %+v", it)
	}
	raw, _ := io.ReadAll(h.do("GET", "/api/items/"+it.ID+"/raw", "", nil).Body)
	if string(raw) != "grüße\nline two" {
		t.Errorf("raw = %q", raw)
	}
}

func TestUntypedBodyBecomesTextOrFileBySniffing(t *testing.T) {
	h := newHub(t)
	// What `echo hi | curl --data-binary @-` sends: no useful type, trailing newline.
	if it := h.create("/api/items", "application/x-www-form-urlencoded", []byte("hi\n")); it.Kind != "text" || it.Text != "hi" {
		t.Errorf("piped text: got %+v", it)
	}
	if it := h.create("/api/items", "application/octet-stream", tinyPNG); it.Kind != "image" || it.Mime != "image/png" || !strings.HasSuffix(it.Name, ".png") {
		t.Errorf("piped png: got %+v", it)
	}
}

func TestImageIsStoredAndServedInline(t *testing.T) {
	h := newHub(t)
	it := h.create("/api/items?name=shot.png", "image/png", tinyPNG)
	if it.Kind != "image" || it.Name != "shot.png" || it.Size != int64(len(tinyPNG)) {
		t.Fatalf("got %+v", it)
	}
	resp := h.do("GET", "/api/items/"+it.ID+"/raw", "", nil)
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(body, tinyPNG) {
		t.Error("served bytes differ from the upload")
	}
	if d := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(d, "inline") {
		t.Errorf("disposition = %q, want inline", d)
	}
}

// Uploaded markup must never render as a page on the hub's own origin.
func TestActiveContentIsOnlyEverADownload(t *testing.T) {
	h := newHub(t)
	for name, mime := range map[string]string{"x.svg": "image/svg+xml", "x.html": "text/html"} {
		it := h.create("/api/items?name="+name, mime, []byte("<script>alert(1)</script>"))
		resp := h.do("GET", "/api/items/"+it.ID+"/raw", "", nil)
		if it.Kind != "file" || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
			t.Errorf("%s: kind=%s disposition=%q", name, it.Kind, resp.Header.Get("Content-Disposition"))
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
			t.Errorf("%s: missing hardening headers", name)
		}
	}
}

func TestNamesCannotEscapeTheDataDirectory(t *testing.T) {
	h := newHub(t)
	it := h.create("/api/items?name=..%2F..%2Fetc%2Fpasswd", "application/pdf", []byte("x"))
	if it.Name != "passwd" {
		t.Errorf("name = %q", it.Name)
	}
	if resp := h.do("GET", "/api/items/..%2F..%2Ftoken/raw", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("traversal id: got %d, want 404", resp.StatusCode)
	}
}

func TestRejectsEmptyAndOversizedUploads(t *testing.T) {
	h := newHub(t)
	if resp := h.do("POST", "/api/items", "text/plain", []byte("  \n ")); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("blank text: got %d, want 400", resp.StatusCode)
	}
	big := bytes.Repeat([]byte{0xff}, 1<<20+1)
	if resp := h.do("POST", "/api/items?name=big.bin", "application/octet-stream", big); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: got %d, want 413", resp.StatusCode)
	}
	if n := len(h.list()); n != 0 {
		t.Errorf("%d items stored after rejected uploads", n)
	}
	if left, _ := os.ReadDir(filepath.Join(h.dir, "files")); len(left) != 0 {
		t.Errorf("%d stray files left after rejected uploads", len(left))
	}
}

func TestPinnedItemsSurviveClearAndExpiry(t *testing.T) {
	h := newHub(t)
	keep := h.create("/api/items?name=keep.png", "image/png", tinyPNG)
	h.create("/api/items", "text/plain", []byte("temporary"))
	h.create("/api/items?name=temp.png", "image/png", tinyPNG)
	if resp := h.do("PUT", "/api/items/"+keep.ID+"/pin?on=1", "", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("pin: %s", resp.Status)
	}

	time.Sleep(5 * time.Millisecond)
	if n, err := h.s.store.Expire(time.Millisecond); err != nil || n != 2 {
		t.Fatalf("Expire removed %d (err %v), want 2", n, err)
	}
	items := h.list()
	if len(items) != 1 || items[0].ID != keep.ID || !items[0].Pinned {
		t.Fatalf("after expiry: %+v", items)
	}
	if left, _ := os.ReadDir(filepath.Join(h.dir, "files")); len(left) != 1 {
		t.Errorf("%d files on disk, want only the pinned one", len(left))
	}

	h.create("/api/items", "text/plain", []byte("again"))
	h.do("DELETE", "/api/items", "", nil)
	if items := h.list(); len(items) != 1 || items[0].ID != keep.ID {
		t.Errorf("after clear: %+v", items)
	}
}

func TestFeedSurvivesARestart(t *testing.T) {
	h := newHub(t)
	h.create("/api/items", "text/plain", []byte("remember me"))
	img := h.create("/api/items", "image/png", tinyPNG)

	reopened, err := OpenStore(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	items := reopened.List()
	if len(items) != 2 || items[0].ID != img.ID || items[1].Text != "remember me" {
		t.Fatalf("after reopen: %+v", items)
	}
}

func TestChangesReachLiveListeners(t *testing.T) {
	h := newHub(t)
	req, _ := http.NewRequest("GET", h.srv.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	lines := make(chan string, 8)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				lines <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	expect := func(want string) {
		t.Helper()
		select {
		case got := <-lines:
			if !strings.Contains(got, want) {
				t.Fatalf("stream sent %q, want %q", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no %q on the stream", want)
		}
	}
	expect("data: hello")
	h.create("/api/items", "text/plain", []byte("ping"))
	expect("data: changed")
}

func TestSchemeIsGuessedFromTheAddress(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.20:8787":         "http://192.168.1.20:8787",
		"10.0.0.5":                  "http://10.0.0.5",
		"proxmox:8787/":             "http://proxmox:8787",
		"hub.lan":                   "http://hub.lan",
		"fetch.example.com":         "https://fetch.example.com",
		"fetch.example.com/":        "https://fetch.example.com",
		"http://fetch.example.com":  "http://fetch.example.com",
		"https://192.168.1.20:8787": "https://192.168.1.20:8787",
	} {
		if got := normalizeURL(in); got != want {
			t.Errorf("normalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestShortTokensAreRefused(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadToken(dir, "hunter2"); err == nil {
		t.Error("a 7-character token was accepted")
	}
	if tok, err := loadToken(dir, "correct-horse-battery"); err != nil || tok != "correct-horse-battery" {
		t.Errorf("a long token was refused: %q, %v", tok, err)
	}
	generated, err := loadToken(dir, "")
	if err != nil || len(generated) < minTokenLen {
		t.Fatalf("generated token %q, err %v", generated, err)
	}
	if again, _ := loadToken(dir, ""); again != generated {
		t.Errorf("token changed across restarts: %q then %q", generated, again)
	}
}
