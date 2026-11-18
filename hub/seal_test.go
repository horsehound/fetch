package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

func testKey(seed byte) Key {
	var k Key
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

// pattern is reproducible test data any language can generate.
func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + 3)
	}
	return b
}

func seal(t *testing.T, k Key, salt, plain []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := k.SealData(&buf, salt)
	// Odd write sizes, so the chunking can't depend on how data arrives.
	for p := plain; len(p) > 0; {
		n := min(len(p), 1000+len(p)%777)
		if _, err := w.Write(p[:n]); err != nil {
			t.Fatal(err)
		}
		p = p[n:]
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func open(k Key, salt, sealed []byte) ([]byte, error) {
	return io.ReadAll(k.OpenData(bytes.NewReader(sealed), salt))
}

var sizes = []int{0, 1, 100, chunkSize - 1, chunkSize, chunkSize + 1, 2 * chunkSize, 3*chunkSize + 12345}

func TestDataRoundTripsAtEverySize(t *testing.T) {
	k, salt := testKey(1), pattern(saltSize)
	for _, n := range sizes {
		plain := pattern(n)
		sealed := seal(t, k, salt, plain)
		if int64(len(sealed)) != sealedSize(int64(n)) {
			t.Errorf("%d bytes sealed to %d, sealedSize says %d", n, len(sealed), sealedSize(int64(n)))
		}
		if !validDataSize(int64(len(sealed))) {
			t.Errorf("%d bytes sealed to an invalid size %d", n, len(sealed))
		}
		got, err := open(k, salt, sealed)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("%d bytes: round trip failed: %v", n, err)
		}
	}
}

func TestAlteredDataIsRejected(t *testing.T) {
	k, salt := testKey(1), pattern(saltSize)
	plain := pattern(2*chunkSize + 500)
	sealed := seal(t, k, salt, plain)
	first, second, last := sealed[:sealedChunk], sealed[sealedChunk:2*sealedChunk], sealed[2*sealedChunk:]
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	flipped := bytes.Clone(sealed)
	flipped[sealedChunk+10] ^= 1

	cases := map[string][]byte{
		"a flipped bit":            flipped,
		"the last chunk dropped":   cat(first, second),
		"the last chunk cut short": sealed[:len(sealed)-1],
		"chunks swapped":           cat(second, first, last),
		"a chunk repeated":         cat(first, first, second, last),
		"bytes appended":           cat(sealed, []byte{0}),
		"a whole chunk appended":   cat(sealed, last),
		"only a fragment":          sealed[:10],
		"nothing at all":           nil,
	}
	for name, data := range cases {
		if _, err := open(k, salt, data); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := open(testKey(2), salt, sealed); !errors.Is(err, errOpen) {
		t.Errorf("another key: got %v", err)
	}
	if _, err := open(k, pattern(saltSize + 1)[1:], sealed); !errors.Is(err, errOpen) {
		t.Errorf("another item's salt: got %v", err)
	}
}

func TestValidDataSize(t *testing.T) {
	for n, want := range map[int64]bool{0: true, 15: false, 16: true, 17: true, sealedChunk: true, sealedChunk + 15: false, sealedChunk + 16: true, 2 * sealedChunk: true} {
		if validDataSize(n) != want {
			t.Errorf("validDataSize(%d) = %v", n, !want)
		}
	}
}

func TestMetaRoundTripsAndIsBoundToItsKey(t *testing.T) {
	k := testKey(1)
	m := Meta{Kind: "image", Name: "grüße 🎾.png", Mime: "image/png", Device: "CachyOS", Size: 42}
	env, salt, err := k.SealMeta(m)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(env, []byte("grüße")) || bytes.Contains(env, []byte("CachyOS")) {
		t.Fatal("the envelope contains readable metadata")
	}
	got, gotSalt, err := k.OpenMeta(env)
	m.V = formatVersion
	if err != nil || got != m || !bytes.Equal(gotSalt, salt) {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	other, _, _ := k.SealMeta(m)
	if bytes.Equal(other[1:1+saltSize], salt) {
		t.Error("two envelopes share a salt")
	}
	for name, bad := range map[string][]byte{
		"another key":   func() []byte { e, _, _ := testKey(2).SealMeta(m); return e }(),
		"a flipped bit": func() []byte { e := bytes.Clone(env); e[len(e)-1] ^= 1; return e }(),
		"a new version": func() []byte { e := bytes.Clone(env); e[0] = 2; return e }(),
		"too short":     env[:metaOverhead-1],
	} {
		if _, _, err := k.OpenMeta(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestUploadsCarryMetaThenData(t *testing.T) {
	k := testKey(3)
	var buf bytes.Buffer
	if err := k.WriteUpload(&buf, Meta{Kind: "file", Name: "a.bin"}, bytes.NewReader(pattern(70000))); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	n := int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	m, salt, err := k.OpenMeta(b[4 : 4+n])
	if err != nil || m.Name != "a.bin" {
		t.Fatalf("meta: %+v %v", m, err)
	}
	got, err := open(k, salt, b[4+n:])
	if err != nil || !bytes.Equal(got, pattern(70000)) {
		t.Fatalf("data: %v", err)
	}
}

func TestPairingLinks(t *testing.T) {
	p := Pairing{Hub: "http://192.168.1.20:8787", Token: "abcde-fghjk-mnpqr-stuvw", Key: testKey(9)}
	link := p.Link()
	if !strings.HasPrefix(link, "fetchpair://v1?") {
		t.Fatalf("link = %s", link)
	}
	got, err := ParsePairing("  " + link + "\n")
	if err != nil || got != p {
		t.Fatalf("parsed %+v, %v", got, err)
	}
	// A link copied around may lose its scheme on the hub address.
	bare := strings.Replace(link, "http%3A%2F%2F", "", 1)
	if got, err := ParsePairing(bare); err != nil || got.Hub != "http://192.168.1.20:8787" {
		t.Errorf("bare hub address: %+v %v", got, err)
	}
	for _, bad := range []string{"", "https://example.com/?key=x", "fetchpair://v2?hub=a&token=b&key=" + testKey(1).String(),
		"fetchpair://v1?hub=a&token=b&key=short", "fetchpair://v1?token=b&key=" + testKey(1).String()} {
		if _, err := ParsePairing(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestKeysAndChecks(t *testing.T) {
	a, _ := NewKey()
	b, _ := NewKey()
	if a == b {
		t.Fatal("two new keys are equal")
	}
	parsed, err := ParseKey(a.String())
	if err != nil || parsed != a || len(a.String()) != 43 {
		t.Fatalf("key text round trip: %v", err)
	}
	if a.Check() == b.Check() || len(a.Check()) != 32 || a.Check() != a.Check() {
		t.Error("checks don't tell keys apart")
	}
	if _, err := ParseKey("AAAA"); err == nil {
		t.Error("a short key was accepted")
	}
}

// ---- vectors for the other implementations ----

type vectorItem struct {
	Name     string          `json:"name"`
	Salt     string          `json:"salt"`      // hex
	MetaKey  string          `json:"meta_key"`  // hex, for debugging
	DataKey  string          `json:"data_key"`  // hex
	Meta     json.RawMessage `json:"meta"`      // what the envelope contains
	Envelope string          `json:"envelope"`  // base64
	DataSize int             `json:"data_size"` // plaintext: pattern(data_size)
	Data     string          `json:"data"`      // base64 of the encrypted data
}

type vectorCorruption struct {
	Name string `json:"name"`
	Of   int    `json:"of"`
	Op   string `json:"op"`
}

// corrupt applies a vectorCorruption to an envelope and data.
func corrupt(op string, env, data []byte) ([]byte, []byte) {
	env, data = bytes.Clone(env), bytes.Clone(data)
	switch {
	case op == "flip_envelope":
		env[len(env)-1] ^= 1
	case strings.HasPrefix(op, "keep:"):
		var n int
		fmt.Sscanf(op, "keep:%d", &n)
		data = data[:n]
	case op == "append_zero":
		data = append(data, 0)
	case op == "swap_first_chunks":
		data = bytes.Join([][]byte{data[sealedChunk : 2*sealedChunk], data[:sealedChunk], data[2*sealedChunk:]}, nil)
	}
	return env, data
}

type vectorFile struct {
	Note   string       `json:"note"`
	Key    string       `json:"key"` // as in a pairing link
	KeyHex string       `json:"key_hex"`
	Check  string       `json:"check"`
	Link   string       `json:"link"`
	Hub    string       `json:"hub"`
	Token  string       `json:"token"`
	Items  []vectorItem `json:"items"`
	// Altered copies of an item that must not open. Each is an operation on
	// item "of": flip_envelope (flip the last bit of the envelope),
	// keep:<n> (keep only the first n bytes of the data), append_zero (add a
	// 0 byte to the data), swap_first_chunks (swap data chunks 0 and 1).
	Corrupt []vectorCorruption `json:"corrupt"`
	// Invite links: what the host sends someone so they can make a space.
	// Every client reads the hub address and code out of these, and treats
	// each of not_invites as something else (a pairing link, or nothing).
	Invites    []vectorInvite `json:"invites"`
	NotInvites []string       `json:"not_invites"`
}

type vectorInvite struct {
	Link string `json:"link"`
	Hub  string `json:"hub"`
	Code string `json:"code"`
}

func makeVectors() vectorFile {
	k := testKey(0x10)
	v := vectorFile{
		Note:   "Generated by hub/seal_test.go (FETCH_WRITE_VECTORS=1 go test -run Vectors). Plaintext data is pattern(n): byte i = (i*7 + 3) mod 256.",
		Key:    k.String(),
		KeyHex: hex.EncodeToString(k[:]),
		Check:  k.Check(),
		Hub:    "http://192.168.1.20:8787",
		Token:  "abcde-fghjk-mnpqr-stuvw",
	}
	v.Link = Pairing{Hub: v.Hub, Token: v.Token, Key: k}.Link()
	items := []struct {
		name string
		meta Meta
		size int
	}{
		{"text", Meta{Kind: "text", Device: "CachyOS", Text: "sudo pacman -Syu\n# grüße 🎾 \"quoted\""}, 0},
		{"empty file", Meta{Kind: "file", Name: "empty.txt", Mime: "text/plain", Device: "iPhone"}, 0},
		{"small picture", Meta{Kind: "image", Name: "shot.png", Mime: "image/png", Device: "Windows PC", Size: 1000}, 1000},
		{"exactly one chunk", Meta{Kind: "file", Name: "one.bin", Mime: "application/octet-stream", Size: chunkSize}, chunkSize},
		{"two chunks and a bit", Meta{Kind: "file", Name: "Bericht März.pdf", Mime: "application/pdf", Size: 2*chunkSize + 7}, 2*chunkSize + 7},
	}
	for i, it := range items {
		salt := pattern(saltSize + i)[i:]
		mk, dk := hkdf(k[:], salt, "fetch v1 meta", 32), hkdf(k[:], salt, "fetch v1 data", 32)
		env := k.sealMetaWithSalt(it.meta, salt)
		m := it.meta
		m.V = formatVersion
		mj, _ := json.Marshal(m)
		vi := vectorItem{
			Name: it.name, Salt: hex.EncodeToString(salt), MetaKey: hex.EncodeToString(mk), DataKey: hex.EncodeToString(dk),
			Meta: mj, Envelope: base64.StdEncoding.EncodeToString(env), DataSize: it.size,
		}
		if it.name != "text" {
			var buf bytes.Buffer
			w := k.SealData(&buf, salt)
			w.Write(pattern(it.size))
			w.Close()
			vi.Data = base64.StdEncoding.EncodeToString(buf.Bytes())
		}
		v.Items = append(v.Items, vi)
	}
	full := sealedSize(2*chunkSize + 7)
	v.Corrupt = []vectorCorruption{
		{"envelope with a flipped bit", 0, "flip_envelope"},
		{"last chunk dropped", 4, fmt.Sprintf("keep:%d", 2*sealedChunk)},
		{"cut one byte short", 4, fmt.Sprintf("keep:%d", full-1)},
		{"one byte appended", 4, "append_zero"},
		{"first two chunks swapped", 4, "swap_first_chunks"},
	}
	v.Invites = []vectorInvite{
		{InviteLink("https://fetch.example.com", "abcd-efgh-jkmn"), "https://fetch.example.com", "abcd-efgh-jkmn"},
		{InviteLink(v.Hub, "2345-6789-abcd"), v.Hub, "2345-6789-abcd"},
		{"  https://fetch.example.com/#invite=abcd-efgh-jkmn\n", "https://fetch.example.com", "abcd-efgh-jkmn"},
	}
	v.NotInvites = []string{v.Link, "https://fetch.example.com/", "https://fetch.example.com/#other=1", "abcd-efgh-jkmn", "fetchpair://v1?hub=x&invite=abcd"}
	return v
}

const vectorPath = "../spec/vectors.json"

// The vectors are checked in. This test fails if the format changes without
// them being regenerated (FETCH_WRITE_VECTORS=1), and checks that they open.
func TestVectors(t *testing.T) {
	want := makeVectors()
	b, _ := json.MarshalIndent(want, "", "  ")
	b = append(b, '\n')
	if os.Getenv("FETCH_WRITE_VECTORS") != "" {
		if err := os.WriteFile(vectorPath, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	have, err := os.ReadFile(vectorPath)
	if err != nil || !bytes.Equal(have, b) {
		t.Fatalf("%s is out of date (run with FETCH_WRITE_VECTORS=1): %v", vectorPath, err)
	}
	for _, inv := range want.Invites {
		hub, code, ok := ParseInvite(inv.Link)
		if !ok || hub != inv.Hub || code != inv.Code {
			t.Errorf("invite %q: got %q %q %v", inv.Link, hub, code, ok)
		}
	}
	for _, s := range want.NotInvites {
		if _, _, ok := ParseInvite(s); ok {
			t.Errorf("%q was taken for an invite link", s)
		}
	}
	k, _ := ParseKey(want.Key)
	for _, it := range want.Items {
		env, _ := base64.StdEncoding.DecodeString(it.Envelope)
		m, salt, err := k.OpenMeta(env)
		if err != nil || hex.EncodeToString(salt) != it.Salt {
			t.Fatalf("%s: %v", it.Name, err)
		}
		mj, _ := json.Marshal(m)
		if !bytes.Equal(mj, it.Meta) {
			t.Errorf("%s: meta %s", it.Name, mj)
		}
		if it.Data != "" {
			data, _ := base64.StdEncoding.DecodeString(it.Data)
			plain, err := open(k, salt, data)
			if err != nil || !bytes.Equal(plain, pattern(it.DataSize)) {
				t.Errorf("%s: data: %v", it.Name, err)
			}
		}
	}
	for _, c := range want.Corrupt {
		it := want.Items[c.Of]
		salt, _ := hex.DecodeString(it.Salt)
		env0, _ := base64.StdEncoding.DecodeString(it.Envelope)
		data0, _ := base64.StdEncoding.DecodeString(it.Data)
		env, data := corrupt(c.Op, env0, data0)
		_, _, metaErr := k.OpenMeta(env)
		_, dataErr := open(k, salt, data)
		if metaErr == nil && (it.Data == "" || dataErr == nil) {
			t.Errorf("%s: opened", c.Name)
		}
	}
	if p, err := ParsePairing(want.Link); err != nil || p.Key != k {
		t.Errorf("link: %v", err)
	}
}
