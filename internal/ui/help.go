package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// helpSection is one page of the in-app help. Body lines use a tiny markup:
// lines starting with "# " are subheadings; "key | text" lines render as a
// key table; everything else is prose.
type helpSection struct {
	title string
	body  string
}

var helpSections = []helpSection{
	{"Start", `
# What this is
Riak Commander browses and edits data in a Riak KV cluster through its HTTP
API. The left pane drills down bucket types → buckets → keys; the right pane
shows the selected value, with JSON as an editable tree.

# Moving around
↑ ↓  PgUp PgDn  Home End | move in a list (j / k also work)
Enter  → | open the selected item
←  Backspace | go back up a level
Tab | switch between the list and the value pane
/ | filter the list (or search inside a JSON value)
Esc | cancel a load, clear a filter, leave 2i or find results, close dialogs
s | servers: connect, switch, recent history
F1  ? | this help (Tab / → next page, ← previous, 1–9 jump)
q  F10  Ctrl-C | quit (asks first if there are unsaved edits)

# Function keys
Every F-key has an alternative for terminals that intercept F-keys
(common on macOS): see the Keys page.

# Safety in one sentence
Nothing is written to Riak until you press F2 (save) or confirm a create or
delete, and saves keep the vclock and all secondary indexes.`},

	{"Servers", `
# Connecting
Pass an address on the command line, or press s for the server dialog.
Accepted forms: host, host:port, http://host:port, https://host:port/prefix.
The default port is 8098 (Riak's HTTP listener).

# Recent servers
Every server you connect to successfully is remembered, most recent first.
Starting riak-commander with no address reconnects to the last one.

Enter | connect to the selected server (or the typed address)
Ctrl-L | connect to the most recently used server
e | give the selected server a label, e.g. "staging"
d  Del | forget the selected server
↓ ↑  Tab | move between the address field and the list

The history lives in ~/.config/riak-commander/history.json (or
$XDG_CONFIG_HOME/riak-commander/). Run with --no-history to neither read nor
write it. Addresses containing credentials are rejected so passwords never
land in that file.

# Bucket types are remembered too
Riak's HTTP API cannot list bucket types. Types you open with "other bucket
type…" are remembered per server and listed next time; press Del on one to
forget it. Use --types a,b,c to always list some.`},

	{"Browsing", `
# Bucket types
"default" holds untyped buckets. Other types must be entered by name once
(see "riak-admin bucket-type list" on a Riak node) and are then remembered.

# Buckets
Riak only lists buckets that contain at least one key. Listing buckets and
keys is a full scan of the cluster: cheap on a laptop, expensive on a large
production cluster.

# Keys
Key listings stream and stop after --max-keys keys (default 1000). Select
"load next … keys" at the bottom to fetch more.

# While something loads
The pane switches to what you opened right away and shows a spinner, the
elapsed time and, for key listings, how many keys have arrived so far. The
list is locked until the data is there. Esc (or ←) cancels the load and
brings back what you were looking at.

/ | filter: type to narrow the list, Enter keeps it, Esc clears it
p | bucket properties (n_val, allow_mult, backend, datatype, …)
F5  Ctrl-R | reload the list
F7  n | create a key in the open bucket
F8  Del | delete the selected key (asks for confirmation)`},

	{"Viewing", `
# Value pane
The header shows the content type, size, last-modified time, whether a
vclock is present, the object's secondary indexes (2i) and user metadata.
A badge says whether the value is EDITABLE, MODIFIED or READ-ONLY.

# How values are shown
JSON (up to 1 MiB) | collapsible tree, editable
other text | plain text, read-only
binary | hex dump of the first 4 KiB, read-only
CRDT buckets | datatype JSON (maps, sets, counters), read-only

# In the JSON tree
Enter | expand / collapse a container, edit a scalar
← → | collapse / expand (← on a closed node jumps to its parent)
* | expand everything below the selected node
/ | search keys and values; Enter or n jumps to the next match
F3  v | toggle between the tree and the raw JSON text
F5  Ctrl-R | reload the value from Riak`},

	{"Editing", `
# Editing a value
Edits happen in memory. The title shows ● and the header shows MODIFIED until
you save. Leaving the value (or quitting) with unsaved edits asks whether to
save, discard or stay.

F4  e  Enter | edit the selected field: pick a type and enter a value
F7  a | add a field to an object, or an element to an array
r | rename an object field (position is kept)
F8  Del | remove the selected field or element
F2  Ctrl-S | save the value to Riak

# Value types in the edit dialog
string | stored as typed, no quotes needed
number | a JSON number: 42, -1.5, 6.02e23 (large integers are kept exactly)
bool | true or false
null | ignores the value field
raw JSON | any JSON document, e.g. {"a":1} or [1,2,3]

# Creating and deleting keys
In a key list, F7 creates a key (with If-None-Match: *, so an existing key is
never overwritten) and F8 deletes the selected key after confirmation.`},

	{"2i", `
# Secondary indexes
Objects may carry secondary indexes (2i): name/value pairs stored next to
the value. Index names end in _bin (strings) or _int (integers). 2i needs a
backend that supports it (leveldb or memory, not bitcask).

i | query an index on the open (or selected) bucket
Esc | leave the results and return to the full key list

# The query dialog
Index | the index name; suggestions come from objects you opened in this
      | bucket and from earlier queries
Mode | exact (one Value) or range (From … To, inclusive; the To
     | field appears only in range mode)
Max results | stop after this many keys

The special indexes $key (range over key names) and $bucket (all keys of the
bucket) work as well. The last query per bucket is prefilled next time.`},

	{"Find", `
# Find objects by field
f | find in the open (or selected) bucket
Esc | stop a running scan (matches so far are kept); again to go back

# Scan (works on every cluster)
Field | a path such as plan.name, items[0].sku or items[*].sku; quote odd
      | names: ["odd.key"]. Leave empty to match any field name or value
Match | equals (numbers compare numerically: 12 = 12.0), contains (ignores
      | case), regex (Go syntax), exists (the field is present)
Value | what to match; hidden for exists
Scan up to | how many keys to check (defaults to --max-keys)

The scan lists the bucket's keys (or reuses the open key list), fetches
each object (8 at a time) and matches it. Matches appear while it runs,
next to the matched value. Non-JSON values, siblings and objects deleted
meanwhile are skipped and counted. A scan reads every object it checks:
fine for development data, slow and heavy on big production buckets.

# Riak Search
When the bucket has a Riak Search index (its search_index property),
"Using" offers Riak Search: type a Solr query, e.g. plan.name_s:business,
and the cluster answers from the index instantly. Field names follow the
index schema. Riak Search was deprecated and is absent from Riak KV 3.x
builds by default, so many clusters only offer the scan.`},

	{"Safety", `
# Why not just curl?
Vclocks | every save sends back the X-Riak-Vclock read with the value, so
        | concurrent writes are detected instead of silently lost
2i | Riak stores indexes on the object; a PUT without them deletes them.
   | Saves re-send every X-Riak-Index-* header read with the value
Metadata | X-Riak-Meta-* user metadata is preserved the same way
Siblings | a conflicted key shows its siblings; open one and press F2 to
         | make it the resolved value
Faithful JSON | key order and number literals are preserved exactly, so a
              | save never reshuffles a document or rounds 64-bit IDs
Create | new keys use If-None-Match: *, never clobbering existing keys

# Read-only on purpose
CRDT values (buckets whose type has a datatype), binary values and values
over 1 MiB are shown but cannot be edited here.`},

	{"Keys", `
# All keys
F1  ? | help
F2  Ctrl-S | save the open value
F3  v | tree ↔ raw view
F4  e | edit the selected field
F5  Ctrl-R | reload the list or value
F7 | new key (list) · add field (tree); also n / a
F8  Del | delete key (list) · delete field (tree) · forget type
F10  q  Ctrl-C | quit
Tab | switch panes
s  c | servers
f | find objects by field (scan or Riak Search)
i | 2i query
p | bucket properties
r | rename a field
/ | filter / search
Esc | cancel a load, clear filter, leave 2i or find results, close dialogs
j  k | down / up in lists

# Mouse
Click to focus a pane or select a row; the wheel scrolls.

# Troubleshooting
F-keys do nothing | use the letter / Ctrl alternatives above, or enable
                  | "Use Option as Meta" / F-key passthrough in the terminal
Colours look odd | try --theme light or --theme mono, or set NO_COLOR=1
Listing is slow | key listings scan the whole cluster; lower --max-keys`},
}

// helpSectionFor picks the page that matches what the user is doing.
func (a *App) helpSectionFor() int {
	switch {
	case a.client == nil:
		return 1
	case a.viewerFocused() && a.viewer.cur != nil && a.viewer.cur.editable:
		return 4
	case a.viewerFocused():
		return 3
	case a.browser.inResults && strings.HasPrefix(a.browser.queryDesc, "2i"):
		return 5
	case a.browser.inResults:
		return 6
	case a.browser.level > levelTypes:
		return 2
	}
	return 0
}

// helpModal shows the paged help, opened on the page for the current context.
func (a *App) helpModal() {
	cur := a.helpSectionFor()

	tabs := tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	tabs.SetBackgroundColor(th.Surface)
	body := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWordWrap(true)
	body.SetBackgroundColor(th.Surface)
	footer := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight)
	footer.SetBackgroundColor(th.Surface)
	footer.SetText(keyHelp("Tab →", "next page", "←", "previous", "1–9", "jump", "↑↓", "scroll", "Esc", "close"))

	show := func(i int) {
		cur = (i + len(helpSections)) % len(helpSections)
		var t strings.Builder
		for j, s := range helpSections {
			label := fmt.Sprintf(" %d %s ", j+1, s.title)
			if j == cur {
				t.WriteString(fgbg(th.OnAccent, th.Accent) + tBold() + label + reset)
			} else {
				t.WriteString(tMuted() + label + reset)
			}
		}
		tabs.SetText(t.String())
		body.SetText(renderHelp(helpSections[cur].body))
		body.ScrollToBeginning()
	}
	show(cur)

	title := "Riak Commander help"
	if a.opts.Version != "" {
		title = fmt.Sprintf("Riak Commander %s · help", a.opts.Version)
	}
	frame := newDialogFrame(tview.Escape(title)).
		AddItem(tabs, 1, 0, false).
		AddItem(spacer(), 1, 0, false).
		AddItem(body, 0, 1, true).
		AddItem(footer, 1, 0, false)

	body.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEscape, tcell.KeyF1:
			a.closeModal()
			return nil
		case tcell.KeyTab, tcell.KeyRight:
			show(cur + 1)
			return nil
		case tcell.KeyBacktab, tcell.KeyLeft:
			show(cur - 1)
			return nil
		}
		switch r := ev.Rune(); {
		case r == 'q' || r == '?':
			a.closeModal()
			return nil
		case r >= '1' && r <= '9' && int(r-'1') < len(helpSections):
			show(int(r - '1'))
			return nil
		case r == 'j':
			return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
		case r == 'k':
			return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
		}
		return ev
	})
	a.openModal(frame, 96, 34)
}

// renderHelp turns the help markup into coloured text.
func renderHelp(src string) string {
	lines := strings.Split(strings.Trim(src, "\n"), "\n")
	// align the key column within each contiguous table block
	widths := map[int]int{}
	start, w := -1, 0
	flush := func(end int) {
		for j := start; j < end; j++ {
			widths[j] = w
		}
		start, w = -1, 0
	}
	for i, l := range lines {
		if k, _, ok := strings.Cut(l, " | "); ok {
			if start < 0 {
				start = i
			}
			w = max(w, len([]rune(strings.TrimSpace(k))))
		} else if start >= 0 {
			flush(i)
		}
	}
	if start >= 0 {
		flush(len(lines))
	}

	var b strings.Builder
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "# "):
			b.WriteString(" " + tAccent() + tBold() + tview.Escape(l[2:]) + reset + "\n")
		case strings.Contains(l, " | "):
			k, v, _ := strings.Cut(l, " | ")
			k = strings.TrimSpace(k)
			pad := strings.Repeat(" ", widths[i]-len([]rune(k)))
			b.WriteString("   " + tText() + tBold() + tview.Escape(k) + reset + pad + "  " + tMuted() + tview.Escape(v) + reset + "\n")
		default:
			b.WriteString(" " + tText() + tview.Escape(l) + reset + "\n")
		}
	}
	return b.String()
}
