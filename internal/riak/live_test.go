package riak

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestLiveCRUDRoundTrip exercises create → read → update → delete against a
// real cluster, in a dedicated scratch bucket so no application data is
// touched. Skipped unless RIAK_LIVE is set:
//
//	RIAK_LIVE=127.0.0.1:8098 go test ./internal/riak/ -run TestLiveCRUDRoundTrip -v
func TestLiveCRUDRoundTrip(t *testing.T) {
	hostPort := os.Getenv("RIAK_LIVE")
	if hostPort == "" {
		t.Skip("RIAK_LIVE not set")
	}
	const bucket = "riak_commander_selftest"
	key := fmt.Sprintf("crud-%d", time.Now().UnixNano())
	c, err := NewClient(hostPort, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// best-effort cleanup even on failure
	defer c.DeleteObject(ctx, "", bucket, key, "")

	// create (with a 2i index, as real application objects usually carry)
	obj := &RiakObject{
		Bucket:      bucket,
		Key:         key,
		Body:        []byte(`{"name":"selftest","count":9007199254740993,"nested":{"deep":true}}`),
		ContentType: "application/json",
		Indexes:     map[string][]string{"selftest_bin": {"v1"}},
	}
	if err := c.PutObject(ctx, obj, true); err != nil {
		t.Fatalf("create: %v", err)
	}

	// read back: vclock and index must be there
	got, err := c.GetObject(ctx, "", bucket, key)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Vclock == "" {
		t.Fatal("read: no vclock returned")
	}
	if len(got.Indexes["selftest_bin"]) != 1 || got.Indexes["selftest_bin"][0] != "v1" {
		t.Fatalf("read: 2i index lost, got %v", got.Indexes)
	}
	if !bytes.Contains(got.Body, []byte("9007199254740993")) {
		t.Fatalf("read: body mismatch: %s", got.Body)
	}

	// 2i exact query finds the key
	res, err := c.IndexQuery(ctx, "", bucket, "selftest_bin", "v1", "", 100)
	if err != nil {
		t.Fatalf("2i query: %v", err)
	}
	found := false
	for _, k := range res.Keys {
		if k == key {
			found = true
		}
	}
	if !found {
		t.Fatalf("2i query did not return %s (got %v)", key, res.Keys)
	}

	// update with the vclock, exactly as the editor's save path does
	got.Body = []byte(`{"name":"selftest-updated","count":9007199254740993,"nested":{"deep":false}}`)
	if err := c.PutObject(ctx, got, false); err != nil {
		t.Fatalf("update: %v", err)
	}
	got2, err := c.GetObject(ctx, "", bucket, key)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !bytes.Contains(got2.Body, []byte("selftest-updated")) {
		t.Fatalf("update not visible: %s", got2.Body)
	}
	if len(got2.Indexes["selftest_bin"]) != 1 {
		t.Fatalf("update dropped the 2i index: %v", got2.Indexes)
	}

	// delete, then confirm gone
	if err := c.DeleteObject(ctx, "", bucket, key, got2.Vclock); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// tombstone reads can briefly linger; retry a few times
	for i := 0; i < 10; i++ {
		_, err = c.GetObject(ctx, "", bucket, key)
		if errors.Is(err, ErrNotFound) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("key still readable after delete: %v", err)
}
