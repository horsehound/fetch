package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Item is one entry in the feed. The hub can't read any of it: Meta is an
// encrypted envelope (kind, text, file name, device...) and the data on disk
// under files/<id> is an encrypted chunk stream. Only the timestamps, sizes
// and the pinned flag are visible to the hub.
type Item struct {
	ID      string `json:"id"`
	Meta    []byte `json:"meta"` // base64 in JSON
	Size    int64  `json:"size"` // bytes of encrypted data; 0 for text items
	Created int64  `json:"created"`
	Pinned  bool   `json:"pinned"`
}

var errBadUpload = errors.New("that upload isn't a complete encrypted item")

type Store struct {
	dir   string
	mu    sync.RWMutex
	items []Item // newest first
	check string // key fingerprint, set by the first device
}

type vaultFile struct {
	Check string `json:"check"`
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
	if b, err := os.ReadFile(s.vaultPath()); err == nil {
		var v vaultFile
		if json.Unmarshal(b, &v) == nil {
			s.check = v.Check
		}
	}

	// Reconcile the index with what is on disk. Items from before end-to-end
	// encryption have no envelope; they are deleted, pinned or not.
	known := map[string]bool{}
	kept := s.items[:0]
	legacy := 0
	for _, it := range s.items {
		if len(it.Meta) == 0 {
			legacy++
			continue
		}
		if it.Size > 0 {
			if st, err := os.Stat(s.Path(it.ID)); err != nil || st.Size() != it.Size {
				continue
			}
		}
		known[it.ID] = true
		kept = append(kept, it)
	}
	s.items = kept
	if legacy > 0 {
		log.Printf("deleted %d unencrypted item(s) from before end-to-end encryption", legacy)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "files"))
	for _, e := range entries {
		if !known[e.Name()] {
			os.Remove(filepath.Join(dir, "files", e.Name()))
		}
	}
	return s, s.save()
}

func (s *Store) indexPath() string     { return filepath.Join(s.dir, "index.json") }
func (s *Store) vaultPath() string     { return filepath.Join(s.dir, "vault.json") }
func (s *Store) Path(id string) string { return filepath.Join(s.dir, "files", id) }

func writeAtomic(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// save must be called with s.mu held (or before the store is shared).
func (s *Store) save() error {
	items := s.items
	if items == nil {
		items = []Item{}
	}
	return writeAtomic(s.indexPath(), items)
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

// Add stores an encrypted item: its metadata envelope and its encrypted data,
// streamed from r. The hub can only check the shape, never the contents.
func (s *Store) Add(meta []byte, r io.Reader) (Item, error) {
	if len(meta) < metaOverhead || meta[0] != formatVersion {
		return Item{}, errBadUpload
	}
	it := Item{ID: newID(), Meta: meta, Created: time.Now().UnixMilli()}

	tmp, err := os.CreateTemp(filepath.Join(s.dir, "files"), "tmp-*")
	if err != nil {
		return Item{}, err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	it.Size, err = io.Copy(tmp, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Item{}, err
	}
	if !validDataSize(it.Size) {
		return Item{}, errBadUpload
	}
	if it.Size > 0 {
		if err := os.Rename(tmp.Name(), s.Path(it.ID)); err != nil {
			return Item{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = append([]Item{it}, s.items...)
	return it, s.save()
}

func (s *Store) removeWhere(drop func(Item) bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeLocked(drop)
}

func (s *Store) removeLocked(drop func(Item) bool) (int, error) {
	kept := make([]Item, 0, len(s.items))
	n := 0
	for _, it := range s.items {
		if drop(it) {
			os.Remove(s.Path(it.ID))
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

// Usage is how much this feed takes up on the hub: the encrypted data and
// envelopes of its items, and how many there are.
func (s *Store) Usage() (bytes int64, items int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, it := range s.items {
		bytes += it.Size + int64(len(it.Meta))
	}
	return bytes, len(s.items)
}

// ---- the key fingerprint ----

func (s *Store) Check() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.check
}

var errOtherKey = errors.New("this hub already uses a different key")

// SetCheck records the fingerprint of the key the devices share. Only the
// first one sticks; a different one means a device has the wrong key.
func (s *Store) SetCheck(check string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.check == check {
		return nil
	}
	if s.check != "" {
		return errOtherKey
	}
	s.check = check
	return writeAtomic(s.vaultPath(), vaultFile{Check: check})
}

// Reset forgets the key and deletes every item, pinned or not, so the
// devices can start over with a new key.
func (s *Store) Reset() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.removeLocked(func(Item) bool { return true }); err != nil {
		return err
	}
	s.check = ""
	if err := os.Remove(s.vaultPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
