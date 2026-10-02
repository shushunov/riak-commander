package ui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rivo/tview"

	"github.com/shushunov/riak-commander/internal/jsontree"
	"github.com/shushunov/riak-commander/internal/riak"
)

// findWorkers is how many objects a scan fetches concurrently: enough to
// hide network latency without hammering the cluster.
const findWorkers = 8

// findBatchInterval is how often scan matches are pushed to the list.
const findBatchInterval = 200 * time.Millisecond

// dontIndex is the search_index value Riak uses for "no search index".
const dontIndex = "_dont_index_"

var matchModeNames = []string{"equals", "contains", "regex", "exists"}

// findQuery is the find dialog's input; the last one per bucket is
// remembered to prefill the next dialog.
type findQuery struct {
	search bool // Riak Search instead of a scan

	path, mode, value string // scan (always the whole bucket)

	solr string // search: Solr query
	rows int    // search: max results
}

func (q findQuery) describe() string {
	if q.search {
		return "search " + q.solr
	}
	field := q.path
	if field == "" {
		field = "any field"
	}
	if q.mode == "exists" {
		return fmt.Sprintf("%s exists", field)
	}
	op := map[string]string{"equals": "=", "contains": "contains", "regex": "~"}[q.mode]
	return fmt.Sprintf("%s %s %s", field, op, q.value)
}

func matchModeFromName(name string) jsontree.MatchMode {
	switch name {
	case "contains":
		return jsontree.MatchContains
	case "regex":
		return jsontree.MatchRegex
	case "exists":
		return jsontree.MatchExists
	}
	return jsontree.MatchEquals
}

// findDialog opens "Find in bucket" for the open bucket (key list) or the
// selected one (bucket list). Bucket props are needed first: they tell
// whether a Riak Search index exists and whether the bucket holds CRDTs.
func (a *App) findDialog() {
	b := a.browser
	bucket := b.bucket
	if b.level == levelBuckets {
		bucket = b.currentItem()
	}
	if (b.level != levelKeys && b.level != levelBuckets) || bucket == "" {
		a.status.Info("Open or select a bucket first: find searches one bucket")
		return
	}
	btype := b.btype
	pkey := btype + "/" + bucket
	if props, ok := b.propsCache[pkey]; ok {
		a.openFindDialog(btype, bucket, props)
		return
	}
	client := a.client
	a.async("reading bucket properties", 0, func(ctx context.Context) (any, error) {
		return client.BucketProps(ctx, btype, bucket)
	}, func(res any, err error) {
		var props map[string]any
		if err == nil {
			props = res.(map[string]any)
			b.propsCache[pkey] = props
		}
		a.openFindDialog(btype, bucket, props) // without props: scan only
	})
}

func searchIndexOf(props map[string]any) string {
	idx, _ := props["search_index"].(string)
	if idx == dontIndex {
		return ""
	}
	return idx
}

func (a *App) openFindDialog(btype, bucket string, props map[string]any) {
	k := bucketKey(btype, bucket)
	searchIndex := searchIndexOf(props)
	last, ok := a.lastFind[k]
	if !ok {
		last = findQuery{mode: "equals", rows: 100}
	}
	if searchIndex == "" {
		last.search = false
	}

	// Fields are created once and re-added as the mode changes, so typed
	// text survives switching.
	var using *tview.DropDown
	if searchIndex != "" {
		using = tview.NewDropDown().SetLabel("Using").
			SetOptions([]string{"scan objects", "Riak Search (" + searchIndex + ")"}, nil)
		if last.search {
			using.SetCurrentOption(1)
		} else {
			using.SetCurrentOption(0)
		}
		styleDropDown(using)
	}
	pathField := tview.NewInputField().SetLabel("Field").SetText(last.path).SetFieldWidth(52).
		SetPlaceholder("e.g. plan.name or items[*].sku (empty: any field)")
	match := tview.NewDropDown().SetLabel("Match").SetOptions(matchModeNames, nil)
	for i, n := range matchModeNames {
		if n == last.mode {
			match.SetCurrentOption(i)
		}
	}
	styleDropDown(match)
	valueField := tview.NewInputField().SetLabel("Value").SetText(last.value).SetFieldWidth(52)
	solrField := tview.NewInputField().SetLabel("Query").SetText(last.solr).SetFieldWidth(52).
		SetPlaceholder("Solr syntax, e.g. plan.name_s:business")
	rowsField := tview.NewInputField().SetLabel("Max results").SetText(strconv.Itoa(last.rows)).SetFieldWidth(10)
	for _, in := range []*tview.InputField{pathField, valueField, solrField, rowsField} {
		styleInput(in)
	}

	searchMode := func() bool {
		if using == nil {
			return false
		}
		i, _ := using.GetCurrentOption()
		return i == 1
	}
	matchName := func() string { _, n := match.GetCurrentOption(); return n }

	hint := "Scan: checks every object in the bucket (" + strconv.Itoa(findWorkers) + " at a time); on big buckets " +
		"this takes a while and loads the cluster. Esc stops and keeps the matches found so far. Field paths: a.b, " +
		"items[0].sku, items[*].sku, [\"odd.key\"]; an empty Field matches any field name or value. Equals compares " +
		"numbers numerically; contains ignores case."
	if searchIndex != "" {
		hint += " Riak Search answers from the index instantly; field names follow the index schema (often with a type suffix such as _s)."
	}

	a.form(formSpec{
		title:   "Find in " + k,
		okLabel: "Find",
		hint:    hint,
		width:   78,
		build: func(f *tview.Form) {
			if using != nil {
				f.AddFormItem(using)
			}
		},
		setup: func(f *tview.Form, refit func()) {
			fixed := f.GetFormItemCount()
			layout := func() {
				for f.GetFormItemCount() > fixed {
					f.RemoveFormItem(f.GetFormItemCount() - 1)
				}
				if searchMode() {
					f.AddFormItem(solrField).AddFormItem(rowsField)
				} else {
					f.AddFormItem(pathField).AddFormItem(match)
					if matchName() != "exists" {
						f.AddFormItem(valueField)
					}
				}
				refit()
			}
			// callbacks attached after construction: SetCurrentOption above
			// would otherwise fire them before the form exists
			if using != nil {
				using.SetSelectedFunc(func(string, int) { layout() })
			}
			match.SetSelectedFunc(func(string, int) { layout() })
			layout()
			// focus the main input of the current mode; "Using" is one
			// Shift-Tab away
			if !searchMode() && last.path != "" && last.mode != "exists" {
				f.SetFocus(fixed + 2) // straight to Value
			} else {
				f.SetFocus(fixed) // Field, or Query in search mode
			}
		},
		onOK: func(f *tview.Form) error {
			q := findQuery{search: searchMode()}
			if q.search {
				q.solr = strings.TrimSpace(solrField.GetText())
				if q.solr == "" {
					return fmt.Errorf("enter a Solr query, e.g. field_s:value or *:*")
				}
				rows, err := strconv.Atoi(strings.TrimSpace(rowsField.GetText()))
				if err != nil || rows < 1 {
					return fmt.Errorf("max results must be a positive number")
				}
				q.rows = rows
				q.path, q.mode, q.value = last.path, last.mode, last.value
			} else {
				q.path, q.mode, q.value = strings.TrimSpace(pathField.GetText()), matchName(), valueField.GetText()
				path, err := jsontree.ParsePath(q.path)
				if err != nil {
					return err
				}
				if q.mode != "exists" && q.value == "" {
					return fmt.Errorf("enter a value to match (or choose Match: exists)")
				}
				if q.mode == "exists" && path.IsEmpty() && q.value == "" {
					return fmt.Errorf("enter a Field whose existence to check")
				}
				if _, err := jsontree.NewMatcher(matchModeFromName(q.mode), q.value); err != nil {
					return err
				}
				q.solr, q.rows = last.solr, last.rows
			}
			a.lastFind[k] = q
			if q.search {
				a.runSearch(btype, bucket, searchIndex, q)
			} else {
				a.runScan(btype, bucket, props, q)
			}
			return nil
		},
	})
}

// ---- Riak Search ----

func (a *App) runSearch(btype, bucket, index string, q findQuery) {
	b := a.browser
	back := b.snapshot()
	p := b.startListLoad("Querying "+index, false, func() {
		b.enterResults(btype, bucket, q.describe(), "search results", back)
	})
	if p == nil {
		return
	}
	client := a.client
	a.asyncPending(p, "querying search index "+index, 0, func(ctx context.Context) (any, error) {
		return client.Search(ctx, index, q.solr, btype, bucket, q.rows)
	}, func(r any, err error) {
		if err != nil {
			return
		}
		res := r.(*riak.SearchResult)
		b.items = res.Keys
		b.renderFresh()
		more := ""
		if res.NumFound > len(res.Keys) {
			more = fmt.Sprintf(" (%s hits in the index; raise Max results or narrow the query)", formatCount(res.NumFound))
		}
		a.status.Success("Riak Search: %s%s", pluralize(len(res.Keys), "match", "matches"), more)
	})
}

// ---- scan ----

// A scan always covers the whole bucket: a silent cap would make "no
// matches" look definitive when part of the bucket was never checked. Keys
// stream straight into the workers, so memory stays flat and matching
// starts at once; Esc stops the scan, and the result says whether every key
// was checked.

// scanStats are shared between the scan goroutines and the draw hook.
type scanStats struct {
	fed         atomic.Int64 // distinct keys handed to the workers
	listingDone atomic.Bool  // the key source is exhausted: fed is the total
	checked     atomic.Int64
	matched     atomic.Int64
	skipped     atomic.Int64
}

type scanHit struct{ key, note string }

type scanOutcome struct {
	stopped bool  // Esc
	listErr error // the key listing failed part-way
	lastErr error // last per-object error (objects are skipped, not fatal)
}

// complete reports whether every key in the bucket was checked.
func (o scanOutcome) complete() bool { return !o.stopped && o.listErr == nil }

// keySource produces the keys to scan, calling emit for each; it stops
// early when emit returns false.
type keySource func(ctx context.Context, emit func(key string) bool) error

// runScan fetches every object of the bucket and matches it. Matches stream
// into the results list as they are found; Esc stops the scan and keeps
// them.
func (a *App) runScan(btype, bucket string, props map[string]any, q findQuery) {
	b := a.browser
	path, _ := jsontree.ParsePath(q.path)                           // validated in the dialog
	m, _ := jsontree.NewMatcher(matchModeFromName(q.mode), q.value) // validated in the dialog
	crdt := isCRDT(props)
	client := a.client

	// A complete key list that is already open is reused; anything else
	// (a truncated page, or starting from the bucket list) streams the
	// bucket's keys from Riak.
	var source keySource
	if b.level == levelKeys && !b.inResults && b.btype == btype && b.bucket == bucket && !b.truncated {
		known := append([]string(nil), b.items...)
		source = func(ctx context.Context, emit func(string) bool) error {
			for _, k := range known {
				if !emit(k) {
					break
				}
			}
			return nil
		}
	} else {
		source = func(ctx context.Context, emit func(string) bool) error {
			err := client.StreamKeys(ctx, btype, bucket, func(chunk []string) error {
				for _, k := range chunk {
					if !emit(k) {
						return context.Canceled
					}
				}
				return nil
			})
			if ctx.Err() != nil {
				return nil // stopped, not a listing failure
			}
			return err
		}
	}

	back := b.snapshot()
	p := b.startListLoad("Searching "+bucket, false, func() {
		b.enterResults(btype, bucket, "find "+q.describe(), "find results", back)
	})
	if p == nil {
		return
	}
	gen := b.resultsGen
	st := &scanStats{}
	p.keepOnCancel = true
	p.progressText = func() string {
		of := ""
		if st.listingDone.Load() {
			of = " of " + formatCount(int(st.fed.Load()))
		}
		return fmt.Sprintf("checked %s%s · %s", formatCount(int(st.checked.Load())), of,
			pluralize(int(st.matched.Load()), "match", "matches"))
	}

	// deliver applies a batch of hits on the UI goroutine, unless the user
	// has moved on to another view meanwhile
	deliver := func(batch []scanHit) {
		a.tv.QueueUpdateDraw(func() {
			if b.resultsGen != gen || !b.inResults {
				return
			}
			for _, h := range batch {
				if _, dup := b.notes[h.key]; !dup {
					b.items = append(b.items, h.key)
					b.notes[h.key] = h.note
				}
			}
			b.render()
		})
	}

	// No overall deadline: the scan runs until the bucket is done or the
	// user presses Esc; each object fetch has its own timeout.
	a.asyncPending(p, "searching "+bucket, 365*24*time.Hour, func(ctx context.Context) (any, error) {
		return scanKeys(ctx, client, btype, bucket, source, crdt, path, m, st, deliver), nil
	}, func(r any, err error) {
		if err != nil {
			return
		}
		out := r.(scanOutcome)
		if b.resultsGen == gen && b.inResults {
			if !out.complete() {
				b.resultsTitle = "find results (incomplete)"
			}
			b.render()
			if b.list.GetCurrentItem() == 0 && len(b.items) > 0 {
				b.list.SetCurrentItem(1)
				b.restyleAll()
			}
		}
		checked, matched := int(st.checked.Load()), int(st.matched.Load())
		found := pluralize(matched, "match", "matches")
		if matched == 0 {
			found = "no matches"
		}
		skipped := ""
		if n := st.skipped.Load(); n > 0 {
			skipped = fmt.Sprintf(" · %s skipped (not JSON, siblings or unreadable)", formatCount(int(n)))
			if out.lastErr != nil {
				skipped += ", last error: " + out.lastErr.Error()
			}
		}
		switch {
		case out.stopped:
			a.status.Warn("Stopped after checking %s · %s. Not all keys were checked%s",
				pluralize(checked, "key", "keys"), found, skipped)
		case out.listErr != nil:
			a.status.Err(fmt.Errorf("listing keys failed after %s (%v) · %s. Not all keys were checked",
				pluralize(checked, "key", "keys"), out.listErr, found))
		default:
			a.status.Success("Searched all %s · %s%s", pluralize(checked, "key", "keys"), found, skipped)
		}
	})
}

// scanKeys fetches and matches the keys from source with a bounded worker
// pool, streaming hits through deliver in batches. It returns when the
// source is exhausted and all keys are checked, or when ctx is cancelled;
// every delivered batch has been applied by then.
func scanKeys(ctx context.Context, client *riak.Client, btype, bucket string, source keySource, crdt bool,
	path jsontree.FieldPath, m *jsontree.Matcher, st *scanStats, deliver func([]scanHit)) scanOutcome {
	jobs := make(chan string)
	hits := make(chan scanHit, 64)
	var errMu sync.Mutex
	var lastErr error

	var workers sync.WaitGroup
	for i := 0; i < findWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for key := range jobs {
				hit, ok, err := scanOne(ctx, client, btype, bucket, key, crdt, path, m)
				if ctx.Err() != nil {
					return // stopped: this object was not really checked
				}
				st.checked.Add(1)
				switch {
				case err != nil:
					st.skipped.Add(1)
					if !errors.Is(err, errSkip) {
						errMu.Lock()
						lastErr = err
						errMu.Unlock()
					}
				case ok:
					st.matched.Add(1)
					hits <- hit
				}
			}
		}()
	}

	// collector: batch hits so the list is not re-rendered per match
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		var batch []scanHit
		tick := time.NewTicker(findBatchInterval)
		defer tick.Stop()
		for {
			select {
			case h, ok := <-hits:
				if !ok {
					if len(batch) > 0 {
						deliver(batch)
					}
					return
				}
				batch = append(batch, h)
			case <-tick.C:
				if len(batch) > 0 {
					deliver(batch)
					batch = nil
				}
			}
		}
	}()

	// feed: Riak may repeat keys across streamed chunks, so remember which
	// were sent (key strings only, a small fraction of the objects' size).
	// Sending blocks while all workers are busy, which throttles reading.
	seen := map[string]bool{}
	listErr := source(ctx, func(k string) bool {
		if seen[k] {
			return true
		}
		seen[k] = true
		select {
		case jobs <- k:
			st.fed.Add(1)
			return true
		case <-ctx.Done():
			return false
		}
	})
	if ctx.Err() == nil {
		st.listingDone.Store(true)
	}
	close(jobs)
	workers.Wait()
	close(hits)
	<-collected

	errMu.Lock()
	defer errMu.Unlock()
	return scanOutcome{stopped: ctx.Err() != nil, listErr: listErr, lastErr: lastErr}
}

// errSkip marks objects that are skipped by design (not JSON, siblings,
// deleted meanwhile) rather than because of a failure worth reporting.
var errSkip = errors.New("skipped")

// scanOne fetches one object and matches it.
func scanOne(ctx context.Context, client *riak.Client, btype, bucket, key string, crdt bool,
	path jsontree.FieldPath, m *jsontree.Matcher) (scanHit, bool, error) {
	rctx, cancel := context.WithTimeout(ctx, client.Timeout())
	defer cancel()
	var body []byte
	if crdt {
		b, err := client.GetDatatype(rctx, btype, bucket, key)
		if err != nil {
			return scanHit{}, false, skipReason(err)
		}
		body = b
	} else {
		obj, err := client.GetObject(rctx, btype, bucket, key)
		if err != nil {
			return scanHit{}, false, skipReason(err)
		}
		body = obj.Body
	}
	if len(body) > treeEditLimit {
		return scanHit{}, false, errSkip
	}
	root, err := jsontree.Parse(body)
	if err != nil {
		return scanHit{}, false, errSkip
	}
	if crdt {
		// datatype JSON is {"type": …, "value": …}; match inside value
		for _, c := range root.Children {
			if c.Key == "value" {
				root = c
				c.Parent = nil
			}
		}
	}
	found := jsontree.Find(root, path, m)
	if len(found) == 0 {
		return scanHit{}, false, nil
	}
	return scanHit{key: key, note: hitNote(found[0], path.IsEmpty())}, true, nil
}

func skipReason(err error) error {
	var sib *riak.ErrSiblings
	if errors.As(err, &sib) || errors.Is(err, riak.ErrNotFound) {
		return errSkip
	}
	return err
}

// hitNote is the short text shown next to a matching key.
func hitNote(h jsontree.Match, anywhere bool) string {
	n := h.Node
	if h.Name {
		return "field " + strconv.Quote(n.Key) + " at " + n.Path()
	}
	val := n.ScalarDisplay()
	switch n.Kind {
	case jsontree.Object:
		val = "{…}"
	case jsontree.Array:
		val = "[…]"
	case jsontree.String:
		val = strconv.Quote(truncate(n.Str, 60))
	}
	if anywhere {
		return n.Path() + " = " + val
	}
	return val
}
