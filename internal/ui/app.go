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
	"strings"
	"sync/atomic"
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
	run       *asyncRun    // the in-flight async call, if any
	pending   *pendingLoad // the in-flight navigation load, if any
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
	// While a navigation load is pending the panes are locked; clicks would
	// otherwise act on content that is about to be replaced.
	a.tv.SetMouseCapture(func(ev *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
		if a.pending != nil && !a.modalOpen {
			return nil, action
		}
		return ev, action
	})
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

// pendingKind says which pane a navigation load is filling.
type pendingKind int

const (
	pendingList  pendingKind = iota + 1 // the browser list (buckets, keys, 2i results)
	pendingValue                        // the value pane
)

// loadingDelay hides the in-pane loading indicator for loads that finish
// almost instantly, so fast clusters do not flash; input is locked at once.
const loadingDelay = 150 * time.Millisecond

// pendingLoad is a navigation load in flight. The pane has already switched
// to its target ("navigate first") and shows a loading row; restore puts the
// previous view back if the load fails or is cancelled.
type pendingLoad struct {
	kind     pendingKind
	desc     string // "Loading keys"
	started  time.Time
	progress atomic.Int64 // items received so far (key listings), set by the worker
	restore  func()
}

// elapsed reports how long the load has run and whether the loading
// indicator should be visible yet.
func (p *pendingLoad) elapsed() (time.Duration, bool) {
	d := time.Since(p.started)
	return d, d >= loadingDelay
}

// asyncRun is one in-flight async call.
type asyncRun struct {
	cancel    context.CancelFunc
	cancelled bool
}

// busy reports (and tells the user) whether another call is in flight.
// Navigation code checks it before switching panes.
func (a *App) busy() bool {
	if !a.loading {
		return false
	}
	if a.pending != nil {
		a.status.Info("Still loading. Press Esc to cancel")
	} else {
		a.status.Warn("Busy: %s is still in progress", a.status.busyDesc)
	}
	return true
}

// async runs a network call off the UI goroutine and applies the result back
// on it. A single in-flight call at a time keeps pane state simple.
func (a *App) async(desc string, timeout time.Duration, fn func(ctx context.Context) (any, error), done func(res any, err error)) {
	a.asyncPending(nil, desc, timeout, fn, done)
}

// asyncPending is async for navigation loads: p (may be nil) marks the
// target pane as loading, which locks input until the call finishes or the
// user cancels it with Esc (see cancelPending). On failure p.restore runs
// before done.
func (a *App) asyncPending(p *pendingLoad, desc string, timeout time.Duration, fn func(ctx context.Context) (any, error), done func(res any, err error)) {
	if a.busy() {
		if p != nil && p.restore != nil {
			p.restore()
		}
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
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	run := &asyncRun{cancel: cancel}
	a.run = run
	if p != nil {
		p.started = time.Now()
		a.pending = p
	}
	go func() {
		defer cancel()
		res, err := fn(ctx)
		a.tv.QueueUpdateDraw(func() {
			a.loading = false
			a.run = nil
			a.status.stopBusy()
			if run.cancelled {
				return // cancelPending already restored the view and said so
			}
			if a.pending == p {
				a.pending = nil
			}
			if err != nil {
				a.status.Err(err)
				if p != nil && p.restore != nil {
					p.restore()
				}
			}
			done(res, err)
		})
	}()
}

// cancelPending aborts the in-flight navigation load and immediately puts the
// previous view back; the late result, if any, is discarded.
func (a *App) cancelPending() {
	p, run := a.pending, a.run
	if p == nil || run == nil {
		return
	}
	run.cancelled = true
	run.cancel()
	a.pending = nil
	if p.restore != nil {
		p.restore()
	}
	a.status.Info("Cancelled: %s", strings.ToLower(p.desc))
}

// newPending starts describing a navigation load; pass it to asyncPending.
func newPending(kind pendingKind, desc string, restore func()) *pendingLoad {
	return &pendingLoad{kind: kind, desc: desc, restore: restore}
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
	if p := a.pending; p != nil && p.kind == pendingList {
		// no highlight while loading: the list is inert until data arrives
		a.browser.list.SetSelectedStyle(tcell.StyleDefault.Background(th.Bg).Foreground(th.Text))
		a.browser.renderLoading()
	} else {
		a.browser.list.SetSelectedStyle(selectedStyle(bFocus))
	}
	if p := a.pending; p != nil && p.kind == pendingValue {
		a.viewer.renderLoading()
	} else {
		a.viewer.syncSelection(vFocus)
	}

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
	case a.pending != nil && a.pending.kind == pendingList:
		return "" // counts of the old or empty list would mislead
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
