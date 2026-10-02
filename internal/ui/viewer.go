package ui

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/shushunov/riak-commander/internal/jsontree"
	"github.com/shushunov/riak-commander/internal/riak"
)

type viewMode int

const (
	modeNone viewMode = iota
	modeTree          // editable JSON tree
	modeRaw           // pretty/raw text, read-only
	modeHex           // binary preview, read-only
	modeCRDT          // datatype JSON, read-only
)

// treeEditLimit: values larger than this open raw-only (tree rebuild cost).
const treeEditLimit = 1 << 20

// maxIndexLines caps the 2i lines shown in the value header.
const maxIndexLines = 4

// loaded is the state of the object shown in the right pane.
type loaded struct {
	obj      *riak.RiakObject
	root     *jsontree.Node
	nodeMap  map[*jsontree.Node]*tview.TreeNode
	mode     viewMode
	dirty    bool
	editable bool
	rawShown bool // F3 toggle: forced raw view of an editable object
	sibling  string
}

// Viewer is the right pane: metadata header + tree/text content.
type Viewer struct {
	app    *App
	layout *tview.Flex
	header *tview.TextView
	body   *tview.Pages
	tree   *tview.TreeView
	text   *tview.TextView

	cur *loaded

	selNode    *tview.TreeNode // node currently rendered without colour tags
	selFocused bool

	loadingKey  string // key being loaded while a value load is pending
	loadingLast string // last rendered loading text

	searchMode bool
	search     string
}

func newViewer(a *App) *Viewer {
	v := &Viewer{app: a}
	v.header = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.header.SetBackgroundColor(th.Bg)
	v.tree = tview.NewTreeView().SetGraphicsColor(th.Border)
	v.tree.SetBackgroundColor(th.Bg)
	v.text = tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(false)
	v.text.SetBackgroundColor(th.Bg)
	v.text.SetTextColor(th.Text)
	v.body = tview.NewPages().
		AddPage("text", v.text, true, true).
		AddPage("tree", v.tree, true, false)

	v.layout = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(v.header, 0, 0, false).
		AddItem(v.body, 0, 1, true)
	styleBox(v.layout.Box)
	v.layout.SetBorder(true).SetTitleAlign(tview.AlignLeft)

	v.tree.SetSelectedFunc(func(n *tview.TreeNode) {
		jn, _ := n.GetReference().(*jsontree.Node)
		if jn == nil {
			return
		}
		if jn.Kind == jsontree.Object || jn.Kind == jsontree.Array {
			n.SetExpanded(!n.IsExpanded())
		} else {
			a.editNode(jn)
		}
	})
	v.tree.SetChangedFunc(func(*tview.TreeNode) { v.syncSelection(v.selFocused) })
	v.tree.SetInputCapture(v.handleTreeKey)
	v.text.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Rune() {
		case 'j':
			return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
		case 'k':
			return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
		}
		return ev
	})
	v.showEmpty()
	return v
}

func (v *Viewer) focusTarget() tview.Primitive {
	if v.cur != nil && v.cur.mode == modeTree && !v.cur.rawShown {
		return v.tree
	}
	return v.text
}

// clear unloads the current value.
func (v *Viewer) clear() {
	v.cur = nil
	v.searchMode, v.search = false, ""
	v.showEmpty()
}

// showEmpty renders the getting-started panel.
func (v *Viewer) showEmpty() {
	v.layout.ResizeItem(v.header, 0, 0)
	v.layout.SetTitle(" Value ")
	k := func(s string) string { return tAccent() + tBold() + s + reset }
	m := func(s string) string { return tMuted() + s + reset }
	var b strings.Builder
	b.WriteString("\n  " + tBold() + tText() + "Riak Commander" + reset + m("  ·  browse and safely edit Riak KV") + "\n\n")
	if v.app.client == nil {
		b.WriteString("  " + tWarning() + "Not connected." + reset + " Press " + k("s") + " to pick or add a server.\n\n")
	}
	rows := [][2]string{
		{"↑ ↓  Enter", "move and open: bucket type → bucket → key"},
		{"← / Bksp", "go back up a level"},
		{"Tab", "switch between the list and this pane"},
		{"/", "filter the list, or search inside a JSON value"},
		{"F4  Enter", "edit the selected JSON field"},
		{"F2", "save (vclock and 2i indexes are kept)"},
		{"i", "query a secondary index (2i)"},
		{"s", "servers: recent connections, switch cluster"},
		{"F1  ?", "full help"},
	}
	for _, r := range rows {
		fmt.Fprintf(&b, "  %s%-12s%s %s\n", tAccent()+tBold(), r[0], reset, m(r[1]))
	}
	b.WriteString("\n  " + m("Nothing is written to Riak until you press F2 (or confirm a create/delete).") + "\n")
	v.text.SetText(b.String())
	v.text.ScrollToBeginning()
	v.body.SwitchToPage("text")
}

// ---- loading ----

// loadKey fetches and displays a key, first resolving unsaved edits.
func (v *Viewer) loadKey(btype, bucket, key string) {
	v.dirtyGuard(func() { v.doLoad(btype, bucket, key) })
}

func isCRDT(props map[string]any) bool {
	dt, _ := props["datatype"].(string)
	return dt != ""
}

type loadResult struct {
	obj   *riak.RiakObject
	crdt  []byte
	props map[string]any
}

// doLoad fetches a key. Bucket props decide which API to use: buckets whose
// type has a `datatype` property hold CRDTs, readable only through the
// datatypes endpoint. Props are fetched on demand if not cached yet.
func (v *Viewer) doLoad(btype, bucket, key string) {
	a := v.app
	if a.busy() {
		return
	}
	pkey := btype + "/" + bucket
	props := a.browser.propsCache[pkey]
	client := a.client
	// navigate first: the pane shows the key loading right away; on failure
	// or Esc the previous value (or the empty state) comes back
	p := newPending(pendingValue, "Loading "+key, func() { v.render() })
	v.beginLoading(key)
	a.asyncPending(p, "loading "+key, 0, func(ctx context.Context) (any, error) {
		res := loadResult{props: props}
		if res.props == nil {
			if p, err := client.BucketProps(ctx, btype, bucket); err == nil {
				res.props = p
			}
		}
		if isCRDT(res.props) {
			body, err := client.GetDatatype(ctx, btype, bucket, key)
			res.crdt = body
			return res, err
		}
		obj, err := client.GetObject(ctx, btype, bucket, key)
		res.obj = obj
		return res, err
	}, func(r any, err error) {
		res, _ := r.(loadResult)
		if res.props != nil && a.client == client {
			a.browser.propsCache[pkey] = res.props
		}
		if err != nil {
			if sib, ok := err.(*riak.ErrSiblings); ok {
				a.siblingDialog(btype, bucket, key, sib)
			} else if err == riak.ErrNotFound {
				a.status.Warn("%s no longer exists (deleted since the key list was loaded?)", key)
			}
			return
		}
		if res.crdt != nil {
			v.showCRDT(btype, bucket, key, res.crdt)
			a.status.Success("Loaded %s (CRDT %s, read-only)", key, res.props["datatype"])
			return
		}
		v.show(res.obj)
		a.status.Success("Loaded %s · %s · %s", key, humanBytes(len(res.obj.Body)),
			pluralize(len(res.obj.Indexes), "2i index", "2i indexes"))
	})
}

// show displays a fetched object, picking the right mode.
func (v *Viewer) show(obj *riak.RiakObject) {
	cur := &loaded{obj: obj}
	root, err := jsontree.Parse(obj.Body)
	switch {
	case err == nil && len(obj.Body) <= treeEditLimit:
		cur.mode, cur.root, cur.editable = modeTree, root, true
	case looksBinary(obj.Body):
		cur.mode = modeHex
	default:
		cur.mode = modeRaw
	}
	v.cur = cur
	v.searchMode, v.search = false, ""
	v.app.noteIndexes(obj)
	v.render()
	if cur.mode == modeTree {
		v.app.focusViewer()
	}
}

func (v *Viewer) showCRDT(btype, bucket, key string, body []byte) {
	obj := &riak.RiakObject{BucketType: btype, Bucket: bucket, Key: key, Body: body, ContentType: "application/json"}
	v.cur = &loaded{obj: obj, mode: modeCRDT}
	v.render()
}

// reloadCurrent re-fetches the object (F5).
func (v *Viewer) reloadCurrent() {
	if v.cur == nil {
		return
	}
	o := v.cur.obj
	v.dirtyGuard(func() { v.doLoad(o.BucketType, o.Bucket, o.Key) })
}

// beginLoading switches the pane to the key being loaded. It runs on the
// event goroutine (not in the draw hook) because switching pages may move
// focus.
func (v *Viewer) beginLoading(key string) {
	v.loadingKey, v.loadingLast = key, ""
	v.layout.SetTitle(" " + tview.Escape(key) + " ")
	v.layout.ResizeItem(v.header, 0, 0)
	v.text.SetText("")
	v.body.SwitchToPage("text")
}

// renderLoading shows a spinner and the elapsed time for the key being
// loaded. It runs before every draw while a value load is pending, so it
// only updates text. The pane stays blank for the first loadingDelay so
// instant loads do not flash.
func (v *Viewer) renderLoading() {
	p := v.app.pending
	d, visible := p.elapsed()
	if !visible {
		return
	}
	text := "\n\n  " + tAccent() + v.app.status.spinner() + reset + " " + tText() + tview.Escape(p.desc) + "…" + reset +
		"  " + tMuted() + fmt.Sprintf("%.1f s", d.Seconds()) + reset +
		"\n\n  " + tAccent() + "Esc" + reset + tMuted() + " cancel and go back" + reset
	if text != v.loadingLast {
		v.loadingLast = text
		v.text.SetText(text)
	}
}

// ---- rendering ----

func (v *Viewer) render() {
	c := v.cur
	if c == nil {
		v.showEmpty()
		return
	}
	obj := c.obj
	title := " " + tview.Escape(obj.Key) + " "
	if c.dirty {
		title = " " + tWarning() + "●" + reset + tview.Escape(obj.Key) + " "
	}
	v.layout.SetTitle(title)

	lines := v.headerLines()
	v.header.SetText(strings.Join(lines, "\n"))
	v.layout.ResizeItem(v.header, len(lines), 0)

	switch {
	case c.mode == modeTree && !c.rawShown:
		v.buildTree()
		v.body.SwitchToPage("tree")
	case c.mode == modeHex:
		v.text.SetText(hexPreview(obj.Body))
		v.text.ScrollToBeginning()
		v.body.SwitchToPage("text")
	default: // raw, CRDT, or forced-raw tree
		v.text.SetText(highlightJSON(prettyOrRaw(obj.Body, c)))
		v.text.ScrollToBeginning()
		v.body.SwitchToPage("text")
	}
}

// headerLines renders the metadata block above the value.
func (v *Viewer) headerLines() []string {
	c := v.cur
	obj := c.obj
	label := func(s string) string { return tMuted() + fmt.Sprintf("%-8s", s) + reset }
	badge := func(text string, colour tcell.Color) string {
		if th.Mono {
			return "[::r] " + text + " " + reset
		}
		return fgbg(th.OnAccent, colour) + tBold() + " " + text + " " + reset
	}

	var badges []string
	switch {
	case c.dirty:
		badges = append(badges, badge("MODIFIED · F2 save", th.Warning))
	case c.mode == modeTree && c.rawShown:
		badges = append(badges, badge("RAW VIEW · F3 tree", th.Accent))
	case c.mode == modeTree:
		badges = append(badges, badge("EDITABLE", th.Success))
	}
	switch c.mode {
	case modeRaw:
		badges = append(badges, badge("READ-ONLY · not JSON", th.Muted))
		if len(obj.Body) > treeEditLimit {
			badges[len(badges)-1] = badge("READ-ONLY · over 1 MiB", th.Muted)
		}
	case modeHex:
		badges = append(badges, badge("READ-ONLY · binary", th.Muted))
	case modeCRDT:
		badges = append(badges, badge("READ-ONLY · CRDT", th.Muted))
	}
	if c.sibling != "" {
		badges = append(badges, badge("SIBLING "+c.sibling+" · save resolves", th.Danger))
	}

	ct := obj.ContentType
	if ct == "" {
		ct = "(none)"
	}
	meta := tText() + tview.Escape(ct) + reset + tMuted() + "  ·  " + reset + tText() + humanBytes(len(obj.Body)) + reset
	if obj.LastMod != "" {
		meta += tMuted() + "  ·  modified " + reset + tText() + tview.Escape(relTime(obj.LastMod)) + reset
	}
	if c.mode != modeCRDT {
		if obj.Vclock != "" {
			meta += tMuted() + "  ·  vclock " + reset + tSuccess() + "✓" + reset
		} else {
			meta += tMuted() + "  ·  vclock " + reset + tWarning() + "none" + reset
		}
	}

	lines := []string{" " + strings.Join(badges, " "), " " + label("Object") + meta}

	names := make([]string, 0, len(obj.Indexes))
	for name := range obj.Indexes {
		names = append(names, name)
	}
	sort.Strings(names)
	switch {
	case c.mode == modeCRDT:
	case len(names) == 0:
		lines = append(lines, " "+label("2i")+tMuted()+"none"+reset)
	default:
		for i, name := range names {
			if i == maxIndexLines {
				lines = append(lines, " "+label("")+tMuted()+fmt.Sprintf("… %d more", len(names)-maxIndexLines)+reset)
				break
			}
			l := ""
			if i == 0 {
				l = "2i"
			}
			lines = append(lines, " "+label(l)+tAccent()+tview.Escape(name)+reset+tMuted()+" = "+reset+
				tText()+tview.Escape(truncate(strings.Join(obj.Indexes[name], ", "), 80))+reset)
		}
	}
	if len(obj.Meta) > 0 {
		keys := make([]string, 0, len(obj.Meta))
		for k := range obj.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = tAccent() + tview.Escape(k) + reset + tMuted() + "=" + reset + tText() + tview.Escape(truncate(obj.Meta[k], 40)) + reset
		}
		lines = append(lines, " "+label("Meta")+strings.Join(parts, "  "))
	}
	sep := strings.Repeat("─", 200)
	lines = append(lines, tMuted()+sep+reset)
	return lines
}

func prettyOrRaw(body []byte, c *loaded) string {
	if c.mode == modeTree && c.root != nil {
		return string(jsontree.Serialize(c.root))
	}
	if json.Valid(body) {
		var out json.RawMessage = body
		if pretty, err := json.MarshalIndent(out, "", "  "); err == nil {
			return string(pretty)
		}
	}
	return string(body)
}

// highlightJSON adds syntax colour to pretty-printed JSON (or escapes plain
// text unchanged). It is a light lexer: good enough for display, never used
// for writing.
func highlightJSON(s string) string {
	if th.Mono || !json.Valid([]byte(s)) {
		return tview.Escape(s)
	}
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case r == '"':
			j := i + 1
			for j < len(rs) && rs[j] != '"' {
				if rs[j] == '\\' {
					j++
				}
				j++
			}
			if j >= len(rs) {
				j = len(rs) - 1
			}
			str := string(rs[i : j+1])
			// a string followed by ':' is an object key
			k := j + 1
			for k < len(rs) && rs[k] == ' ' {
				k++
			}
			colour := fg(th.JSONString)
			if k < len(rs) && rs[k] == ':' {
				colour = fg(th.JSONKey)
			}
			b.WriteString(colour + tview.Escape(str) + reset)
			i = j + 1
		case r == '-' || (r >= '0' && r <= '9'):
			j := i
			for j < len(rs) && strings.ContainsRune("+-.eE0123456789", rs[j]) {
				j++
			}
			b.WriteString(fg(th.JSONNumber) + string(rs[i:j]) + reset)
			i = j
		case strings.HasPrefix(string(rs[i:min(i+5, len(rs))]), "true"), strings.HasPrefix(string(rs[i:min(i+5, len(rs))]), "false"):
			n := 4
			if r == 'f' {
				n = 5
			}
			b.WriteString(fg(th.JSONBool) + string(rs[i:i+n]) + reset)
			i += n
		case strings.HasPrefix(string(rs[i:min(i+4, len(rs))]), "null"):
			b.WriteString(fg(th.JSONNull) + "null" + reset)
			i += 4
		case r == '{' || r == '}' || r == '[' || r == ']' || r == ':' || r == ',':
			b.WriteString(tMuted() + tview.Escape(string(r)) + reset)
			i++
		default:
			b.WriteRune(r)
			i++
		}
	}
	return b.String()
}

func looksBinary(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	if !utf8.Valid(b) {
		return true
	}
	sample := b
	if len(sample) > 512 {
		sample = sample[:512]
	}
	for _, c := range sample {
		if c == 0 {
			return true
		}
	}
	return false
}

func hexPreview(b []byte) string {
	const maxDump = 4096
	dump := b
	note := ""
	if len(dump) > maxDump {
		dump = dump[:maxDump]
		note = "\n" + tMuted() + fmt.Sprintf("… %s more not shown", humanBytes(len(b)-maxDump)) + reset
	}
	return tview.Escape(hex.Dump(dump)) + note
}

// ---- tree building ----

func (v *Viewer) buildTree() {
	c := v.cur
	c.nodeMap = map[*jsontree.Node]*tview.TreeNode{}
	indexHint = map[*jsontree.Node]int{}
	var record func(n *jsontree.Node)
	record = func(n *jsontree.Node) {
		for i, ch := range n.Children {
			if n.Kind == jsontree.Array {
				indexHint[ch] = i
			}
			record(ch)
		}
	}
	record(c.root)
	v.selNode = nil
	root := v.makeTreeNode(c.root, 0)
	v.tree.SetRoot(root).SetCurrentNode(root)
	v.syncSelection(v.selFocused)
}

func (v *Viewer) makeTreeNode(jn *jsontree.Node, depth int) *tview.TreeNode {
	tn := tview.NewTreeNode(nodeLabel(jn, false)).SetReference(jn)
	tn.SetSelectedTextStyle(selectedStyle(true))
	v.cur.nodeMap[jn] = tn
	if jn.Kind == jsontree.Object || jn.Kind == jsontree.Array {
		tn.SetExpanded(depth < 2)
		for _, child := range jn.Children {
			tn.AddChild(v.makeTreeNode(child, depth+1))
		}
	}
	return tn
}

// syncSelection keeps the current tree node readable: it is rendered without
// colour tags (so the selection style applies to the whole line) while every
// other node keeps its syntax colours. focused picks the selection style.
func (v *Viewer) syncSelection(focused bool) {
	v.selFocused = focused
	cur := v.tree.GetCurrentNode()
	if v.selNode != nil && v.selNode != cur {
		if jn, ok := v.selNode.GetReference().(*jsontree.Node); ok {
			v.selNode.SetText(nodeLabel(jn, false))
		}
	}
	if cur != nil {
		if jn, ok := cur.GetReference().(*jsontree.Node); ok {
			cur.SetText(nodeLabel(jn, true))
			cur.SetSelectedTextStyle(selectedStyle(focused))
		}
	}
	v.selNode = cur
}

func nodeLabel(jn *jsontree.Node, plain bool) string {
	c := func(col tcell.Color) string {
		if plain {
			return ""
		}
		return fg(col)
	}
	r := reset
	if plain {
		r = ""
	}
	key := ""
	if jn.Parent != nil && jn.Parent.Kind == jsontree.Object {
		key = c(th.JSONKey) + tview.Escape(jn.Key) + r + c(th.Muted) + ": " + r
	} else if jn.Parent != nil {
		key = c(th.Muted) + fmt.Sprintf("%d: ", arrayIndex(jn)) + r
	}
	switch jn.Kind {
	case jsontree.Object:
		return key + c(th.Muted) + fmt.Sprintf("{…} %s", pluralize(len(jn.Children), "field", "fields")) + r
	case jsontree.Array:
		return key + c(th.Muted) + tview.Escape("[…]") + " " + pluralize(len(jn.Children), "item", "items") + r
	case jsontree.String:
		return key + c(th.JSONString) + tview.Escape(fmt.Sprintf("%q", truncate(jn.Str, 120))) + r
	case jsontree.Number:
		return key + c(th.JSONNumber) + jn.Str + r
	case jsontree.Bool:
		return key + c(th.JSONBool) + fmt.Sprint(jn.BoolVal) + r
	default:
		return key + c(th.JSONNull) + "null" + r
	}
}

// arrayIndex is the position of jn in its parent array. Labels are built
// for a handful of nodes at a time (initial build excepted, see
// makeTreeNode), so the linear scan is fine; indexHint short-circuits it.
func arrayIndex(jn *jsontree.Node) int {
	if p := jn.Parent; p != nil {
		if i := indexHint[jn]; i < len(p.Children) && p.Children[i] == jn {
			return i
		}
		for i, ch := range p.Children {
			if ch == jn {
				return i
			}
		}
	}
	return 0
}

// indexHint caches array positions recorded while building the tree.
var indexHint = map[*jsontree.Node]int{}

// refreshNode re-labels a node and its ancestors (child counts change).
func (v *Viewer) refreshNode(jn *jsontree.Node) {
	for cur := jn; cur != nil; cur = cur.Parent {
		if tn, ok := v.cur.nodeMap[cur]; ok {
			tn.SetText(nodeLabel(cur, tn == v.selNode))
		}
	}
}

// ---- tree keys (search) ----

func (v *Viewer) handleTreeKey(ev *tcell.EventKey) *tcell.EventKey {
	if v.searchMode {
		switch ev.Key() {
		case tcell.KeyEscape:
			v.searchMode, v.search = false, ""
			v.app.status.Info("Search closed")
			return nil
		case tcell.KeyEnter:
			v.jumpToMatch(true) // next match
			return nil
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if r := []rune(v.search); len(r) > 0 {
				v.search = string(r[:len(r)-1])
			}
			v.jumpToMatch(false)
			return nil
		case tcell.KeyRune:
			v.search += string(ev.Rune())
			v.jumpToMatch(false)
			return nil
		case tcell.KeyUp, tcell.KeyDown, tcell.KeyTab:
			v.searchMode = false
			return ev
		}
		return nil
	}
	switch {
	case ev.Rune() == '/' && v.cur != nil && v.cur.mode == modeTree:
		v.searchMode, v.search = true, ""
		v.app.status.Info("Search: type to jump · Enter next match · Esc close")
		return nil
	case ev.Rune() == 'n' && v.search != "":
		v.jumpToMatch(true)
		return nil
	case ev.Key() == tcell.KeyLeft:
		// collapse the current container, or jump to the parent
		if tn := v.tree.GetCurrentNode(); tn != nil {
			if len(tn.GetChildren()) > 0 && tn.IsExpanded() {
				tn.SetExpanded(false)
			} else if jn, ok := tn.GetReference().(*jsontree.Node); ok && jn.Parent != nil {
				if p, ok := v.cur.nodeMap[jn.Parent]; ok {
					v.tree.SetCurrentNode(p)
					v.syncSelection(v.selFocused)
				}
			}
		}
		return nil
	case ev.Key() == tcell.KeyRight:
		if tn := v.tree.GetCurrentNode(); tn != nil && len(tn.GetChildren()) > 0 {
			tn.SetExpanded(true)
		}
		return nil
	case ev.Rune() == '*':
		if tn := v.tree.GetCurrentNode(); tn != nil {
			tn.ExpandAll()
		}
		return nil
	}
	return ev
}

// jumpToMatch selects the first node (in document order) whose key or scalar
// value contains the search text; next=true continues after the current node.
func (v *Viewer) jumpToMatch(next bool) {
	if v.search == "" || v.cur == nil || v.cur.root == nil {
		v.app.status.Info("Search: %s", v.search)
		return
	}
	needle := strings.ToLower(v.search)
	var order []*jsontree.Node
	var walk func(n *jsontree.Node)
	walk = func(n *jsontree.Node) {
		order = append(order, n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(v.cur.root)
	start := 0
	if next {
		if tn := v.tree.GetCurrentNode(); tn != nil {
			for i, n := range order {
				if v.cur.nodeMap[n] == tn {
					start = i + 1
				}
			}
		}
	}
	var found *jsontree.Node
	for i := 0; i < len(order) && found == nil; i++ {
		n := order[(start+i)%len(order)]
		if strings.Contains(strings.ToLower(n.Key), needle) ||
			strings.Contains(strings.ToLower(n.ScalarDisplay()), needle) {
			found = n
		}
	}
	if found == nil {
		v.app.status.Warn("Search: %s (no match)", v.search)
		return
	}
	v.app.status.Info("Search: %s · Enter next · Esc close", v.search)
	for cur := found.Parent; cur != nil; cur = cur.Parent {
		if tn, ok := v.cur.nodeMap[cur]; ok {
			tn.SetExpanded(true)
		}
	}
	if tn, ok := v.cur.nodeMap[found]; ok {
		v.tree.SetCurrentNode(tn)
		v.syncSelection(v.selFocused)
	}
}

// toggleRaw is F3: flip between tree and raw text for editable objects.
func (v *Viewer) toggleRaw() {
	if v.cur == nil {
		return
	}
	if v.cur.mode != modeTree {
		v.app.status.Info("This value has no tree view (it is not editable JSON)")
		return
	}
	v.cur.rawShown = !v.cur.rawShown
	v.render()
	v.app.focusViewer()
}

// selectedNode returns the jsontree node under the cursor.
func (v *Viewer) selectedNode() *jsontree.Node {
	if v.cur == nil || v.cur.mode != modeTree || v.cur.rawShown {
		return nil
	}
	tn := v.tree.GetCurrentNode()
	if tn == nil {
		return nil
	}
	jn, _ := tn.GetReference().(*jsontree.Node)
	return jn
}

// ---- formatting helpers ----

func humanBytes(n int) string {
	switch {
	case n < 1024:
		return pluralize(n, "byte", "bytes")
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// relTime turns an HTTP Last-Modified date into "3 h ago (2026-01-02 15:04)".
func relTime(httpDate string) string {
	t, err := time.Parse(time.RFC1123, httpDate)
	if err != nil {
		return httpDate
	}
	return ago(t) + " (" + t.Local().Format("2006-01-02 15:04") + ")"
}

// ago renders a coarse relative time.
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	default:
		return t.Local().Format("2006-01-02")
	}
}
