package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// handleKey is the global dispatcher. Pane-local behaviour (arrows, Enter,
// filter/search typing) lives in the panes' own InputCapture handlers; only
// app-wide keys are handled here. Every function key has a letter or Ctrl
// alternative because many terminals (notably on macOS) intercept F-keys.
func (a *App) handleKey(ev *tcell.EventKey) *tcell.EventKey {
	if a.modalOpen {
		return ev // modals manage their own keys (incl. Esc)
	}
	if a.pending != nil {
		return a.handlePendingKey(ev)
	}
	typing := a.browser.filterMode || a.viewer.searchMode

	switch ev.Key() {
	case tcell.KeyF1:
		a.helpModal()
		return nil
	case tcell.KeyF2, tcell.KeyCtrlS:
		a.save(nil)
		return nil
	case tcell.KeyF3:
		a.viewer.toggleRaw()
		return nil
	case tcell.KeyF4:
		a.editSelected()
		return nil
	case tcell.KeyF5, tcell.KeyCtrlR:
		a.reload()
		return nil
	case tcell.KeyF7:
		a.newItem()
		return nil
	case tcell.KeyF8, tcell.KeyDelete:
		if typing && ev.Key() == tcell.KeyDelete {
			return ev
		}
		a.deleteItem()
		return nil
	case tcell.KeyF10, tcell.KeyCtrlC:
		a.quit()
		return nil
	case tcell.KeyTab, tcell.KeyBacktab:
		if !typing {
			a.toggleFocus()
			return nil
		}
	}

	if typing || ev.Key() != tcell.KeyRune {
		return ev
	}
	switch ev.Rune() {
	case '?':
		a.helpModal()
	case 'q':
		a.quit()
	case 's', 'c':
		a.serverPicker("")
	case 'i':
		if a.client != nil {
			a.indexQueryDialog()
		}
	case 'p':
		if a.client != nil {
			a.propsModal()
		}
	case 'v':
		a.viewer.toggleRaw()
	case 'e':
		if a.viewerFocused() {
			a.editSelected()
		} else {
			return ev
		}
	case 'n':
		if a.browserFocused() {
			a.newKeyDialog()
		} else {
			return ev
		}
	case 'a':
		if a.viewerFocused() {
			a.addChild()
		} else {
			return ev
		}
	case 'r':
		if a.viewerFocused() {
			a.renameNode()
		} else {
			return ev
		}
	default:
		return ev
	}
	return nil
}

// handlePendingKey is the key policy while a navigation load is in flight:
// the panes are about to be replaced, so only cancel, help and quit work.
func (a *App) handlePendingKey(ev *tcell.EventKey) *tcell.EventKey {
	switch {
	case ev.Key() == tcell.KeyEscape, ev.Key() == tcell.KeyLeft,
		ev.Key() == tcell.KeyBackspace, ev.Key() == tcell.KeyBackspace2:
		a.cancelPending()
	case ev.Key() == tcell.KeyF1, ev.Rune() == '?':
		a.helpModal()
	case ev.Key() == tcell.KeyF10, ev.Key() == tcell.KeyCtrlC, ev.Rune() == 'q':
		a.quit()
	}
	return nil // everything else would act on content about to be replaced
}

func (a *App) editSelected() {
	if jn := a.viewer.selectedNode(); jn != nil {
		a.editNode(jn)
	} else if a.viewer.cur != nil && !a.viewer.cur.editable {
		a.status.Warn("This value is read-only: %s", readOnlyReason(a.viewer.cur))
	} else {
		a.status.Info("Open a JSON value and select a field to edit")
	}
}

func (a *App) reload() {
	if a.client == nil {
		a.serverPicker("")
		return
	}
	if a.browserFocused() {
		a.browser.refresh()
	} else {
		a.viewer.reloadCurrent()
	}
}

func (a *App) newItem() {
	if a.browserFocused() {
		a.newKeyDialog()
	} else {
		a.addChild()
	}
}

func (a *App) deleteItem() {
	switch {
	case !a.browserFocused():
		a.deleteNode()
	case a.browser.level == levelTypes:
		a.browser.forgetType()
	default:
		a.deleteKeyDialog()
	}
}

// keyHints returns the context-sensitive key bar entries, most important
// first (the bar is truncated on narrow terminals).
func (a *App) keyHints() []KeyHint {
	if a.pending != nil {
		return []KeyHint{{"Esc", "Cancel"}, {"F1", "Help"}, {"q", "Quit"}}
	}
	h := []KeyHint{{"F1", "Help"}}
	b := a.browser
	switch {
	case a.client == nil:
		h = append(h, KeyHint{"s", "Servers"})
	case a.viewerFocused() && a.viewer.cur != nil:
		c := a.viewer.cur
		switch {
		case c.mode == modeTree && !c.rawShown:
			h = append(h, KeyHint{"F2", "Save"}, KeyHint{"F4", "Edit"}, KeyHint{"F7", "Add"},
				KeyHint{"F8", "Delete"}, KeyHint{"r", "Rename"}, KeyHint{"/", "Search"}, KeyHint{"F3", "Raw"})
		case c.mode == modeTree:
			h = append(h, KeyHint{"F2", "Save"}, KeyHint{"F3", "Tree"})
		}
		h = append(h, KeyHint{"F5", "Reload"}, KeyHint{"Tab", "List"})
	case b.level == levelTypes:
		h = append(h, KeyHint{"Enter", "Open"}, KeyHint{"/", "Filter"})
		if a.isRememberedType(b.currentItem()) {
			h = append(h, KeyHint{"Del", "Forget"})
		}
		h = append(h, KeyHint{"s", "Servers"})
	case b.level == levelBuckets:
		h = append(h, KeyHint{"Enter", "Open"}, KeyHint{"←", "Back"}, KeyHint{"/", "Filter"},
			KeyHint{"i", "2i query"}, KeyHint{"p", "Props"}, KeyHint{"F5", "Reload"}, KeyHint{"s", "Servers"})
	case b.inQuery:
		h = append(h, KeyHint{"Enter", "View"}, KeyHint{"Esc", "All keys"}, KeyHint{"/", "Filter"},
			KeyHint{"i", "New query"}, KeyHint{"F8", "Delete"})
	default:
		h = append(h, KeyHint{"Enter", "View"}, KeyHint{"←", "Back"}, KeyHint{"/", "Filter"},
			KeyHint{"F7", "New"}, KeyHint{"F8", "Delete"}, KeyHint{"i", "2i query"}, KeyHint{"p", "Props"},
			KeyHint{"F5", "Reload"})
	}
	return append(h, KeyHint{"q", "Quit"})
}

// contextHint is the status line text shown when there is no message.
func (a *App) contextHint() string {
	b, v := a.browser, a.viewer
	switch {
	case a.conn == connConnecting:
		return "Connecting…"
	case a.pending != nil:
		return a.pending.desc + "… Esc cancels and goes back"
	case a.client == nil:
		return "Not connected. Press s to choose a server, or F1 for help."
	case b.filterMode:
		return "Type to filter · ↑↓ move · Enter keep filter · Esc clear"
	case v.searchMode:
		return "Type to search keys and values · Enter next match · Esc close"
	case a.viewerFocused() && v.cur != nil:
		c := v.cur
		switch {
		case c.dirty:
			return "Unsaved changes · F2 save · F5 reload to discard"
		case c.mode == modeTree && !c.rawShown:
			return "Enter expand / edit · ← → collapse / expand · * expand all · / search"
		default:
			return "Read-only view · ↑↓ PgUp PgDn scroll · " + readOnlyReason(c)
		}
	case b.level == levelTypes:
		return "Pick a bucket type. Riak can't list types over HTTP: add one with “other bucket type…” or --types."
	case b.level == levelBuckets:
		return "Pick a bucket. Only buckets that contain keys are listed (a Riak limitation)."
	case b.inQuery:
		return "2i query results · Enter view · Esc back to the full key list"
	default:
		return "Enter view value · F7 new key · F8 delete key · i query a secondary index"
	}
}

// propsModal shows the bucket properties of the open or selected bucket.
func (a *App) propsModal() {
	b := a.browser
	bucket := b.bucket
	if b.level == levelBuckets {
		bucket = b.currentItem()
	}
	if bucket == "" || b.level == levelTypes {
		a.status.Info("Open or select a bucket first")
		return
	}
	btype := b.btype
	a.async("fetching bucket properties", 0, func(ctx context.Context) (any, error) {
		return a.client.BucketProps(ctx, btype, bucket)
	}, func(res any, err error) {
		if err != nil {
			return
		}
		props := res.(map[string]any)
		b.propsCache[btype+"/"+bucket] = props
		keys := make([]string, 0, len(props))
		width := 0
		for k := range props {
			keys = append(keys, k)
			width = max(width, len(k))
		}
		sort.Strings(keys)
		var sb strings.Builder
		sb.WriteString(tMuted() + "Read-only. Change properties with riak-admin bucket-type update or the HTTP props API." + reset + "\n\n")
		for _, k := range keys {
			val, _ := json.Marshal(props[k])
			fmt.Fprintf(&sb, "%s%-*s%s  %s\n", tAccent(), width, tview.Escape(k), reset, highlightJSON(string(val)))
		}
		a.textModal("Bucket properties · "+bucketKey(btype, bucket), sb.String(), 76, 26)
	})
}
