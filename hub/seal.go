package main

// End-to-end encryption. This file is the reference implementation of the
// format described in spec/encryption.md; the web page, the desktop app and
// the phone apps implement the same format and are tested against the
// vectors in spec/vectors.json, which this file's tests produce.
//
// The hub itself never holds a key. It uses this code only for the
// command-line client and in tests.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

const (
	formatVersion = 1
	chunkSize     = 64 << 10            // plaintext bytes per data chunk
	tagSize       = 16                  // AES-GCM authentication tag
	sealedChunk   = chunkSize + tagSize // ciphertext bytes per full chunk
	saltSize      = 16
	metaOverhead  = 1 + saltSize + tagSize // version byte, salt, tag
	maxMeta       = 2 << 20                // largest metadata envelope the hub accepts

	sealedType = "application/x-fetch-sealed" // Content-Type of an upload
)

var (
	errBadKey    = errors.New("that isn't a Fetch key")
	errOpen      = errors.New("can't decrypt: this item was made with a different key, or it was changed")
	errTruncated = errors.New("can't decrypt: the data was cut short")
)

// Key is the 32-byte secret every paired device shares.
type Key [32]byte

func NewKey() (Key, error) {
	var k Key
	_, err := rand.Read(k[:])
	return k, err
}

// String is the key as it appears in a pairing link: base64url, no padding.
func (k Key) String() string { return base64.RawURLEncoding.EncodeToString(k[:]) }

func ParseKey(s string) (Key, error) {
	var k Key
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
	if err != nil || len(b) != len(k) {
		return k, errBadKey
	}
	copy(k[:], b)
	return k, nil
}

// Check is a fingerprint of the key the hub keeps, so a device can tell
// whether its key is the one the others use. It reveals nothing about the key.
func (k Key) Check() string {
	return hex.EncodeToString(hkdf(k[:], nil, "fetch v1 check", 16))
}

// hkdf is HKDF-SHA256 (RFC 5869). A nil salt means a block of zero bytes.
func hkdf(ikm, salt []byte, info string, n int) []byte {
	if salt == nil {
		salt = make([]byte, sha256.Size)
	}
	ext := hmac.New(sha256.New, salt)
	ext.Write(ikm)
	prk := ext.Sum(nil)

	var out, prev []byte
	for i := byte(1); len(out) < n; i++ {
		m := hmac.New(sha256.New, prk)
		m.Write(prev)
		m.Write([]byte(info))
		m.Write([]byte{i})
		prev = m.Sum(nil)
		out = append(out, prev...)
	}
	return out[:n]
}

func gcm(key []byte) cipher.AEAD {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err) // only on a key that isn't 32 bytes, which can't happen here
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return aead
}

func (k Key) itemKeys(salt []byte) (meta, data cipher.AEAD) {
	return gcm(hkdf(k[:], salt, "fetch v1 meta", 32)), gcm(hkdf(k[:], salt, "fetch v1 data", 32))
}

// ---- metadata ----

// Meta is everything about an item except its file contents. Text items keep
// their text here and have no data.
type Meta struct {
	V      int    `json:"v"`
	Kind   string `json:"kind"` // text | image | file
	Device string `json:"device,omitempty"`
	Text   string `json:"text,omitempty"`
	Name   string `json:"name,omitempty"`
	Mime   string `json:"mime,omitempty"`
	Size   int64  `json:"size,omitempty"` // plaintext size of the data
}

// SealMeta encrypts meta under a fresh salt and returns the envelope and the
// salt (the data of the same item is encrypted with keys from that salt).
func (k Key) SealMeta(m Meta) (envelope, salt []byte, err error) {
	salt = make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, nil, err
	}
	return k.sealMetaWithSalt(m, salt), salt, nil
}

func (k Key) sealMetaWithSalt(m Meta, salt []byte) []byte {
	m.V = formatVersion
	plain, _ := json.Marshal(m)
	metaKey, _ := k.itemKeys(salt)
	env := append([]byte{formatVersion}, salt...)
	return metaKey.Seal(env, make([]byte, 12), plain, nil)
}

// OpenMeta decrypts an envelope and returns the metadata and the item's salt.
func (k Key) OpenMeta(env []byte) (Meta, []byte, error) {
	var m Meta
	if len(env) < metaOverhead || env[0] != formatVersion {
		return m, nil, errOpen
	}
	salt := env[1 : 1+saltSize]
	metaKey, _ := k.itemKeys(salt)
	plain, err := metaKey.Open(nil, make([]byte, 12), env[1+saltSize:], nil)
	if err != nil {
		return m, nil, errOpen
	}
	if err := json.Unmarshal(plain, &m); err != nil {
		return m, nil, errOpen
	}
	return m, salt, nil
}

// ---- data: a stream of 64 KiB chunks ----

// chunkNonce is the chunk number, big-endian in 11 bytes, then 1 for the last
// chunk and 0 for every other. Reordering, dropping or appending chunks makes
// decryption fail.
func chunkNonce(i uint64, last bool) []byte {
	n := make([]byte, 12)
	binary.BigEndian.PutUint64(n[3:11], i)
	if last {
		n[11] = 1
	}
	return n
}

type sealWriter struct {
	w    io.Writer
	aead cipher.AEAD
	buf  []byte
	n    uint64
}

// SealData returns a writer that encrypts what is written to it into w. Close
// writes the final chunk; without it the data can't be decrypted.
func (k Key) SealData(w io.Writer, salt []byte) io.WriteCloser {
	_, data := k.itemKeys(salt)
	return &sealWriter{w: w, aead: data, buf: make([]byte, 0, chunkSize)}
}

func (s *sealWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		// A full buffer is only written once more data arrives, because the
		// last chunk has to be marked as such.
		if len(s.buf) == chunkSize {
			if err := s.flush(false); err != nil {
				return total - len(p), err
			}
		}
		n := copy(s.buf[len(s.buf):chunkSize], p)
		s.buf = s.buf[:len(s.buf)+n]
		p = p[n:]
	}
	return total, nil
}

func (s *sealWriter) flush(last bool) error {
	out := s.aead.Seal(nil, chunkNonce(s.n, last), s.buf, nil)
	s.n++
	s.buf = s.buf[:0]
	_, err := s.w.Write(out)
	return err
}

func (s *sealWriter) Close() error { return s.flush(true) }

type openReader struct {
	r    io.Reader
	aead cipher.AEAD
	n    uint64
	in   []byte // ciphertext read ahead
	out  []byte // decrypted, not yet returned
	done bool
}

// OpenData returns a reader of the plaintext of an encrypted data stream. It
// returns an error rather than io.EOF if the stream was cut short or altered.
func (k Key) OpenData(r io.Reader, salt []byte) io.Reader {
	_, data := k.itemKeys(salt)
	return &openReader{r: r, aead: data}
}

func (o *openReader) Read(p []byte) (int, error) {
	for len(o.out) == 0 {
		if o.done {
			return 0, io.EOF
		}
		if err := o.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, o.out)
	o.out = o.out[n:]
	return n, nil
}

// next decrypts one chunk. A chunk is the last one exactly when nothing
// follows it, so one byte past it is read to find out.
func (o *openReader) next() error {
	want := sealedChunk + 1
	for len(o.in) < want {
		buf := make([]byte, want-len(o.in))
		n, err := io.ReadFull(o.r, buf)
		o.in = append(o.in, buf[:n]...)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return err
		}
	}
	last := len(o.in) <= sealedChunk
	if len(o.in) < tagSize {
		return errTruncated
	}
	size := sealedChunk
	if last {
		size = len(o.in)
	}
	plain, err := o.aead.Open(nil, chunkNonce(o.n, last), o.in[:size], nil)
	if err != nil {
		return errOpen
	}
	o.n++
	o.in = o.in[size:]
	o.out = plain
	o.done = last
	return nil
}

// sealedSize is how many bytes plain bytes of data become.
func sealedSize(plain int64) int64 {
	full := plain / chunkSize
	if plain%chunkSize == 0 && plain > 0 {
		full-- // the last chunk is a full one
	}
	return plain + (full+1)*tagSize
}

// validDataSize reports whether n bytes can be a complete encrypted data
// stream: nothing at all (a text item), or full chunks plus a final one that
// holds at least its tag.
func validDataSize(n int64) bool {
	return n == 0 || n%sealedChunk == 0 || n%sealedChunk >= tagSize
}

// ---- uploads ----

// WriteUpload writes the body of an upload: the length of the metadata
// envelope (4 bytes, big-endian), the envelope, then the encrypted data.
func (k Key) WriteUpload(w io.Writer, m Meta, data io.Reader) error {
	env, salt, err := k.SealMeta(m)
	if err != nil {
		return err
	}
	head := binary.BigEndian.AppendUint32(nil, uint32(len(env)))
	if _, err := w.Write(append(head, env...)); err != nil {
		return err
	}
	if data == nil {
		return nil
	}
	sw := k.SealData(w, salt)
	if _, err := io.Copy(sw, data); err != nil {
		return err
	}
	return sw.Close()
}

// ---- pairing links ----

// Pairing is what a new device needs: where the hub is, how to get in, and
// the key. Anyone holding the link can read and change the whole feed.
type Pairing struct {
	Hub   string
	Token string
	Key   Key
}

func (p Pairing) Link() string {
	q := url.Values{}
	q.Set("hub", p.Hub)
	q.Set("token", p.Token)
	q.Set("key", p.Key.String())
	return "fetchpair://v1?" + q.Encode()
}

func ParsePairing(link string) (Pairing, error) {
	var p Pairing
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "fetchpair" || u.Host != "v1" {
		return p, fmt.Errorf("that isn't a Fetch pairing link (they start with fetchpair://)")
	}
	q := u.Query()
	p.Hub = normalizeURL(q.Get("hub"))
	p.Token = q.Get("token")
	if p.Key, err = ParseKey(q.Get("key")); err != nil || p.Hub == "" || p.Token == "" {
		return p, fmt.Errorf("that pairing link is incomplete; copy it again")
	}
	return p, nil
}
