// Package ui implements the Midnight Commander-style TUI: a header with the
// connection and location, a browser pane on the left (bucket types →
// buckets → keys), a value viewer/editor on the right, a status line and a
// context-sensitive key bar.
//
// All widget state is owned by the tview event goroutine. Network calls run
// through App.async, which executes them on a worker goroutine and applies
// the result back on the event goroutine via QueueUpdateDraw.
package ui

import (
	"context"
	"sort"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/shushunov/riak-commander/internal/history"
	"github.com/shushunov/riak-commander/internal/riak"
)

// DefaultAddress is offered in the server picker when nothing better is known.
const DefaultAddress = "127.0.0.1:8098"

// connectTimeout bounds the ping performed before switching servers.
const connectTimeout = 5 * time.Second

// Options configures the UI.
type Options struct {
	MaxKeys    int            // key-listing page size
	Timeout    time.Duration  // per-request timeout for new connections
	Theme      Theme          // colour palette
	History    *history.Store // server history (history.Disabled() to opt out)
	ExtraTypes []string       // bucket types from --types, always listed
	Version    string         // shown in the help screen
}

type connState int

const (
	connNone       connState = iota // no server chosen yet
	connConnecting                  // ping in flight
	connUp                          // a server answered
	connDown                        // last attempt failed
)

// App wires the panes together and owns the connection.
type App struct {
	tv     *tview.Application
	client *riak.Client // nil until the first successful connection
	opts   Options
	hist   *history.Store

	pages   *tview.Pages // "main" + at most one "modal"
	header  *Header
	browser *Browser
	viewer  *Viewer
	status  *StatusBar

	conn          connState
	connAddress   string // address being connected to / last tried
	failedAddress string // last address that failed, prefilled in the picker

	loading   bool
	modalOpen bool
	lastFocus tview.Primitive

	// per-bucket 2i conveniences, keyed "type/bucket"
	seenIndexes map[string]map[string]bool // index names seen on loaded objects
	lastQuery   map[string]indexQuery      // last query run in the bucket

	// startAddress is connected to by Run (empty: open the server picker)
	startAddress string
}

// New builds the UI. address is the server to connect to on Run; pass ""
// to start with the server picker instead.
func New(address string, opts Options) *App {
	if opts.MaxKeys <= 0 {
		opts.MaxKeys = 1000
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	if opts.History == nil {
		opts.History = history.Disabled()
	}
	if opts.Theme.Name == "" {
		opts.Theme = DarkTheme
	}
	th = opts.Theme
	th.apply()

	a := &App{
		tv:           tview.NewApplication(),
		opts:         opts,
		hist:         opts.History,
		seenIndexes:  map[string]map[string]bool{},
		lastQuery:    map[string]indexQuery{},
		startAddress: address,
	}
	a.header = newHeader()
	a.status = newStatusBar(a)
	a.browser = newBrowser(a)
	a.viewer = newViewer(a)

	panes := tview.NewFlex().
		AddItem(a.browser.layout, 0, 1, true).
		AddItem(a.viewer.layout, 0, 2, false)

	root := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header.view, 1, 0, false).
		AddItem(panes, 0, 1, true).
		AddItem(a.status.row, 1, 0, false).
		AddItem(a.status.keybar, 1, 0, false)

	a.pages = tview.NewPages().AddPage("main", root, true, true)
	a.tv.SetRoot(a.pages, true)
	a.tv.EnableMouse(true)
	a.tv.SetInputCapture(a.handleKey)
	// Chrome (header, borders, key bar) is derived from state right before
	// every draw, so no code path can forget to refresh it. The callback runs
	// under the application lock: it must not call a.tv methods.
	a.tv.SetBeforeDrawFunc(func(tcell.Screen) bool {
		a.syncChrome()
		return false
	})
	return a
}

// Run starts the event loop; it returns when the user quits.
func (a *App) Run() error {
	a.browser.showTypes()
	if a.startAddress != "" {
		a.connect(a.startAddress, nil)
	} else {
		a.status.Info("Welcome! Pick a server to connect to.")
		a.serverPicker("")
	}
	return a.tv.Run()
}

// SetScreen is used by tests to run against a simulation screen.
func (a *App) SetScreen(screen tcell.Screen) { a.tv.SetScreen(screen) }

// connect pings address and, on success, makes it the active server, records
// it in the history and resets both panes. On failure the current connection
// (if any) is kept and the server picker reopens with the error. onDone, if
// set, runs after a successful switch.
func (a *App) connect(address string, onDone func()) {
	candidate, err := riak.NewClient(address, a.opts.Timeout)
	if err != nil {
		a.status.Err(err)
		a.failedAddress = address
		a.serverPicker(err.Error())
		return
	}
	prevState, prevAddress := a.conn, a.connAddress
	a.conn, a.connAddress = connConnecting, candidate.Address()
	a.async("connecting to "+candidate.Address(), connectTimeout, func(ctx context.Context) (any, error) {
		return nil, candidate.Ping(ctx)
	}, func(_ any, err error) {
		if err != nil {
			if a.client != nil && prevState == connUp {
				a.conn, a.connAddress = connUp, a.client.Address() // keep the old server
			} else {
				a.conn = connDown
			}
			a.failedAddress = candidate.Address()
			a.serverPicker(friendlyConnError(candidate.Address(), err))
			return
		}
		// Until unsaved edits are dealt with, the old connection stays active.
		a.conn, a.connAddress = prevState, prevAddress
		a.viewer.dirtyGuard(func() {
			a.client = candidate
			a.conn, a.connAddress = connUp, candidate.Address()
			a.hist.Touch(candidate.Address(), time.Now())
			a.saveHistory()
			a.viewer.clear()
			a.browser.propsCache = map[string]map[string]any{}
			a.seenIndexes = map[string]map[string]bool{}
			a.lastQuery = map[string]indexQuery{}
			a.browser.showTypes()
			a.focusBrowser()
			a.status.Success("Connected to %s", candidate.Address())
			if onDone != nil {
				onDone()
			}
		})
	})
}

// saveHistory persists the server history, surfacing (but not failing on)
// write errors.
func (a *App) saveHistory() {
	if err := a.hist.Save(); err != nil {
		a.status.Warn("%v", err)
	}
}

// bucketTypes is the type list for the current server: "default", the
// --types flag, then types remembered for this server (types cannot be
// enumerated over HTTP).
func (a *App) bucketTypes() []string {
	seen := map[string]bool{"default": true}
	out := []string{"default"}
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range a.opts.ExtraTypes {
		add(t)
	}
	if a.client != nil {
		if e, ok := a.hist.Get(a.client.Address()); ok {
			rest := append([]string(nil), e.BucketTypes...)
			sort.Strings(rest)
			for _, t := range rest {
				add(t)
			}
		}
	}
	return out
}

// isRememberedType reports whether btype came from the history (and can
// therefore be forgotten), as opposed to "default" or --types.
func (a *App) isRememberedType(btype string) bool {
	if btype == "default" || a.client == nil {
		return false
	}
	for _, t := range a.opts.ExtraTypes {
		if t == btype {
			return false
		}
	}
	e, _ := a.hist.Get(a.client.Address())
	for _, t := range e.BucketTypes {
		if t == btype {
			return true
		}
	}
	return false
}

// async runs a network call off the UI goroutine and applies the result back
// on it. A single in-flight call at a time keeps pane state simple.
func (a *App) async(desc string, timeout time.Duration, fn func(ctx context.Context) (any, error), done func(res any, err error)) {
	if a.loading {
		a.status.Warn("Busy: %s is still in progress", a.status.busyDesc)
		return
	}
	a.loading = true
	a.status.startBusy(desc)
	if timeout == 0 {
		timeout = a.opts.Timeout
		if a.client != nil {
			timeout = a.client.Timeout()
		}
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		res, err := fn(ctx)
		a.tv.QueueUpdateDraw(func() {
			a.loading = false
			a.status.stopBusy()
			if err != nil {
				a.status.Err(err)
			}
			done(res, err)
		})
	}()
}

// ---- focus ----

func (a *App) browserFocused() bool { return a.browser.list.HasFocus() }

func (a *App) viewerFocused() bool {
	return a.viewer.tree.HasFocus() || a.viewer.text.HasFocus()
}

func (a *App) focusBrowser() { a.tv.SetFocus(a.browser.list) }

func (a *App) focusViewer() { a.tv.SetFocus(a.viewer.focusTarget()) }

func (a *App) toggleFocus() {
	if a.browserFocused() {
		a.focusViewer()
	} else {
		a.focusBrowser()
	}
}

// syncChrome refreshes everything derived from state: header, pane borders,
// selection colours and the key bar. Runs before every draw.
func (a *App) syncChrome() {
	bFocus, vFocus := a.browserFocused(), a.viewerFocused()
	border := func(b *tview.Box, focused bool) {
		if focused {
			b.SetBorderColor(th.Accent)
			b.SetTitleColor(th.Accent)
			if th.Mono {
				b.SetBorderAttributes(tcell.AttrBold)
			}
		} else {
			b.SetBorderColor(th.Border)
			b.SetTitleColor(th.Muted)
			if th.Mono {
				b.SetBorderAttributes(tcell.AttrNone)
			}
		}
	}
	border(a.browser.layout.Box, bFocus)
	border(a.viewer.layout.Box, vFocus)
	a.browser.list.SetSelectedStyle(selectedStyle(bFocus))
	a.viewer.syncSelection(vFocus)

	a.header.render(a)
	a.status.setKeys(a.keyHints())
	a.status.setInfo(a.infoText())
}

// infoText is the right-hand side of the status line.
func (a *App) infoText() string {
	b := a.browser
	switch {
	case a.client == nil:
		return ""
	case b.level == levelKeys && b.inQuery:
		return pluralize(len(b.items), "result", "results")
	case b.level == levelKeys:
		s := pluralize(len(b.items), "key", "keys")
		if b.truncated {
			s += " (more available)"
		}
		return s
	case b.level == levelBuckets:
		return pluralize(len(b.items), "bucket", "buckets")
	}
	return ""
}

func (a *App) quit() {
	a.viewer.dirtyGuard(func() { a.tv.Stop() })
}
