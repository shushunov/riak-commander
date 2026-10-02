package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rivo/tview"

	"github.com/shushunov/riak-commander/internal/riak"
)

// indexQuery is a 2i query as entered in the dialog; the last one per bucket
// is remembered to prefill the next dialog.
type indexQuery struct {
	index, mode, from, to string
	max                   int
}

// noteIndexes records the 2i names carried by a loaded object, so the query
// dialog can suggest them for that bucket.
func (a *App) noteIndexes(obj *riak.RiakObject) {
	if len(obj.Indexes) == 0 {
		return
	}
	k := bucketKey(obj.BucketType, obj.Bucket)
	if a.seenIndexes[k] == nil {
		a.seenIndexes[k] = map[string]bool{}
	}
	for name := range obj.Indexes {
		a.seenIndexes[k][name] = true
	}
}

// indexSuggestions lists known index names for a bucket: those used in past
// queries (most recent first, from the history), then those seen on objects.
func (a *App) indexSuggestions(btype, bucket string) []string {
	k := bucketKey(btype, bucket)
	seen := map[string]bool{}
	var out []string
	if a.client != nil {
		if e, ok := a.hist.Get(a.client.Address()); ok {
			for _, name := range e.Indexes[k] {
				if !seen[name] {
					seen[name] = true
					out = append(out, name)
				}
			}
		}
	}
	var rest []string
	for name := range a.seenIndexes[k] {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// indexQueryDialog runs a 2i exact or range query against the current bucket;
// results replace the key list (Esc restores the normal listing).
func (a *App) indexQueryDialog() {
	b := a.browser
	if b.level != levelKeys && b.level != levelBuckets {
		a.status.Info("Open or select a bucket first: 2i queries run against one bucket")
		return
	}
	bucket := b.bucket
	if b.level == levelBuckets {
		bucket = b.currentItem()
	}
	if bucket == "" {
		a.status.Info("Select a bucket first")
		return
	}
	btype := b.btype
	k := bucketKey(btype, bucket)
	suggestions := a.indexSuggestions(btype, bucket)
	last, hasLast := a.lastQuery[k]
	if !hasLast {
		last = indexQuery{mode: "exact", max: 1000}
		if len(suggestions) > 0 {
			last.index = suggestions[0]
		}
	}

	hint := "Index names end in _bin (string) or _int (integer). Exact finds one value; range " +
		"finds From..To inclusive. Special index $key ranges over key names."
	if len(suggestions) > 0 {
		hint += " Known here: " + strings.Join(suggestions, ", ") + "."
	}

	a.form(formSpec{
		title:   "2i query on " + k,
		okLabel: "Query",
		hint:    hint,
		build: func(f *tview.Form) {
			f.AddInputField("Index", last.index, 40, nil, nil)
			placeholder(f, "Index", "e.g. email_bin")
			in := f.GetFormItemByLabel("Index").(*tview.InputField)
			if len(suggestions) > 0 {
				hasAutocomplete[in] = true
				in.SetAutocompleteFunc(func(text string) []string {
					var out []string
					for _, s := range suggestions {
						if strings.Contains(s, strings.ToLower(text)) {
							out = append(out, s)
						}
					}
					if len(out) == 1 && out[0] == text {
						return nil
					}
					return out
				})
			}
			modeIdx := 0
			if last.mode == "range" {
				modeIdx = 1
			}
			f.AddDropDown("Mode", []string{"exact", "range"}, modeIdx, nil)
			f.AddInputField("Value / From", last.from, 40, nil, nil)
			f.AddInputField("To (range)", last.to, 40, nil, nil)
			placeholder(f, "To (range)", "only used in range mode")
			f.AddInputField("Max results", strconv.Itoa(last.max), 10, nil, nil)
			if last.index != "" {
				f.SetFocus(2) // jump straight to the value
			}
		},
		onOK: func(f *tview.Form) error {
			q := indexQuery{
				index: strings.TrimSpace(inputText(f, "Index")),
				mode:  dropdownValue(f, "Mode"),
				from:  inputText(f, "Value / From"),
				to:    inputText(f, "To (range)"),
			}
			if q.index == "" {
				return fmt.Errorf("enter an index name")
			}
			if !strings.HasSuffix(q.index, "_bin") && !strings.HasSuffix(q.index, "_int") && q.index != "$key" && q.index != "$bucket" {
				return fmt.Errorf("index names end in _bin or _int (or use $key / $bucket)")
			}
			if q.from == "" {
				return fmt.Errorf("enter a value")
			}
			if strings.HasSuffix(q.index, "_int") {
				for _, v := range []string{q.from, q.to} {
					if v == "" {
						continue
					}
					if _, err := strconv.ParseInt(v, 10, 64); err != nil {
						return fmt.Errorf("%q is not an integer (required by _int indexes)", v)
					}
				}
			}
			to := q.to
			if q.mode == "exact" {
				to = ""
			} else if to == "" {
				return fmt.Errorf("range mode needs a To value")
			}
			maxResults, err := strconv.Atoi(strings.TrimSpace(inputText(f, "Max results")))
			if err != nil || maxResults < 1 {
				return fmt.Errorf("max results must be a positive number")
			}
			q.max = maxResults
			a.lastQuery[k] = q

			desc := q.index + "=" + q.from
			if to != "" {
				desc = fmt.Sprintf("%s ∈ [%s … %s]", q.index, q.from, to)
			}
			b.runQuery(btype, bucket, desc, func(ctx context.Context) (*riak.IndexResult, error) {
				return a.client.IndexQuery(ctx, btype, bucket, q.index, q.from, to, maxResults)
			}, func() {
				if !strings.HasPrefix(q.index, "$") {
					a.hist.AddIndex(a.client.Address(), k, q.index)
					a.saveHistory()
				}
			})
			return nil
		},
	})
}
