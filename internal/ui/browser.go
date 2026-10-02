package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/shushunov/riak-commander/internal/riak"
)

type browserLevel int

const (
	levelTypes browserLevel = iota
	levelBuckets
	levelKeys
)

// rowKind distinguishes real items from the synthetic navigation rows.
type rowKind int

const (
	rowItem      rowKind = iota // a type, bucket or key
	rowUp                       // ".." — go up one level
	rowOtherType                // prompt for a bucket type by name
	rowMore                     // load the next page of keys
	rowConnect                  // open the server picker (no connection yet)
)

type row struct {
	kind  rowKind
	value string
}

// Browser is the left pane: a drillable list of types → buckets → keys.
type Browser struct {
	app    *App
	layout *tview.Flex     // bordered frame: list + optional filter line
	list   *tview.List     // focus target
	filter *tview.TextView // "/ text▏" line while filtering

	level  browserLevel
	btype  string
	bucket string

	items     []string // real items at this level (no synthetic rows)
	rows      []row    // what the list currently shows, index-aligned
	truncated bool     // key listing hit the cap
	keyCap    int

	filterText string
	filterMode bool

	inQuery   bool // showing 2i results instead of a key listing
	queryDesc string

	propsCache map[string]map[string]any // "type/bucket" → bucket props
}

func newBrowser(a *App) *Browser {
	b := &Browser{app: a, propsCache: map[string]map[string]any{}}
	b.list = tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(true).SetWrapAround(false)
	styleList(b.list)
	b.list.SetSelectedFunc(func(i int, _, _ string, _ rune) { b.activate(i) })
	b.list.SetChangedFunc(func(i int, _, _ string, _ rune) { b.restyle(i) })
	b.list.SetInputCapture(b.handleKey)

	b.filter = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	b.filter.SetBackgroundColor(th.SurfaceAlt)

	b.layout = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(b.list, 0, 1, true).
		AddItem(b.filter, 0, 0, false)
	styleBox(b.layout.Box)
	b.layout.SetBorder(true).SetTitleAlign(tview.AlignLeft)
	return b
}

func (b *Browser) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	if b.filterMode {
		switch ev.Key() {
		case tcell.KeyEscape:
			b.filterMode, b.filterText = false, ""
			b.render()
			return nil
		case tcell.KeyEnter:
			b.filterMode = false
			b.render()
			return nil
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if r := []rune(b.filterText); len(r) > 0 {
				b.filterText = string(r[:len(r)-1])
			}
			b.render()
			return nil
		case tcell.KeyRune:
			b.filterText += string(ev.Rune())
			b.render()
			return nil
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyHome, tcell.KeyEnd:
			return ev // move through the filtered list while typing
		}
		return nil
	}
	switch {
	case ev.Key() == tcell.KeyLeft, ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		b.up()
		return nil
	case ev.Key() == tcell.KeyRight:
		b.activate(b.list.GetCurrentItem())
		return nil
	case ev.Key() == tcell.KeyEscape && (b.filterText != "" || b.inQuery):
		if b.filterText != "" {
			b.filterText = ""
			b.render()
		} else {
			b.exitQueryMode()
		}
		return nil
	case ev.Rune() == '/':
		b.filterMode = true
		b.render()
		return nil
	case ev.Rune() == 'j':
		return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
	case ev.Rune() == 'k':
		return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
	}
	return ev
}

// ---- rendering ----

func (b *Browser) render() {
	prev := b.list.GetCurrentItem()
	b.list.Clear()
	b.rows = b.rows[:0]
	add := func(r row) {
		b.rows = append(b.rows, r)
		b.list.AddItem("", "", 0, nil)
	}

	switch b.level {
	case levelTypes:
		if b.app.client == nil {
			add(row{kind: rowConnect})
		}
	default:
		add(row{kind: rowUp})
	}

	shown := 0
	if b.level != levelTypes || b.app.client != nil {
		needle := strings.ToLower(b.filterText)
		for _, it := range b.items {
			if needle == "" || strings.Contains(strings.ToLower(it), needle) {
				add(row{kind: rowItem, value: it})
				shown++
			}
		}
	}
	if b.level == levelTypes && b.app.client != nil && b.filterText == "" {
		add(row{kind: rowOtherType})
	}
	if b.level == levelKeys && b.truncated && b.filterText == "" && !b.inQuery {
		add(row{kind: rowMore})
	}

	b.layout.SetTitle(b.title(shown))
	b.renderFilter(shown)

	if prev >= 0 && prev < b.list.GetItemCount() {
		b.list.SetCurrentItem(prev)
	}
	b.restyleAll()
}

func (b *Browser) title(shown int) string {
	var t string
	switch b.level {
	case levelTypes:
		t = "Bucket types"
	case levelBuckets:
		t = fmt.Sprintf("%s › buckets", orDefault(b.btype))
	case levelKeys:
		if b.inQuery {
			t = fmt.Sprintf("%s › 2i results", b.bucket)
		} else {
			t = fmt.Sprintf("%s › keys", b.bucket)
		}
	}
	if b.filterText != "" {
		t += fmt.Sprintf(" (%d of %d)", shown, len(b.items))
	}
	return " " + tview.Escape(t) + " "
}

func (b *Browser) renderFilter(shown int) {
	if !b.filterMode && b.filterText == "" {
		b.layout.ResizeItem(b.filter, 0, 0)
		return
	}
	b.layout.ResizeItem(b.filter, 1, 0)
	cursor := ""
	if b.filterMode {
		cursor = tAccent() + "▏" + reset
	}
	match := ""
	if b.filterText != "" && shown == 0 {
		match = "  " + tWarning() + "no match" + reset
	}
	help := tMuted() + "  Enter keep · Esc clear" + reset
	if !b.filterMode {
		help = tMuted() + "  / edit · Esc clear" + reset
	}
	b.filter.SetText(tAccent() + " / " + reset + tText() + tview.Escape(b.filterText) + reset + cursor + match + help)
}

// label formats a row; selected rows are rendered without colour tags so the
// selection style is not overridden by per-segment colours.
func (b *Browser) label(r row, selected bool) string {
	glyph, text, note := "", "", ""
	glyphColour, textColour := tAccent(), tText()
	switch r.kind {
	case rowUp:
		glyph, text, textColour = "↩", "..", tMuted()
	case rowOtherType:
		glyph, text, textColour = "+", "other bucket type…", tMuted()
	case rowMore:
		glyph, text = "↓", fmt.Sprintf("load next %d keys…", b.app.opts.MaxKeys)
		textColour = tAccent()
	case rowConnect:
		glyph, text = "→", "connect to a server…"
		textColour = tAccent()
	default:
		text = r.value
		switch b.level {
		case levelTypes:
			glyph = "◆"
			if r.value == "default" {
				note = "untyped buckets"
			}
		case levelBuckets:
			glyph = "▤"
		case levelKeys:
			glyph, glyphColour = "·", tMuted()
		}
	}
	if selected || th.Mono {
		s := " " + glyph + " " + tview.Escape(text)
		if note != "" {
			s += "  " + note
		}
		return s
	}
	s := " " + glyphColour + glyph + reset + " " + textColour + tview.Escape(text) + reset
	if note != "" {
		s += "  " + tMuted() + note + reset
	}
	return s
}

func (b *Browser) restyleAll() {
	cur := b.list.GetCurrentItem()
	for i, r := range b.rows {
		b.list.SetItemText(i, b.label(r, i == cur), "")
	}
}

// restyle is the list's changed callback: re-label the old and new current
// rows.
func (b *Browser) restyle(cur int) {
	for i, r := range b.rows {
		if i >= b.list.GetItemCount() {
			break // render is still adding items
		}
		want := b.label(r, i == cur)
		if main, _ := b.list.GetItemText(i); main != want {
			b.list.SetItemText(i, want, "")
		}
	}
}

// renderFresh renders a newly entered level with the cursor at the top
// (skipping the synthetic ".." entry when real items exist).
func (b *Browser) renderFresh() {
	b.list.SetCurrentItem(0)
	b.render()
	if b.list.GetItemCount() > 1 && b.level != levelTypes {
		b.list.SetCurrentItem(1)
	} else {
		b.list.SetCurrentItem(0)
	}
	b.restyleAll()
}

// currentRow returns the row under the cursor.
func (b *Browser) currentRow() (row, bool) {
	i := b.list.GetCurrentItem()
	if i < 0 || i >= len(b.rows) {
		return row{}, false
	}
	return b.rows[i], true
}

// currentItem returns the real item under the cursor ("" on synthetic rows).
func (b *Browser) currentItem() string {
	if r, ok := b.currentRow(); ok && r.kind == rowItem {
		return r.value
	}
	return ""
}

// ---- navigation ----

func (b *Browser) showTypes() {
	b.level, b.items = levelTypes, b.app.bucketTypes()
	b.btype, b.bucket, b.filterText, b.filterMode, b.inQuery = "", "", "", false, false
	b.renderFresh()
}

func (b *Browser) activate(i int) {
	if i < 0 || i >= len(b.rows) {
		return
	}
	r := b.rows[i]
	switch r.kind {
	case rowUp:
		b.up()
		return
	case rowOtherType:
		b.promptOtherType()
		return
	case rowConnect:
		b.app.serverPicker("")
		return
	case rowMore:
		b.keyCap += b.app.opts.MaxKeys
		b.loadKeys()
		return
	}
	switch b.level {
	case levelTypes:
		b.loadBuckets(r.value)
	case levelBuckets:
		b.enterBucket(r.value)
	case levelKeys:
		b.app.viewer.loadKey(b.btype, b.bucket, r.value)
	}
}

func (b *Browser) up() {
	b.filterText, b.filterMode = "", false
	switch b.level {
	case levelKeys:
		if b.inQuery {
			b.exitQueryMode()
			return
		}
		b.app.viewer.dirtyGuard(func() {
			b.app.viewer.clear()
			b.loadBuckets(b.btype)
		})
	case levelBuckets:
		b.showTypes()
	default:
		b.render()
	}
}

func (b *Browser) refresh() {
	switch b.level {
	case levelTypes:
		b.showTypes()
	case levelBuckets:
		b.loadBuckets(b.btype)
	case levelKeys:
		if b.inQuery {
			b.exitQueryMode()
		} else {
			b.loadKeys()
		}
	}
}

func (b *Browser) promptOtherType() {
	b.app.form(formSpec{
		title: "Open a bucket type",
		hint: "Riak's HTTP API cannot list bucket types, so enter one by name " +
			"(see `riak-admin bucket-type list`). Types you open are remembered for this server.",
		build: func(f *tview.Form) {
			f.AddInputField("Type", "", 40, nil, nil)
			placeholder(f, "Type", "e.g. sessions")
		},
		onOK: func(f *tview.Form) error {
			t := strings.TrimSpace(inputText(f, "Type"))
			if t == "" {
				return fmt.Errorf("enter a bucket type name")
			}
			b.loadBuckets(t)
			return nil
		},
	})
}

// forgetType removes a remembered bucket type from this server's history.
func (b *Browser) forgetType() {
	t := b.currentItem()
	if b.level != levelTypes || t == "" {
		return
	}
	if !b.app.isRememberedType(t) {
		b.app.status.Warn("%q is not a remembered type (default and --types entries are always listed)", t)
		return
	}
	b.app.confirm("Forget bucket type",
		fmt.Sprintf("Remove %q from the type list for this server?\n\nNo data is deleted; the type is only forgotten by Riak Commander.", t),
		func() {
			b.app.hist.RemoveBucketType(b.app.client.Address(), t)
			b.app.saveHistory()
			b.showTypes()
			b.app.status.Success("Forgot bucket type %q", t)
		})
}

// ---- data loading ----

func (b *Browser) loadBuckets(btype string) {
	b.app.async("listing buckets in "+orDefault(btype), 0, func(ctx context.Context) (any, error) {
		return b.app.client.ListBuckets(ctx, btype)
	}, func(res any, err error) {
		if err != nil {
			return
		}
		buckets := res.([]string)
		b.level, b.btype, b.bucket = levelBuckets, btype, ""
		b.items, b.filterText, b.filterMode, b.inQuery = buckets, "", false, false
		b.renderFresh()
		if btype != "default" && btype != "" {
			if b.app.hist.AddBucketType(b.app.client.Address(), btype) {
				b.app.saveHistory()
			}
		}
		if len(buckets) == 0 {
			b.app.status.Info("No buckets in type %q (Riak lists only buckets that contain keys)", orDefault(btype))
		} else {
			b.app.status.Success("%s in type %q", pluralize(len(buckets), "bucket", "buckets"), orDefault(btype))
		}
	})
}

func (b *Browser) enterBucket(bucket string) {
	b.bucket = bucket
	b.keyCap = b.app.opts.MaxKeys
	b.fetchProps()
	b.loadKeys()
}

func (b *Browser) loadKeys() { b.loadKeysThen(nil) }

// loadKeysThen lists keys and, on success, runs then (may be nil).
func (b *Browser) loadKeysThen(then func()) {
	btype, bucket, keyCap := b.btype, b.bucket, b.keyCap
	b.app.async("listing keys in "+bucket, 4*b.app.client.Timeout(), func(ctx context.Context) (any, error) {
		keys, truncated, err := b.app.client.ListKeys(ctx, btype, bucket, keyCap)
		return []any{keys, truncated}, err
	}, func(res any, err error) {
		if err != nil {
			return
		}
		pair := res.([]any)
		b.level = levelKeys
		b.items, b.truncated = pair[0].([]string), pair[1].(bool)
		b.filterText, b.filterMode, b.inQuery = "", false, false
		b.renderFresh()
		if b.truncated {
			b.app.status.Info("Showing the first %d keys of %s; select “load next” for more", keyCap, bucket)
		} else {
			b.app.status.Success("%s in %s", pluralize(len(b.items), "key", "keys"), bucket)
		}
		if then != nil {
			then()
		}
	})
}

// fetchProps loads bucket props in the background; they route CRDT buckets
// to the datatype API and feed the props dialog. Failure is non-fatal: the
// viewer fetches them itself when needed.
func (b *Browser) fetchProps() {
	key := b.btype + "/" + b.bucket
	if _, ok := b.propsCache[key]; ok {
		return
	}
	btype, bucket := b.btype, b.bucket
	client := b.app.client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), client.Timeout())
		defer cancel()
		props, err := client.BucketProps(ctx, btype, bucket)
		if err != nil {
			return
		}
		b.app.tv.QueueUpdateDraw(func() {
			if b.app.client == client {
				b.propsCache[key] = props
			}
		})
	}()
}

// showQueryResults swaps the key list for 2i query results.
func (b *Browser) showQueryResults(desc string, res *riak.IndexResult) {
	b.level = levelKeys
	if b.keyCap < b.app.opts.MaxKeys {
		b.keyCap = b.app.opts.MaxKeys // Esc returns to a bounded key listing
	}
	b.inQuery, b.queryDesc = true, desc
	b.items, b.truncated, b.filterText, b.filterMode = res.Keys, false, "", false
	b.renderFresh()
	note := ""
	if res.Continuation != "" {
		note = " (more exist; raise “Max results” to see them)"
	}
	b.app.status.Success("2i %s: %s%s", desc, pluralize(len(res.Keys), "key", "keys"), note)
}

func (b *Browser) exitQueryMode() {
	b.inQuery, b.queryDesc = false, ""
	b.loadKeys()
}

func bucketKey(btype, bucket string) string { return orDefault(btype) + "/" + bucket }
