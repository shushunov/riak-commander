package ui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// findStub serves bucket "users" with 50 JSON objects u1…u50 (plan "business"
// for every 10th, "free" otherwise), one binary value and one key with
// siblings. slowAfter > 0 makes GETs of u<n> with n > slowAfter block until
// release is closed. With searchIndex set, the bucket has that Riak Search
// index and /search/query returns u10 and u20.
type findStub struct {
	slowAfter   int
	release     chan struct{}
	searchIndex string
}

func (s *findStub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "OK") })
	mux.HandleFunc("/buckets", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"buckets":["users"]}`)
	})
	mux.HandleFunc("/buckets/users/keys", func(w http.ResponseWriter, r *http.Request) {
		keys := []string{`"bin"`, `"conflict"`}
		for i := 1; i <= 50; i++ {
			keys = append(keys, fmt.Sprintf(`"u%d"`, i))
		}
		fmt.Fprintf(w, `{"keys":[%s]}`, strings.Join(keys, ","))
	})
	mux.HandleFunc("/buckets/users/props", func(w http.ResponseWriter, r *http.Request) {
		idx := s.searchIndex
		if idx == "" {
			idx = dontIndex
		}
		fmt.Fprintf(w, `{"props":{"name":"users","search_index":%q}}`, idx)
	})
	mux.HandleFunc("/buckets/users/keys/", func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/buckets/users/keys/")
		switch key {
		case "bin":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte{0, 1, 2, 3})
			return
		case "conflict":
			w.Header().Set("X-Riak-Vclock", "vc")
			w.WriteHeader(http.StatusMultipleChoices)
			fmt.Fprint(w, "Siblings:\nv1\nv2\n")
			return
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(key, "u"))
		if s.slowAfter > 0 && n > s.slowAfter {
			select {
			case <-s.release:
			case <-r.Context().Done():
				return
			}
		}
		plan := "free"
		if n%10 == 0 {
			plan = "business"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"plan":{"name":%q},"n":%d}`, plan, n)
	})
	mux.HandleFunc("/search/query/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"response":{"numFound":2,"docs":[
			{"_yz_rk":"u10","_yz_rb":"users","_yz_rt":"default"},
			{"_yz_rk":"u20","_yz_rb":"users","_yz_rt":"default"}]}}`)
	})
	return mux
}

// openUsersKeys connects and opens default/users.
func openUsersKeys(t *testing.T, sim tcell.SimulationScreen, app *App) {
	t.Helper()
	waitFor(t, sim, app, "Connected to")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "▤ users")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "52 keys in users")
}

func TestFindScanListsMatchesWithValues(t *testing.T) {
	stub := &findStub{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	app := New(srv.URL, Options{Timeout: 5 * time.Second})
	sim := startApp(t, app)
	openUsersKeys(t, sim, app)

	typeText(sim, "f")
	waitFor(t, sim, app, "Find in default/users")
	if screenHas(sim, app, "Using") {
		t.Fatal("Riak Search mode offered for a bucket without a search index")
	}
	typeText(sim, "plan.name")
	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone) // Match (equals)
	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone) // Value
	typeText(sim, "business")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)

	waitFor(t, sim, app, "users › find results")
	waitFor(t, sim, app, "Checked 52 keys · 5 matches · 2 skipped")
	for _, k := range []string{"· u10", "· u20", "· u50"} {
		waitFor(t, sim, app, k)
	}
	waitFor(t, sim, app, `"business"`)
	if screenHas(sim, app, "· u11") {
		t.Fatal("non-matching key listed")
	}
	waitFor(t, sim, app, "find plan.name = business") // breadcrumb

	// Esc goes back to the key list it came from, without re-listing
	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	waitFor(t, sim, app, "users › keys")
	waitFor(t, sim, app, "· conflict")
}

func TestFindScanEscStopsAndKeepsMatches(t *testing.T) {
	stub := &findStub{slowAfter: 25, release: make(chan struct{})}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()
	defer close(stub.release)

	app := New(srv.URL, Options{Timeout: 5 * time.Second})
	sim := startApp(t, app)
	openUsersKeys(t, sim, app)

	typeText(sim, "f")
	waitFor(t, sim, app, "Find in default/users")
	typeText(sim, "plan.name")
	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	typeText(sim, "business")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)

	// u10 and u20 are found before the scan reaches the slow keys
	waitFor(t, sim, app, "· u20")
	waitFor(t, sim, app, "2 matches")
	waitFor(t, sim, app, "Esc stop")

	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	waitFor(t, sim, app, "Stopped after checking")
	waitFor(t, sim, app, "· u10")
	waitFor(t, sim, app, "users › find results")

	// a second Esc returns to the key list
	sim.InjectKey(tcell.KeyEscape, 0, tcell.ModNone)
	waitFor(t, sim, app, "users › keys")
}

func TestFindRegexErrorShownInDialog(t *testing.T) {
	stub := &findStub{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	app := New(srv.URL, Options{Timeout: 5 * time.Second})
	sim := startApp(t, app)
	openUsersKeys(t, sim, app)

	typeText(sim, "f")
	waitFor(t, sim, app, "Find in default/users")
	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone)   // Match
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone) // open
	sim.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone) // regex
	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone)   // Value
	typeText(sim, "([")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	waitFor(t, sim, app, "invalid regular expression")
	waitFor(t, sim, app, "Find in default/users") // still open
}

func TestFindExistsHidesValueField(t *testing.T) {
	stub := &findStub{}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	app := New(srv.URL, Options{Timeout: 5 * time.Second})
	sim := startApp(t, app)
	openUsersKeys(t, sim, app)

	typeText(sim, "f")
	waitFor(t, sim, app, "│  Value ")
	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)
	for i := 0; i < 3; i++ {
		sim.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	}
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone) // exists
	time.Sleep(300 * time.Millisecond)
	waitFor(t, sim, app, "Match      exists")
	if screenHas(sim, app, "│  Value ") { // the dialog row, not the value pane's title
		t.Fatal("Value field shown for Match: exists")
	}
}

func TestFindRiakSearchMode(t *testing.T) {
	stub := &findStub{searchIndex: "users_idx"}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	app := New(srv.URL, Options{Timeout: 5 * time.Second})
	sim := startApp(t, app)
	openUsersKeys(t, sim, app)

	typeText(sim, "f")
	waitFor(t, sim, app, "Using")
	sim.InjectKey(tcell.KeyBacktab, 0, tcell.ModNone) // Field → Using
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)   // open it
	sim.InjectKey(tcell.KeyDown, 0, tcell.ModNone)
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone) // Riak Search
	waitFor(t, sim, app, "Riak Search (users_idx)")
	waitFor(t, sim, app, "Query")
	sim.InjectKey(tcell.KeyTab, 0, tcell.ModNone) // Using → Query
	typeText(sim, "plan.name_s:business")
	sim.InjectKey(tcell.KeyEnter, 0, tcell.ModNone)

	waitFor(t, sim, app, "users › search results")
	waitFor(t, sim, app, "Riak Search: 2 matches")
	waitFor(t, sim, app, "· u20")
}
