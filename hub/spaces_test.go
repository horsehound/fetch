package main

import (
	"bufio"
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

const ownerToken = "secret-token-1234"

// as sends a request with the given token ("" for none).
func (h *hub) as(token, method, path, contentType string, body io.Reader) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
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

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func wantStatus(t *testing.T, resp *http.Response, want int) string {
	t.Helper()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: got %s (%s), want %d", resp.Request.Method, resp.Request.URL.Path, resp.Status, strings.TrimSpace(string(body)), want)
	}
	return string(body)
}

// invite makes an invite code as the host.
func (h *hub) invite(mb int64) string {
	h.t.Helper()
	resp := h.as(ownerToken, "POST", "/api/invites", "application/json", jsonBody(map[string]any{"mb": mb}))
	var out struct{ Code string }
	json.Unmarshal([]byte(wantStatus(h.t, resp, http.StatusCreated)), &out)
	if out.Code == "" {
		h.t.Fatal("no invite code")
	}
	return out.Code
}

// join makes a space (with an invite code, or none on a public hub) and
// returns its token.
func (h *hub) join(code string) string {
	h.t.Helper()
	resp := h.as("", "POST", "/api/spaces", "application/json", jsonBody(map[string]string{"invite": code}))
	var out struct{ Token string }
	json.Unmarshal([]byte(wantStatus(h.t, resp, http.StatusCreated)), &out)
	if len(out.Token) < minTokenLen {
		h.t.Fatalf("token %q is too short", out.Token)
	}
	return out.Token
}

// sendAs uploads an encrypted item with a token and key.
func (h *hub) sendAs(token string, key Key, m Meta, data []byte) *http.Response {
	h.t.Helper()
	var body bytes.Buffer
	var r io.Reader
	if data != nil {
		r = bytes.NewReader(data)
	}
	if err := key.WriteUpload(&body, m, r); err != nil {
		h.t.Fatal(err)
	}
	return h.as(token, "POST", "/api/items", sealedType, &body)
}

func (h *hub) itemsOf(token string) []Item {
	h.t.Helper()
	var out struct{ Items []Item }
	json.Unmarshal([]byte(wantStatus(h.t, h.as(token, "GET", "/api/items", "", nil), http.StatusOK)), &out)
	return out.Items
}

func TestTheHubSaysWhatKindItIsWithoutAToken(t *testing.T) {
	h := newHub(t)
	var info struct {
		Fetch   bool
		Mode    string
		SpaceMB int64 `json:"space_mb"`
	}
	json.Unmarshal([]byte(wantStatus(t, h.as("", "GET", "/api/hub", "", nil), http.StatusOK)), &info)
	if !info.Fetch || info.Mode != modeInvite || info.SpaceMB != 500 {
		t.Fatalf("got %+v", info)
	}
}

func TestAnInviteMakesASeparateSpace(t *testing.T) {
	h := newHub(t)

	// No code, no space.
	wantStatus(t, h.as("", "POST", "/api/spaces", "application/json", jsonBody(map[string]string{})), http.StatusForbidden)
	wantStatus(t, h.as("", "POST", "/api/spaces", "application/json", jsonBody(map[string]string{"invite": "aaaa-bbbb-cccc"})), http.StatusForbidden)

	code := h.invite(0)
	guest := h.join(strings.ToUpper(strings.ReplaceAll(code, "-", " "))) // typed sloppily
	// A code works once.
	wantStatus(t, h.as("", "POST", "/api/spaces", "application/json", jsonBody(map[string]string{"invite": code})), http.StatusForbidden)

	// The new space starts empty, with no key yet: the guest's first device makes one.
	var vault struct{ Check string }
	json.Unmarshal([]byte(wantStatus(t, h.as(guest, "GET", "/api/vault", "", nil), http.StatusOK)), &vault)
	if vault.Check != "" {
		t.Fatalf("new space already has a key check %q", vault.Check)
	}
	guestKey := testKey(9)
	wantStatus(t, h.as(guest, "PUT", "/api/vault", "application/json", jsonBody(map[string]string{"check": guestKey.Check()})), http.StatusNoContent)

	// Each space only sees its own items.
	h.send(Meta{Kind: "text", Text: "the host's"}, nil)
	wantStatus(t, h.sendAs(guest, guestKey, Meta{Kind: "text", Text: "the guest's"}, nil), http.StatusCreated)
	wantStatus(t, h.sendAs(guest, guestKey, Meta{Kind: "file", Name: "a.bin"}, bytes.Repeat([]byte{7}, 70000)), http.StatusCreated)
	own, theirs := h.itemsOf(ownerToken), h.itemsOf(guest)
	if len(own) != 1 || len(theirs) != 2 {
		t.Fatalf("host sees %d items, guest %d; want 1 and 2", len(own), len(theirs))
	}
	if m, _, err := guestKey.OpenMeta(theirs[1].Meta); err != nil || m.Text != "the guest's" {
		t.Fatalf("guest's own item: %+v %v", m, err)
	}
	// An item from one space can't be fetched, pinned or deleted with another's token.
	wantStatus(t, h.as(ownerToken, "GET", "/api/items/"+theirs[0].ID+"/raw", "", nil), http.StatusNotFound)
	wantStatus(t, h.as(guest, "DELETE", "/api/items/"+own[0].ID, "", nil), http.StatusNotFound)
	wantStatus(t, h.as(guest, "PUT", "/api/items/"+own[0].ID+"/pin", "", nil), http.StatusNotFound)
	// Clearing or starting over only touches your own space.
	wantStatus(t, h.as(guest, "DELETE", "/api/vault", "", nil), http.StatusNoContent)
	if len(h.itemsOf(ownerToken)) != 1 {
		t.Fatal("a guest starting over emptied the host's feed")
	}

	// Guests can't run the hub.
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/spaces"}, {"POST", "/api/invites"}, {"GET", "/api/invites"}, {"DELETE", "/api/spaces/x"},
	} {
		wantStatus(t, h.as(guest, c.method, c.path, "application/json", jsonBody(map[string]any{})), http.StatusForbidden)
	}

	// The host sees the space, but not what's in it.
	var list struct{ Spaces []spaceInfo }
	json.Unmarshal([]byte(wantStatus(t, h.as(ownerToken, "GET", "/api/spaces", "", nil), http.StatusOK)), &list)
	if len(list.Spaces) != 1 || list.Spaces[0].LimitMB != 500 {
		t.Fatalf("spaces: %+v", list.Spaces)
	}

	// Only a hash of the token is kept on disk.
	b, _ := os.ReadFile(filepath.Join(h.dir, "spaces.json"))
	if bytes.Contains(b, []byte(guest)) || !bytes.Contains(b, []byte(hashToken(guest))) {
		t.Fatalf("spaces.json should hold the token's hash only: %s", b)
	}
}

func TestPrivateHubsMakeNoSpaces(t *testing.T) {
	h := newHubWith(t, options{mode: modePrivate})
	wantStatus(t, h.as("", "POST", "/api/spaces", "application/json", jsonBody(map[string]string{})), http.StatusForbidden)
	wantStatus(t, h.as(ownerToken, "POST", "/api/invites", "application/json", jsonBody(map[string]any{})), http.StatusConflict)
}

func TestPublicHubsLetAnyoneInAFewTimesADay(t *testing.T) {
	h := newHubWith(t, options{mode: modePublic, spaceMB: 100})
	seen := map[string]bool{}
	for i := 0; i < publicSpacesPerDay; i++ {
		tok := h.join("")
		if seen[tok] {
			t.Fatal("two spaces got the same token")
		}
		seen[tok] = true
	}
	wantStatus(t, h.as("", "POST", "/api/spaces", "application/json", jsonBody(map[string]string{})), http.StatusTooManyRequests)
	// An invite still works when the address has used up its free ones.
	h.join(h.invite(0))
}

func TestTheProxysAddressIsOnlyTrustedWhenAsked(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.2:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if got := clientIP(r, false); got != "10.0.0.2" {
		t.Fatalf("untrusted: %s", got)
	}
	if got := clientIP(r, true); got != "203.0.113.9" {
		t.Fatalf("trusted: %s", got)
	}
}

func TestAHubTakesNoMoreSpacesThanItsLimit(t *testing.T) {
	h := newHubWith(t, options{mode: modeInvite, maxSpaces: 1})
	h.join(h.invite(0))
	code := h.invite(0)
	wantStatus(t, h.as("", "POST", "/api/spaces", "application/json", jsonBody(map[string]string{"invite": code})), http.StatusServiceUnavailable)
	// The code wasn't used up by the failed attempt.
	var inv struct{ Invites []struct{ Code string } }
	json.Unmarshal([]byte(wantStatus(t, h.as(ownerToken, "GET", "/api/invites", "", nil), http.StatusOK)), &inv)
	if len(inv.Invites) != 1 || inv.Invites[0].Code != code {
		t.Fatalf("invites: %+v", inv.Invites)
	}
	wantStatus(t, h.as(ownerToken, "DELETE", "/api/invites/"+code, "", nil), http.StatusNoContent)
	wantStatus(t, h.as(ownerToken, "DELETE", "/api/invites/"+code, "", nil), http.StatusNotFound)
}

func TestASpaceCantGrowPastItsStorage(t *testing.T) {
	h := newHub(t)
	guest := h.join(h.invite(1)) // 1 MB
	key := testKey(3)
	big := bytes.Repeat([]byte{1}, 600<<10)
	wantStatus(t, h.sendAs(guest, key, Meta{Kind: "file", Name: "one"}, big), http.StatusCreated)
	msg := wantStatus(t, h.sendAs(guest, key, Meta{Kind: "file", Name: "two"}, big), http.StatusInsufficientStorage)
	if !strings.Contains(msg, "full (1 MB)") {
		t.Fatalf("message: %q", msg)
	}
	items := h.itemsOf(guest)
	if len(items) != 1 {
		t.Fatalf("%d items after a refused upload", len(items))
	}
	files, _ := os.ReadDir(filepath.Join(h.dir, "spaces"))
	for _, f := range files {
		left, _ := os.ReadDir(filepath.Join(h.dir, "spaces", f.Name(), "files"))
		if len(left) != 1 {
			t.Fatalf("the refused upload left %d files behind", len(left))
		}
	}
	// Room again once something is deleted.
	wantStatus(t, h.as(guest, "DELETE", "/api/items/"+items[0].ID, "", nil), http.StatusNoContent)
	wantStatus(t, h.sendAs(guest, key, Meta{Kind: "file", Name: "two"}, big), http.StatusCreated)

	var info struct {
		Owner     bool
		LimitMB   int64 `json:"limit_mb"`
		UsedBytes int64 `json:"used_bytes"`
	}
	json.Unmarshal([]byte(wantStatus(t, h.as(guest, "GET", "/api/info", "", nil), http.StatusOK)), &info)
	if info.Owner || info.LimitMB != 1 || info.UsedBytes < int64(len(big)) {
		t.Fatalf("info: %+v", info)
	}
	// The host's own feed has no such limit.
	json.Unmarshal([]byte(wantStatus(t, h.as(ownerToken, "GET", "/api/info", "", nil), http.StatusOK)), &info)
	if !info.Owner || info.LimitMB != 0 {
		t.Fatalf("host's info: %+v", info)
	}
}

func TestSpacesSurviveARestart(t *testing.T) {
	h := newHub(t)
	guest := h.join(h.invite(0))
	key := testKey(4)
	wantStatus(t, h.sendAs(guest, key, Meta{Kind: "text", Text: "still here"}, nil), http.StatusCreated)
	h.s.tidy()

	s2, err := newServer(options{dir: h.dir, token: ownerToken, maxUpload: 1 << 20, mode: modeInvite, spaceMB: 500})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s2.routes())
	defer srv.Close()
	h2 := &hub{t: t, srv: srv, s: s2, dir: h.dir}
	items := h2.itemsOf(guest)
	if len(items) != 1 {
		t.Fatalf("%d items after restart", len(items))
	}
}

func TestLeavingDeletesTheSpace(t *testing.T) {
	h := newHub(t)
	guest := h.join(h.invite(0))
	wantStatus(t, h.sendAs(guest, testKey(1), Meta{Kind: "file", Name: "x"}, []byte("data")), http.StatusCreated)

	// Someone listening for changes is cut off when the space goes.
	stream := h.as(guest, "GET", "/api/events", "", nil)
	lines := bufio.NewReader(stream.Body)
	lines.ReadString('\n') // retry:
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, lines); close(done) }()

	wantStatus(t, h.as(ownerToken, "DELETE", "/api/space", "", nil), http.StatusForbidden)
	wantStatus(t, h.as(guest, "DELETE", "/api/space", "", nil), http.StatusNoContent)
	wantStatus(t, h.as(guest, "GET", "/api/items", "", nil), http.StatusUnauthorized)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the live connection stayed open after the space was deleted")
	}
	left, _ := os.ReadDir(filepath.Join(h.dir, "spaces"))
	if len(left) != 0 {
		t.Fatalf("%d space folders left behind", len(left))
	}
	if len(h.itemsOf(ownerToken)) != 0 {
		t.Fatal("unexpected items in the host's feed")
	}
}

func TestTheHostCanDeleteASpace(t *testing.T) {
	h := newHub(t)
	guest := h.join(h.invite(0))
	var list struct{ Spaces []spaceInfo }
	json.Unmarshal([]byte(wantStatus(t, h.as(ownerToken, "GET", "/api/spaces", "", nil), http.StatusOK)), &list)
	wantStatus(t, h.as(ownerToken, "DELETE", "/api/spaces/"+list.Spaces[0].ID, "", nil), http.StatusNoContent)
	wantStatus(t, h.as(guest, "GET", "/api/vault", "", nil), http.StatusUnauthorized)
	wantStatus(t, h.as(ownerToken, "DELETE", "/api/spaces/"+list.Spaces[0].ID, "", nil), http.StatusNotFound)
}

func TestUnusedSpacesAreDeleted(t *testing.T) {
	h := newHubWith(t, options{mode: modeInvite, idle: time.Hour})
	stale := h.join(h.invite(0))
	fresh := h.join(h.invite(0))
	h.s.reg.find(stale).lastUsed.Store(time.Now().Add(-2 * time.Hour).UnixMilli())
	h.s.owner.lastUsed.Store(0) // the host's own feed is never deleted
	h.s.tidy()
	wantStatus(t, h.as(stale, "GET", "/api/vault", "", nil), http.StatusUnauthorized)
	wantStatus(t, h.as(fresh, "GET", "/api/vault", "", nil), http.StatusOK)
	wantStatus(t, h.as(ownerToken, "GET", "/api/vault", "", nil), http.StatusOK)
}

func TestExpiredInvitesDontWork(t *testing.T) {
	h := newHub(t)
	inv, err := h.s.reg.newInvite(time.Millisecond, 0)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	wantStatus(t, h.as("", "POST", "/api/spaces", "application/json", jsonBody(map[string]string{"invite": inv.Code})), http.StatusForbidden)
}

func TestLeftoverSpaceFoldersAreCleanedUp(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "spaces", "half-made", "files"), 0o700)
	if _, err := newServer(options{dir: dir, token: ownerToken, mode: modeInvite}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "spaces", "half-made")); !os.IsNotExist(err) {
		t.Fatal("a folder no space owns was kept")
	}
}

func TestCodesAreForgivingAboutTyping(t *testing.T) {
	for in, want := range map[string]string{
		"abcd-efgh-jkmn":   "abcd-efgh-jkmn",
		"ABCD EFGH JKMN":   "abcd-efgh-jkmn",
		"abcdefghjkmn":     "abcd-efgh-jkmn",
		" abcd-efgh-jkmn ": "abcd-efgh-jkmn",
	} {
		if got := normalizeCode(in); got != want {
			t.Errorf("normalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInviteLinksOpenTheWebPageAndParseBack(t *testing.T) {
	link := InviteLink("https://fetch.example.com/", "abcd-efgh-jkmn")
	if link != "https://fetch.example.com/#invite=abcd-efgh-jkmn" {
		t.Fatalf("link: %s", link)
	}
	for _, c := range []struct{ link, hub, code string }{
		{link, "https://fetch.example.com", "abcd-efgh-jkmn"},
		{"http://192.168.1.20:8787/#invite=abcd-efgh-jkmn", "http://192.168.1.20:8787", "abcd-efgh-jkmn"},
		{" https://example.com/fetch/#invite=x ", "https://example.com/fetch", "x"},
	} {
		hub, code, ok := ParseInvite(c.link)
		if !ok || hub != c.hub || code != c.code {
			t.Errorf("ParseInvite(%q) = %q %q %v", c.link, hub, code, ok)
		}
	}
	for _, bad := range []string{"https://example.com/", "fetchpair://v1?hub=x&token=y&key=z", "https://example.com/#other=1", "abcd-efgh-jkmn"} {
		if _, _, ok := ParseInvite(bad); ok {
			t.Errorf("ParseInvite(%q) accepted it", bad)
		}
	}
}
