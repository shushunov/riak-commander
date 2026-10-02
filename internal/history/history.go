// Package history persists the list of Riak servers the user has connected
// to, most recently used first, together with per-server conveniences that
// Riak cannot provide over HTTP: the bucket types the user has opened (types
// cannot be enumerated over the HTTP API) and the secondary-index names used
// in queries.
//
// The store is a small JSON file, written atomically (temp file + rename)
// with 0600 permissions. A Store created with Disabled never touches disk.
package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// MaxEntries caps the number of remembered servers; the least recently used
// one is dropped when the list overflows.
const MaxEntries = 20

// maxIndexesPerBucket caps remembered 2i names per bucket.
const maxIndexesPerBucket = 10

// fileVersion is bumped on incompatible format changes.
const fileVersion = 1

// Entry is one remembered server.
type Entry struct {
	Address     string              `json:"address"`
	Label       string              `json:"label,omitempty"`
	LastUsed    time.Time           `json:"last_used"`
	UseCount    int                 `json:"use_count"`
	BucketTypes []string            `json:"bucket_types,omitempty"`
	Indexes     map[string][]string `json:"indexes,omitempty"` // "type/bucket" → index names, MRU first
}

type file struct {
	Version int     `json:"version"`
	Servers []Entry `json:"servers"`
}

// Store is the in-memory history plus where to persist it. It is not safe for
// concurrent use; the UI only touches it from its event goroutine.
type Store struct {
	path    string // "" when disabled
	entries []Entry
}

// DefaultPath returns the history file location:
//
//	$XDG_CONFIG_HOME/riak-commander/history.json   if XDG_CONFIG_HOME is set
//	~/.config/riak-commander/history.json          on macOS, Linux and other Unix
//	%AppData%\riak-commander\history.json          on Windows
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "riak-commander", "history.json"), nil
	}
	if runtime.GOOS == "windows" {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "riak-commander", "history.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "riak-commander", "history.json"), nil
}

// Disabled returns a store that remembers nothing across runs.
func Disabled() *Store { return &Store{} }

// Load reads the history at path. A missing file yields an empty store. A
// file that cannot be parsed is moved aside to <path>.corrupt (so the next
// save does not destroy it silently) and an empty store is returned together
// with an error describing what happened; the store is usable either way.
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("reading server history: %w", err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		_ = os.Rename(path, path+".corrupt")
		return s, fmt.Errorf("server history %s is unreadable (moved to %s.corrupt): %v", path, path, err)
	}
	s.entries = f.Servers
	s.sort()
	return s, nil
}

// Path is the file the store persists to ("" when disabled).
func (s *Store) Path() string { return s.path }

// Enabled reports whether the store persists to disk.
func (s *Store) Enabled() bool { return s.path != "" }

// Save writes the history atomically. It is a no-op for a disabled store.
func (s *Store) Save() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(file{Version: fileVersion, Servers: s.entries}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("saving server history: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".history-*.json")
	if err != nil {
		return fmt.Errorf("saving server history: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("saving server history: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("saving server history: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("saving server history: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("saving server history: %w", err)
	}
	return nil
}

// Entries returns a copy of the remembered servers, most recently used first.
func (s *Store) Entries() []Entry {
	return append([]Entry(nil), s.entries...)
}

// Last returns the most recently used server.
func (s *Store) Last() (Entry, bool) {
	if len(s.entries) == 0 {
		return Entry{}, false
	}
	return s.entries[0], true
}

// Get returns the entry for address.
func (s *Store) Get(address string) (Entry, bool) {
	if i := s.find(address); i >= 0 {
		return s.entries[i], true
	}
	return Entry{}, false
}

// Touch records a successful connection to address at time now, creating
// the entry if needed and moving it to the front.
func (s *Store) Touch(address string, now time.Time) {
	i := s.find(address)
	if i < 0 {
		s.entries = append(s.entries, Entry{Address: address})
		i = len(s.entries) - 1
	}
	s.entries[i].LastUsed = now
	s.entries[i].UseCount++
	s.sort()
	if len(s.entries) > MaxEntries {
		s.entries = s.entries[:MaxEntries]
	}
}

// Remove forgets address. It reports whether an entry was removed.
func (s *Store) Remove(address string) bool {
	i := s.find(address)
	if i < 0 {
		return false
	}
	s.entries = append(s.entries[:i], s.entries[i+1:]...)
	return true
}

// SetLabel sets a human-friendly name for address ("" clears it).
func (s *Store) SetLabel(address, label string) {
	if i := s.find(address); i >= 0 {
		s.entries[i].Label = label
	}
}

// AddBucketType remembers a bucket type opened on address. It reports
// whether the type was new.
func (s *Store) AddBucketType(address, btype string) bool {
	i := s.find(address)
	if i < 0 || btype == "" {
		return false
	}
	for _, t := range s.entries[i].BucketTypes {
		if t == btype {
			return false
		}
	}
	s.entries[i].BucketTypes = append(s.entries[i].BucketTypes, btype)
	sort.Strings(s.entries[i].BucketTypes)
	return true
}

// RemoveBucketType forgets a remembered bucket type. It reports whether the
// type was remembered.
func (s *Store) RemoveBucketType(address, btype string) bool {
	i := s.find(address)
	if i < 0 {
		return false
	}
	types := s.entries[i].BucketTypes
	for j, t := range types {
		if t == btype {
			s.entries[i].BucketTypes = append(types[:j], types[j+1:]...)
			return true
		}
	}
	return false
}

// AddIndex remembers a 2i name queried in bucket (keyed "type/bucket"),
// most recently used first.
func (s *Store) AddIndex(address, bucket, index string) {
	i := s.find(address)
	if i < 0 || index == "" {
		return
	}
	if s.entries[i].Indexes == nil {
		s.entries[i].Indexes = map[string][]string{}
	}
	list := []string{index}
	for _, x := range s.entries[i].Indexes[bucket] {
		if x != index {
			list = append(list, x)
		}
	}
	if len(list) > maxIndexesPerBucket {
		list = list[:maxIndexesPerBucket]
	}
	s.entries[i].Indexes[bucket] = list
}

func (s *Store) find(address string) int {
	for i, e := range s.entries {
		if e.Address == address {
			return i
		}
	}
	return -1
}

func (s *Store) sort() {
	sort.SliceStable(s.entries, func(i, j int) bool {
		return s.entries[i].LastUsed.After(s.entries[j].LastUsed)
	})
}
