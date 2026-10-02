package ui

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/shushunov/riak-commander/internal/history"
	"github.com/shushunov/riak-commander/internal/riak"
)

// stubRiak serves a minimal fake cluster: one default-type bucket "users"
// with one JSON key "alice". PUTs are recorded in puts.
type stubRiak struct {
	mu   sync.Mutex
	puts []*http.Request
	body []string
}

func (s *stubRiak) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "OK")
	})
	mux.HandleFunc("/buckets", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"buckets":["users"]}`)
	})
	mux.HandleFunc("/buckets/users/keys", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"keys":["alice"]}`)
	})
	mux.HandleFunc("/buckets/users/props", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"props":{"name":"users","allow_mult":false}}`)
	})
	mux.HandleFunc("/buckets/users/keys/alice", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			s.puts = append(s.puts, r)
			s.body = append(s.body, string(b))
			s.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Riak-Vclock", "vc1")
		w.Header().Set("x-riak-index-email_bin", "alice@example.com")
		fmt.Fprint(w, `{"email":"alice@example.com","plan":{"name":"business"}}`)
	})
	return mux
}

// screenText flattens the simulation screen into a string for assertions.
func screenText(sim tcell.SimulationScreen) string {
	cells, w, _ := sim.GetContents()
	var b strings.Builder
	for i, c := range cells {
		if len(c.Runes) > 0 {
			b.WriteRune(c.Runes[0])
		} else {
			b.WriteByte(' ')
		}
		if (i+1)%w == 0 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func waitFor(t *testing.T, sim tcell.SimulationScreen, app *App, substr string) {
	t.Helper()
	var txt string
	for i := 0; i < 150; i++ {
		done := make(chan struct{})
		app.tv.QueueUpdateDraw(func() {
			txt = screenText(sim)
			close(done)
		})
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("UI goroutine stuck waiting for %q", substr)
		}
		if strings.Contains(txt, substr) {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("screen never showed %q; screen:\n%s", substr, txt)
}

// startApp runs app on a 120x40 simulation screen until the test ends.
func startApp(t *testing.T, app *App) tcell.SimulationScreen {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	app.SetScreen(sim)
	errc := make(chan error, 1)
	go func() { errc <- app.Run() }()
	// Run initialises the screen (80x25); enlarge it once the loop is up
	app.tv.QueueUpdateDraw(func() { sim.SetSize(120, 40) })
	t.Cleanup(func() {
		app.tv.QueueUpdateDraw(func() { app.tv.Stop() })
		<-errc
	})
	return sim
}

func typeText(sim tcell.SimulationScreen, s string) {
	for _, r := range s {
		sim.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
}

func address(srv *httptest.Server) string {
	_, display, _ := riak.NormalizeAddress(srv.URL)
	return display
}

func TestSmokeBrowseAndLoadKey(t *testing.T) {
	stub := &stubRiak{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	app := New(srv.URL, Options{MaxKeys: 100, Timeout: 5 * time.Second, Version: "test"})
	sim := startApp(t, app)

	// startup: header, type list and key bar rendered, connection made
	waitFor(t, sim, app, "Riak Commander")
	waitFor(t, sim, app, "Bucket types")
	waitFor(t, sim, app, "Connected to "+address(srv))
	waitFor(t, sim, app, "Help")

	// Enter on "default" → bucket list
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "default › buckets")
	waitFor(t, sim, app, "users")

	// drill into users → key list shows alice with the cursor on it
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "alice")

	// open alice → JSON tree, metadata header and breadcrumb rendered
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "email")
	waitFor(t, sim, app, "email_bin")
	waitFor(t, sim, app, "EDITABLE")
	waitFor(t, sim, app, "default › users › alice")
}

func TestEditAndSavePreservesVclockAndIndexes(t *testing.T) {
	stub := &stubRiak{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	app := New(srv.URL, Options{MaxKeys: 100, Timeout: 5 * time.Second})
	sim := startApp(t, app)
	waitFor(t, sim, app, "Connected to")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "users")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "alice")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "EDITABLE")

	// tree root is selected; move to "email" and edit it
	sim.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyF4, 0, tcell.ModNone)
	waitFor(t, sim, app, "Edit $.email")
	sim.InjectKey(tcell.KeyCtrlU, 0, tcell.ModNone) // clear the field
	typeText(sim, "bob@example.com")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "MODIFIED")

	sim.InjectKey(tcell.KeyCtrlS, 0, tcell.ModNone)
	waitFor(t, sim, app, "Saved alice")

	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.puts) != 1 {
		t.Fatalf("expected 1 PUT, got %d", len(stub.puts))
	}
	put := stub.puts[0]
	if got := put.Header.Get("X-Riak-Vclock"); got != "vc1" {
		t.Errorf("vclock not round-tripped: %q", got)
	}
	if got := put.Header.Get("X-Riak-Index-Email_bin"); got != "alice@example.com" {
		t.Errorf("2i index not round-tripped: %q", got)
	}
	if !strings.Contains(stub.body[0], `"email": "bob@example.com"`) || !strings.Contains(stub.body[0], `"plan"`) {
		t.Errorf("unexpected body: %s", stub.body[0])
	}
}

func TestNoAddressReconnectsFromHistoryPicker(t *testing.T) {
	stub := &stubRiak{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	hist, err := history.Load(filepath.Join(t.TempDir(), "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	hist.Touch(address(srv), time.Now().Add(-time.Hour))
	hist.SetLabel(address(srv), "stub cluster")

	app := New("", Options{Timeout: 5 * time.Second, History: hist})
	sim := startApp(t, app)

	// the picker opens with the recent server selected; Enter connects
	waitFor(t, sim, app, "Recent servers")
	waitFor(t, sim, app, "stub cluster")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "Connected to "+address(srv))

	reloaded, err := history.Load(hist.Path())
	if err != nil {
		t.Fatal(err)
	}
	last, ok := reloaded.Last()
	if !ok || last.Address != address(srv) || last.UseCount != 2 {
		t.Fatalf("history not updated: %+v", last)
	}
}

func TestConnectFailureOpensPickerWithExplanation(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := address(srv)
	srv.Close() // nothing listens there any more

	hist := history.Disabled()
	app := New(addr, Options{Timeout: 2 * time.Second, History: hist})
	sim := startApp(t, app)

	waitFor(t, sim, app, "Nothing is listening on "+addr)
	waitFor(t, sim, app, "not connected")
	if _, ok := hist.Last(); ok {
		t.Fatal("a failed connection must not be recorded in the history")
	}
}

func TestBucketTypesAreRememberedPerServer(t *testing.T) {
	stub := &stubRiak{}
	mux := http.NewServeMux()
	mux.Handle("/", stub.handler())
	mux.HandleFunc("/types/sessions/buckets", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"buckets":["web"]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	hist := history.Disabled()
	app := New(srv.URL, Options{Timeout: 5 * time.Second, History: hist})
	sim := startApp(t, app)
	waitFor(t, sim, app, "Connected to")

	// "other bucket type…" is the last row: default, then the prompt
	sim.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "Open a bucket type")
	typeText(sim, "sessions")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "sessions › buckets")
	waitFor(t, sim, app, "web")

	e, _ := hist.Get(address(srv))
	if len(e.BucketTypes) != 1 || e.BucketTypes[0] != "sessions" {
		t.Fatalf("bucket type not remembered: %+v", e)
	}
	// back at the type list, the remembered type is listed
	sim.InjectKey(tcell.KeyLeft, 0, tcell.ModNone)
	waitFor(t, sim, app, "◆ sessions")
}

func TestHelpOpensOnContextPage(t *testing.T) {
	stub := &stubRiak{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	app := New(srv.URL, Options{Timeout: 5 * time.Second, Version: "v9.9.9"})
	sim := startApp(t, app)
	waitFor(t, sim, app, "Connected to")

	typeText(sim, "?")
	waitFor(t, sim, app, "Riak Commander v9.9.9 · help")
	waitFor(t, sim, app, "What this is")
	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	waitFor(t, sim, app, "Recent servers")
	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	waitFor(t, sim, app, "Bucket types")
}
