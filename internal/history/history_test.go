package history

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestLoadMissingFileIsEmpty(t *testing.T) {
	s, err := Load(filepath.Join(t.TempDir(), "nope", "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Last(); ok {
		t.Fatal("expected empty history")
	}
}

func TestTouchOrdersMostRecentFirst(t *testing.T) {
	s := Disabled()
	s.Touch("a:8098", t0)
	s.Touch("b:8098", t0.Add(time.Minute))
	s.Touch("a:8098", t0.Add(2*time.Minute))

	got := []string{}
	for _, e := range s.Entries() {
		got = append(got, e.Address)
	}
	if !reflect.DeepEqual(got, []string{"a:8098", "b:8098"}) {
		t.Fatalf("order = %v", got)
	}
	last, _ := s.Last()
	if last.Address != "a:8098" || last.UseCount != 2 {
		t.Fatalf("last = %+v", last)
	}
}

func TestCapDropsLeastRecentlyUsed(t *testing.T) {
	s := Disabled()
	for i := 0; i < MaxEntries+5; i++ {
		s.Touch(fmt.Sprintf("h%d:8098", i), t0.Add(time.Duration(i)*time.Second))
	}
	entries := s.Entries()
	if len(entries) != MaxEntries {
		t.Fatalf("len = %d, want %d", len(entries), MaxEntries)
	}
	if entries[0].Address != fmt.Sprintf("h%d:8098", MaxEntries+4) {
		t.Fatalf("newest entry missing: %v", entries[0])
	}
	if _, ok := s.Get("h0:8098"); ok {
		t.Fatal("oldest entry should have been dropped")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "history.json")
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Touch("riak1:8098", t0)
	s.Touch("https://riak2:443", t0.Add(time.Hour))
	s.SetLabel("riak1:8098", "staging")
	if !s.AddBucketType("riak1:8098", "sessions") || s.AddBucketType("riak1:8098", "sessions") {
		t.Fatal("AddBucketType should report new types only once")
	}
	s.AddBucketType("riak1:8098", "carts")
	s.AddIndex("riak1:8098", "default/users", "email_bin")
	s.AddIndex("riak1:8098", "default/users", "plan_bin")
	s.AddIndex("riak1:8098", "default/users", "email_bin")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("history file mode = %o, want 600", perm)
	}

	s2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Entries(), s2.Entries()) {
		t.Fatalf("round trip mismatch:\n%+v\n%+v", s.Entries(), s2.Entries())
	}
	e, _ := s2.Get("riak1:8098")
	if e.Label != "staging" {
		t.Errorf("label = %q", e.Label)
	}
	if !reflect.DeepEqual(e.BucketTypes, []string{"carts", "sessions"}) {
		t.Errorf("types = %v", e.BucketTypes)
	}
	if !reflect.DeepEqual(e.Indexes["default/users"], []string{"email_bin", "plan_bin"}) {
		t.Errorf("indexes = %v", e.Indexes)
	}
	if last, _ := s2.Last(); last.Address != "https://riak2:443" {
		t.Errorf("last = %v", last.Address)
	}

	// no temp files left behind
	files, _ := os.ReadDir(filepath.Dir(path))
	if len(files) != 1 {
		t.Errorf("unexpected files in config dir: %v", files)
	}
}

func TestRemove(t *testing.T) {
	s := Disabled()
	s.Touch("a:1", t0)
	s.Touch("b:1", t0)
	if !s.Remove("a:1") || s.Remove("a:1") {
		t.Fatal("Remove should succeed exactly once")
	}
	if len(s.Entries()) != 1 {
		t.Fatalf("entries = %v", s.Entries())
	}
	s.AddBucketType("b:1", "x")
	if !s.RemoveBucketType("b:1", "x") || s.RemoveBucketType("b:1", "x") {
		t.Fatal("RemoveBucketType should succeed exactly once")
	}
}

func TestCorruptFileIsMovedAside(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for a corrupt file")
	}
	if s == nil || len(s.Entries()) != 0 {
		t.Fatal("expected a usable empty store")
	}
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("corrupt file not preserved: %v", err)
	}
}

func TestDisabledNeverWrites(t *testing.T) {
	s := Disabled()
	s.Touch("a:1", t0)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if s.Enabled() || s.Path() != "" {
		t.Fatal("disabled store must not have a path")
	}
}

func TestDefaultPathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join("/tmp/xdg", "riak-commander", "history.json") {
		t.Fatalf("path = %s", p)
	}
}
