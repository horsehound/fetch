package main

import (
	"bytes"
	"encoding/binary"
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

type hub struct {
	t   *testing.T
	srv *httptest.Server
	s   *server
	dir string
	key Key
}

func newHub(t *testing.T) *hub {
	t.Helper()
	dir := t.TempDir()
	store, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: store, token: "secret-token-1234", maxUpload: 1 << 20, ttl: time.Hour, subs: map[chan struct{}]struct{}{}}
	srv := httptest.NewServer(s.routes())
	t.Cleanup(srv.Close)
	return &hub{t: t, srv: srv, s: s, dir: dir, key: testKey(5)}
}

func (h *hub) req(method, path, contentType string, body io.Reader) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, body)
	req.Header.Set("Authorization", "Bearer secret-token-1234")
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

// send encrypts an item the way a device does and uploads it.
func (h *hub) send(m Meta, data []byte) Item {
	h.t.Helper()
	var body bytes.Buffer
	var r io.Reader
	if data != nil {
		r = bytes.NewReader(data)
	}
	if err := h.key.WriteUpload(&body, m, r); err != nil {
		h.t.Fatal(err)
	}
	resp := h.req("POST", "/api/items", sealedType, &body)
	if resp.StatusCode != http.StatusCreated {
		msg, _ := io.ReadAll(resp.Body)
		h.t.Fatalf("upload: %s %s", resp.Status, msg)
	}
	var out struct{ Item Item }
	json.NewDecoder(resp.Body).Decode(&out)
	return out.Item
}

func (h *hub) list() []Item {
	h.t.Helper()
	var out struct{ Items []Item }
	json.NewDecoder(h.req("GET", "/api/items", "", nil).Body).Decode(&out)
	return out.Items
}

func TestRejectsMissingOrWrongToken(t *testing.T) {
	h := newHub(t)
	for _, auth := range []string{"", "Bearer nope", "secret-token-1234"} {
		req, _ := http.NewRequest("GET", h.srv.URL+"/api/items", nil)
		req.Header.Set("Authorization", auth)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if auth != "secret-token-1234" && resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("auth %q: got %d, want 401", auth, resp.StatusCode)
		}
	}
}

func TestItemsRoundTripAndTheHubCannotReadThem(t *testing.T) {
	h := newHub(t)
	secret := "sudo pacman -Syu  # my secret"
	picture := pattern(150000)
	h.send(Meta{Kind: "text", Text: secret, Device: "CachyOS"}, nil)
	h.send(Meta{Kind: "image", Name: "holiday.png", Mime: "image/png", Size: int64(len(picture))}, picture)

	items := h.list()
	if len(items) != 2 {
		t.Fatalf("%d items", len(items))
	}
	pic, text := items[0], items[1]
	if text.Size != 0 || pic.Size != sealedSize(int64(len(picture))) {
		t.Errorf("sizes: text %d, picture %d", text.Size, pic.Size)
	}

	// Nothing readable anywhere the hub keeps things.
	var everything []byte
	filepath.Walk(h.dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			b, _ := os.ReadFile(p)
			everything = append(everything, b...)
		}
		return nil
	})
	for _, plain := range []string{"pacman", "CachyOS", "holiday", "image/png"} {
		if bytes.Contains(everything, []byte(plain)) {
			t.Errorf("%q is readable in the hub's data directory", plain)
		}
	}
	if bytes.Contains(everything, picture[:64]) {
		t.Error("the picture is stored readably")
	}

	// A device with the key gets everything back.
	m, _, err := h.key.OpenMeta(text.Meta)
	if err != nil || m.Text != secret || m.Device != "CachyOS" {
		t.Fatalf("text meta: %+v %v", m, err)
	}
	pm, salt, err := h.key.OpenMeta(pic.Meta)
	if err != nil || pm.Name != "holiday.png" {
		t.Fatalf("picture meta: %+v %v", pm, err)
	}
	resp := h.req("GET", "/api/items/"+pic.ID+"/raw", "", nil)
	if resp.Header.Get("Content-Type") != "application/octet-stream" || !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Errorf("raw headers: %v", resp.Header)
	}
	got, err := io.ReadAll(h.key.OpenData(resp.Body, salt))
	if err != nil || !bytes.Equal(got, picture) {
		t.Fatalf("picture data: %v", err)
	}
	if b, _ := io.ReadAll(h.req("GET", "/api/items/"+text.ID+"/raw", "", nil).Body); len(b) != 0 {
		t.Errorf("a text item has %d bytes of data", len(b))
	}
}

func TestOnlyEncryptedUploadsAreAccepted(t *testing.T) {
	h := newHub(t)
	if resp := h.req("POST", "/api/items", "text/plain", strings.NewReader("hello")); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("plain text: got %d, want 415", resp.StatusCode)
	}
	frame := func(meta, data []byte) io.Reader {
		return bytes.NewReader(append(append(binary.BigEndian.AppendUint32(nil, uint32(len(meta))), meta...), data...))
	}
	env, _, _ := h.key.SealMeta(Meta{Kind: "file"})
	for name, body := range map[string]io.Reader{
		"empty":                      strings.NewReader(""),
		"a short length":             strings.NewReader("\x00\x00"),
		"a tiny envelope":            frame([]byte{1, 2, 3}, nil),
		"an envelope cut short":      bytes.NewReader(append(binary.BigEndian.AppendUint32(nil, uint32(len(env))), env[:10]...)),
		"an absurd length":           bytes.NewReader(binary.BigEndian.AppendUint32(nil, 1<<31)),
		"data of an impossible size": frame(env, make([]byte, 7)),
	} {
		if resp := h.req("POST", "/api/items", sealedType, body); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, resp.StatusCode)
		}
	}
	big := bytes.Repeat([]byte{1}, 2<<20)
	var body bytes.Buffer
	h.key.WriteUpload(&body, Meta{Kind: "file"}, bytes.NewReader(big))
	if resp := h.req("POST", "/api/items", sealedType, &body); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: got %d, want 413", resp.StatusCode)
	}
	if n := len(h.list()); n != 0 {
		t.Errorf("%d items stored after rejected uploads", n)
	}
	if left, _ := os.ReadDir(filepath.Join(h.dir, "files")); len(left) != 0 {
		t.Errorf("%d stray files left after rejected uploads", len(left))
	}
}

func TestVaultKeepsTheFirstKeyUntilReset(t *testing.T) {
	h := newHub(t)
	check := func() string {
		var v struct{ Check string }
		json.NewDecoder(h.req("GET", "/api/vault", "", nil).Body).Decode(&v)
		return v.Check
	}
	put := func(c string) int {
		return h.req("PUT", "/api/vault", "application/json", strings.NewReader(`{"check":"`+c+`"}`)).StatusCode
	}
	if check() != "" {
		t.Fatal("a new hub has a key check")
	}
	if code := put("not-hex"); code != http.StatusBadRequest {
		t.Errorf("bad check: %d", code)
	}
	first, second := testKey(1).Check(), testKey(2).Check()
	if put(first) != http.StatusNoContent || put(first) != http.StatusNoContent {
		t.Error("setting the first check failed")
	}
	if put(second) != http.StatusConflict || check() != first {
		t.Error("a second key replaced the first")
	}

	// It survives a restart.
	reopened, _ := OpenStore(h.dir)
	if reopened.Check() != first {
		t.Error("the check was lost on restart")
	}

	// Reset deletes everything, pinned too, and frees the hub for a new key.
	it := h.send(Meta{Kind: "text", Text: "keep me"}, nil)
	h.send(Meta{Kind: "file", Name: "x"}, pattern(5000))
	h.req("PUT", "/api/items/"+it.ID+"/pin?on=1", "", nil)
	if code := h.req("DELETE", "/api/vault", "", nil).StatusCode; code != http.StatusNoContent {
		t.Fatalf("reset: %d", code)
	}
	if check() != "" || len(h.list()) != 0 {
		t.Error("reset left a check or items behind")
	}
	if left, _ := os.ReadDir(filepath.Join(h.dir, "files")); len(left) != 0 {
		t.Errorf("reset left %d files", len(left))
	}
	if put(second) != http.StatusNoContent {
		t.Error("a new key couldn't be set after reset")
	}
}

func TestItemsFromBeforeEncryptionAreDeleted(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "files"), 0o700)
	os.WriteFile(filepath.Join(dir, "files", "old1"), []byte("PNG..."), 0o600)
	k := testKey(1)
	env, _, _ := k.SealMeta(Meta{Kind: "text", Text: "new"})
	legacy := `[{"id":"new1","meta":"` + jsonBase64(env) + `","size":0,"created":1,"pinned":false},` +
		`{"id":"old1","kind":"image","name":"a.png","size":6,"created":1,"pinned":true},` +
		`{"id":"old2","kind":"text","text":"plain","size":5,"created":1,"pinned":false}]`
	os.WriteFile(filepath.Join(dir, "index.json"), []byte(legacy), 0o600)

	s, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	items := s.List()
	if len(items) != 1 || items[0].ID != "new1" {
		t.Fatalf("kept %+v", items)
	}
	if _, err := os.Stat(filepath.Join(dir, "files", "old1")); !os.IsNotExist(err) {
		t.Error("the old item's file is still there")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "index.json"))
	if bytes.Contains(b, []byte("plain")) {
		t.Error("old text is still in the index")
	}
}

func jsonBase64(b []byte) string {
	s, _ := json.Marshal(b)
	return strings.Trim(string(s), `"`)
}

func TestPinnedItemsSurviveClearAndExpiry(t *testing.T) {
	h := newHub(t)
	keep := h.send(Meta{Kind: "image", Name: "keep.png"}, pattern(100))
	h.send(Meta{Kind: "text", Text: "temporary"}, nil)
	h.send(Meta{Kind: "file", Name: "temp.bin"}, pattern(100))
	if resp := h.req("PUT", "/api/items/"+keep.ID+"/pin?on=1", "", nil); resp.StatusCode != http.StatusNoContent {
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
	h.send(Meta{Kind: "text", Text: "again"}, nil)
	h.req("DELETE", "/api/items", "", nil)
	if items := h.list(); len(items) != 1 || items[0].ID != keep.ID {
		t.Errorf("after clear: %+v", items)
	}
	if resp := h.req("DELETE", "/api/items/"+keep.ID, "", nil); resp.StatusCode != http.StatusNoContent || len(h.list()) != 0 {
		t.Error("delete failed")
	}
}

func TestFeedSurvivesARestart(t *testing.T) {
	h := newHub(t)
	a := h.send(Meta{Kind: "text", Text: "remember me"}, nil)
	b := h.send(Meta{Kind: "file", Name: "f"}, pattern(70000))
	reopened, err := OpenStore(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	items := reopened.List()
	if len(items) != 2 || items[0].ID != b.ID || items[1].ID != a.ID || !bytes.Equal(items[1].Meta, a.Meta) {
		t.Fatalf("after reopen: %+v", items)
	}
}

func TestTheDesktopPageMayCallTheAPI(t *testing.T) {
	h := newHub(t)
	req, _ := http.NewRequest("OPTIONS", h.srv.URL+"/api/items", nil)
	req.Header.Set("Origin", "null")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "authorization, content-type")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "*" ||
		!strings.Contains(resp.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Errorf("preflight: %d %v", resp.StatusCode, resp.Header)
	}
}

func TestThePageIsServedWithAStrictPolicy(t *testing.T) {
	h := newHub(t)
	resp, err := http.Get(h.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	csp := resp.Header.Get("Content-Security-Policy")
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte("<html")) {
		t.Fatalf("index: %d", resp.StatusCode)
	}
	for _, want := range []string{"script-src 'self'", "connect-src 'self'", "default-src 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("policy lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("policy allows inline or eval'd script: %s", csp)
	}
}

func TestChangesReachLiveListeners(t *testing.T) {
	h := newHub(t)
	req, _ := http.NewRequest("GET", h.srv.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer secret-token-1234")
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
	h.send(Meta{Kind: "text", Text: "ping"}, nil)
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
	generated, err := loadToken(dir, "")
	if err != nil || len(generated) < minTokenLen {
		t.Fatalf("generated token %q, err %v", generated, err)
	}
	if again, _ := loadToken(dir, ""); again != generated {
		t.Errorf("token changed across restarts: %q then %q", generated, again)
	}
}
