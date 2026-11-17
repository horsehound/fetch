package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Item is one entry in the feed. Text lives inline; images and files live on
// disk under files/<id>.
type Item struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // text | image | file
	Text    string `json:"text,omitempty"`
	Name    string `json:"name,omitempty"`
	Mime    string `json:"mime,omitempty"`
	Size    int64  `json:"size"`
	Device  string `json:"device,omitempty"`
	Created int64  `json:"created"` // unix milliseconds
	Pinned  bool   `json:"pinned"`

	// Filled in when the feed is listed, never stored: set when a text item
	// is code. Lang is an id such as "bash", LangLabel its display name, and
	// Tokens the text split into highlight classes (see highlight.go).
	Lang      string `json:"lang,omitempty"`
	LangLabel string `json:"lang_label,omitempty"`
	Tokens    []Tok  `json:"tokens,omitempty"`
}

type Store struct {
	dir   string
	mu    sync.RWMutex
	items []Item // newest first
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	if b, err := os.ReadFile(s.indexPath()); err == nil {
		if err := json.Unmarshal(b, &s.items); err != nil {
			return nil, fmt.Errorf("index.json is corrupt: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Reconcile the index with what is actually on disk.
	known := map[string]bool{}
	kept := s.items[:0]
	for _, it := range s.items {
		if it.Kind != "text" {
			if _, err := os.Stat(s.Path(it.ID)); err != nil {
				continue
			}
		}
		known[it.ID] = true
		kept = append(kept, it)
	}
	s.items = kept
	entries, _ := os.ReadDir(filepath.Join(dir, "files"))
	for _, e := range entries {
		if !known[e.Name()] {
			os.Remove(filepath.Join(dir, "files", e.Name()))
		}
	}
	return s, s.save()
}

func (s *Store) indexPath() string     { return filepath.Join(s.dir, "index.json") }
func (s *Store) Path(id string) string { return filepath.Join(s.dir, "files", id) }

// save must be called with s.mu held.
func (s *Store) save() error {
	items := s.items
	if items == nil {
		items = []Item{}
	}
	b, err := json.Marshal(items)
	if err != nil {
		return err
	}
	tmp := s.indexPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.indexPath())
}

func newID() string {
	r := make([]byte, 5)
	rand.Read(r)
	return fmt.Sprintf("%011x%s", time.Now().UnixMilli(), hex.EncodeToString(r))
}

func (s *Store) List() []Item {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Item, len(s.items))
	copy(out, s.items)
	return out
}

func (s *Store) Get(id string) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, it := range s.items {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

func (s *Store) add(it Item) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append([]Item{it}, s.items...)
	return it, s.save()
}

func (s *Store) AddText(text, device string) (Item, error) {
	return s.add(Item{
		ID: newID(), Kind: "text", Text: text, Size: int64(len(text)),
		Device: device, Created: time.Now().UnixMilli(),
	})
}

// AddFile streams r to disk and records it. Errors from r (such as the upload
// size limit) are returned unchanged.
func (s *Store) AddFile(r io.Reader, name, declaredMime, device string) (Item, error) {
	id := newID()
	tmp, err := os.CreateTemp(filepath.Join(s.dir, "files"), "tmp-*")
	if err != nil {
		return Item{}, err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed

	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		tmp.Close()
		return Item{}, err
	}
	head = head[:n]
	size := int64(n)
	if _, err := tmp.Write(head); err != nil {
		tmp.Close()
		return Item{}, err
	}
	copied, err := io.Copy(tmp, r)
	size += copied
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Item{}, err
	}
	if size == 0 {
		return Item{}, errEmpty
	}

	name = cleanName(name)
	mt := resolveMime(declaredMime, name, head)
	kind := "file"
	if inlineImage[mt] {
		kind = "image"
	}
	if name == "" {
		name = kind + "-" + time.Now().Format("20060102-150405") + extFor(mt)
	}
	if err := os.Rename(tmp.Name(), s.Path(id)); err != nil {
		return Item{}, err
	}
	return s.add(Item{
		ID: id, Kind: kind, Name: name, Mime: mt, Size: size,
		Device: device, Created: time.Now().UnixMilli(),
	})
}

func (s *Store) removeWhere(drop func(Item) bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := make([]Item, 0, len(s.items))
	n := 0
	for _, it := range s.items {
		if drop(it) {
			if it.Kind != "text" {
				os.Remove(s.Path(it.ID))
			}
			n++
			continue
		}
		kept = append(kept, it)
	}
	if n == 0 {
		return 0, nil
	}
	s.items = kept
	return n, s.save()
}

func (s *Store) Delete(id string) (bool, error) {
	n, err := s.removeWhere(func(it Item) bool { return it.ID == id })
	return n > 0, err
}

// Clear removes everything that is not pinned.
func (s *Store) Clear() (int, error) {
	return s.removeWhere(func(it Item) bool { return !it.Pinned })
}

// Expire removes unpinned items older than ttl.
func (s *Store) Expire(ttl time.Duration) (int, error) {
	cutoff := time.Now().Add(-ttl).UnixMilli()
	return s.removeWhere(func(it Item) bool { return !it.Pinned && it.Created < cutoff })
}

func (s *Store) SetPinned(id string, on bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID == id {
			s.items[i].Pinned = on
			return true, s.save()
		}
	}
	return false, nil
}

// ---- naming and type helpers ----

// Types a browser can safely show inline in an <img>. SVG is deliberately
// excluded: it can carry script, so it is always served as a download.
var inlineImage = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true,
	"image/webp": true, "image/avif": true, "image/bmp": true,
}

var commonExt = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif",
	"image/webp": ".webp", "image/avif": ".avif", "image/bmp": ".bmp",
	"image/heic": ".heic", "image/svg+xml": ".svg",
	"application/pdf": ".pdf", "application/zip": ".zip",
	"text/plain": ".txt", "video/mp4": ".mp4", "video/quicktime": ".mov",
}

func extFor(mt string) string {
	if e, ok := commonExt[mt]; ok {
		return e
	}
	if exts, _ := mime.ExtensionsByType(mt); len(exts) > 0 {
		return exts[0]
	}
	return ""
}

func baseType(s string) string {
	if mt, _, err := mime.ParseMediaType(s); err == nil {
		return mt
	}
	return ""
}

// isVague reports content types that say nothing about the payload (curl
// sends form-urlencoded for any --data upload, for instance).
func isVague(mt string) bool {
	return mt == "" || mt == "application/octet-stream" || mt == "application/x-www-form-urlencoded"
}

func resolveMime(declared, name string, head []byte) string {
	mt := baseType(declared)
	if !isVague(mt) {
		return mt
	}
	if byExt := baseType(mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))); byExt != "" {
		return byExt
	}
	if sniffed := baseType(http.DetectContentType(head)); sniffed != "" {
		return sniffed
	}
	return "application/octet-stream"
}

func cleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "." || name == ".." {
		return ""
	}
	if r := []rune(name); len(r) > 180 {
		name = string(r[len(r)-180:])
	}
	return name
}
