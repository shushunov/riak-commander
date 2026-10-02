package ui

import (
	"strings"

	"github.com/rivo/tview"
)

// Header is the top line: app name, connection indicator, server and the
// breadcrumb of the current location.
type Header struct {
	view *tview.TextView
	last string
}

func newHeader() *Header {
	h := &Header{view: tview.NewTextView().SetDynamicColors(true).SetWrap(false)}
	h.view.SetBackgroundColor(th.Surface)
	return h
}

func (h *Header) render(a *App) {
	var b strings.Builder
	b.WriteString(fgbg(th.OnAccent, th.Accent) + tBold() + " Riak Commander " + reset)
	if !th.Mono {
		b.WriteString(fg(th.Text))
	}
	b.WriteString("  ")

	switch a.conn {
	case connUp:
		b.WriteString(tSuccess() + "●" + reset + " " + tText() + tview.Escape(a.client.Address()) + reset)
	case connConnecting:
		b.WriteString(tWarning() + "◌" + reset + " " + tMuted() + "connecting to " + tview.Escape(a.connAddress) + "…" + reset)
	case connDown:
		b.WriteString(tDanger() + "●" + reset + " " + tMuted() + "not connected" + reset)
		if a.connAddress != "" {
			b.WriteString(tMuted() + " (" + tview.Escape(a.connAddress) + " unreachable)" + reset)
		}
	default:
		b.WriteString(tMuted() + "○ not connected" + reset)
	}

	if crumbs := a.breadcrumb(); len(crumbs) > 0 {
		b.WriteString(tMuted() + "   │  " + reset)
		for i, c := range crumbs {
			if i > 0 {
				b.WriteString(tMuted() + " › " + reset)
			}
			if i == len(crumbs)-1 {
				b.WriteString(tAccent() + tview.Escape(c) + reset)
			} else {
				b.WriteString(tText() + tview.Escape(c) + reset)
			}
		}
	}
	text := b.String()
	if text != h.last {
		h.last = text
		h.view.SetText(text)
	}
}

// breadcrumb is the current location: type › bucket › key.
func (a *App) breadcrumb() []string {
	b := a.browser
	var out []string
	if b.level >= levelBuckets {
		out = append(out, orDefault(b.btype))
	}
	if b.level >= levelKeys {
		out = append(out, b.bucket)
		if b.inResults {
			out = append(out, b.queryDesc)
		}
	}
	if p := a.pending; p != nil && p.kind == pendingValue {
		out = append(out, a.viewer.loadingKey)
	} else if c := a.viewer.cur; c != nil && c.obj.Bucket == b.bucket && b.level == levelKeys {
		out = append(out, c.obj.Key)
	}
	return out
}
