package ui

import (
	"os"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// TestLiveBrowse drives the real UI against a real cluster, read-only: it
// opens the default type, the first bucket and its first key. Skipped unless
// RIAK_LIVE is set, e.g.:
//
//	RIAK_LIVE=127.0.0.1:8098 go test ./internal/ui/ -run TestLiveBrowse -v
func TestLiveBrowse(t *testing.T) {
	addr := os.Getenv("RIAK_LIVE")
	if addr == "" {
		t.Skip("RIAK_LIVE not set")
	}
	app := New(addr, Options{MaxKeys: 50, Timeout: 10 * time.Second})
	sim := startApp(t, app)

	waitFor(t, sim, app, "Bucket types")
	waitFor(t, sim, app, "Connected to")

	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone) // default type → buckets
	waitFor(t, sim, app, "default › buckets")

	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone) // first bucket
	waitFor(t, sim, app, "› keys")

	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone) // first key
	waitFor(t, sim, app, "Object")
	t.Log("\n" + screenText(sim))
}
