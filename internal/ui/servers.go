package ui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/shushunov/riak-commander/internal/history"
)

// serverPicker opens the server dialog: an address field on top and the
// recently used servers below, most recent first. errMsg, when set, explains
// why the dialog opened (a failed connection) and the failed address is
// prefilled so it can be corrected.
func (a *App) serverPicker(errMsg string) {
	entries := a.hist.Entries()

	frame := newDialogFrame("Servers")

	errView := tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	errView.SetBackgroundColor(th.Surface)
	errRows := 0
	if errMsg != "" {
		errView.SetText(tDanger() + "✖ " + tview.Escape(errMsg) + reset)
		errRows = 1 + len([]rune(errMsg))/60
	}

	input := tview.NewInputField().SetLabel("Connect to ").SetFieldWidth(0)
	styleInput(input)
	input.SetBackgroundColor(th.Surface)
	input.SetPlaceholder("host:port, or http(s)://host:port/prefix")
	switch {
	case errMsg != "" && a.failedAddress != "":
		input.SetText(a.failedAddress)
	case len(entries) == 0:
		input.SetText(DefaultAddress)
	}

	recentLabel := tview.NewTextView().SetDynamicColors(true)
	recentLabel.SetBackgroundColor(th.Surface)

	list := tview.NewList().ShowSecondaryText(false).SetHighlightFullLine(true).SetWrapAround(false)
	styleList(list)
	list.SetBackgroundColor(th.Surface)
	list.SetMainTextStyle(tcell.StyleDefault.Background(th.Surface).Foreground(th.Text))

	footer := tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	footer.SetBackgroundColor(th.Surface)

	current := ""
	if a.client != nil {
		current = a.client.Address()
	}

	var cols [2]int
	fill := func(selected int) {
		list.Clear()
		entries = a.hist.Entries()
		cols = serverColumns(entries)
		for i, e := range entries {
			list.AddItem(serverRow(e, cols, e.Address == current, i == selected), "", 0, nil)
		}
		switch {
		case !a.hist.Enabled():
			recentLabel.SetText(tMuted() + "Recent servers: history is off (--no-history)" + reset)
		case len(entries) == 0:
			recentLabel.SetText(tMuted() + "Recent servers: none yet. Servers you connect to are remembered here." + reset)
		default:
			recentLabel.SetText(tMuted() + "Recent servers" + reset)
		}
		if selected >= 0 && selected < len(entries) {
			list.SetCurrentItem(selected)
		}
	}
	list.SetChangedFunc(func(i int, _, _ string, _ rune) {
		// also fires while fill is still adding items
		for j := 0; j < len(entries) && j < list.GetItemCount(); j++ {
			e := entries[j]
			list.SetItemText(j, serverRow(e, cols, e.Address == current, j == i), "")
		}
	})

	setFooter := func(inList bool) {
		if inList {
			footer.SetText(keyHelp("Enter", "connect", "e", "label", "d", "forget", "↑/Tab", "address field", "Esc", "close"))
		} else {
			footer.SetText(keyHelp("Enter", "connect", "↓/Tab", "recent servers", "Ctrl-L", "last server", "Esc", "close"))
		}
	}

	connectTo := func(addr string) {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return
		}
		a.closeModal()
		a.connect(addr, nil)
	}
	connectLast := func() {
		if last, ok := a.hist.Last(); ok {
			connectTo(last.Address)
		} else {
			a.status.Info("No server history yet")
		}
	}

	input.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			if strings.TrimSpace(input.GetText()) == "" && len(entries) > 0 {
				connectTo(entries[list.GetCurrentItem()].Address)
				return
			}
			connectTo(input.GetText())
		case tcell.KeyEscape:
			a.closeModal()
		case tcell.KeyTab:
			if len(entries) > 0 {
				a.tv.SetFocus(list)
			}
		}
	})
	input.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyDown, tcell.KeyBacktab:
			if len(entries) > 0 {
				a.tv.SetFocus(list)
				return nil
			}
		case tcell.KeyCtrlL:
			connectLast()
			return nil
		}
		return ev
	})
	input.SetFocusFunc(func() { setFooter(false) })

	list.SetSelectedFunc(func(i int, _, _ string, _ rune) {
		if i >= 0 && i < len(entries) {
			connectTo(entries[i].Address)
		}
	})
	list.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		i := list.GetCurrentItem()
		switch ev.Key() {
		case tcell.KeyEscape:
			a.closeModal()
			return nil
		case tcell.KeyTab, tcell.KeyBacktab:
			a.tv.SetFocus(input)
			return nil
		case tcell.KeyUp:
			if i == 0 {
				a.tv.SetFocus(input)
				return nil
			}
		case tcell.KeyCtrlL:
			connectLast()
			return nil
		case tcell.KeyDelete:
			ev = tcell.NewEventKey(tcell.KeyRune, 'd', tcell.ModNone)
		}
		if ev.Key() != tcell.KeyRune || i < 0 || i >= len(entries) {
			return ev
		}
		e := entries[i]
		switch ev.Rune() {
		case 'd':
			a.hist.Remove(e.Address)
			a.saveHistory()
			fill(min(i, len(entries)-2))
			a.status.Info("Forgot %s", e.Address)
			if len(entries) == 0 {
				a.tv.SetFocus(input)
			}
		case 'e':
			a.labelServer(e, errMsg)
		case 'j':
			return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
		case 'k':
			return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
		default:
			// any other character starts typing a new address
			a.tv.SetFocus(input)
			input.SetText(input.GetText() + string(ev.Rune()))
		}
		return nil
	})
	list.SetFocusFunc(func() { setFooter(true) })

	fill(0)
	setFooter(false)

	listRows := min(max(len(entries), 1), 10)
	frame.AddItem(errView, errRows, 0, false).
		AddItem(spacer(), 1, 0, false).
		AddItem(input, 1, 0, true).
		AddItem(spacer(), 1, 0, false).
		AddItem(recentLabel, 1, 0, false).
		AddItem(list, listRows, 0, false).
		AddItem(spacer(), 1, 0, false).
		AddItem(footer, 2, 0, false)
	a.openModal(frame, 76, errRows+listRows+9)
	// Returning users usually want a recent server: start in the list unless
	// the address field holds something to fix.
	if len(entries) > 0 && input.GetText() == "" {
		a.tv.SetFocus(list)
	}
}

func spacer() *tview.Box {
	return tview.NewBox().SetBackgroundColor(th.Surface)
}

// serverColumns returns the address and label column widths.
func serverColumns(entries []history.Entry) [2]int {
	var w [2]int
	for _, e := range entries {
		w[0] = max(w[0], len([]rune(e.Address)))
		w[1] = max(w[1], len([]rune(e.Label)))
	}
	return w
}

// serverRow formats one history entry in aligned columns: marker, address,
// label, last use.
func serverRow(e history.Entry, cols [2]int, current, selected bool) string {
	pad := func(s string, w int) string { return s + strings.Repeat(" ", max(0, w-len([]rune(s)))) }
	addr := pad(e.Address, cols[0])
	label := ""
	if cols[1] > 0 {
		label = "  " + pad(e.Label, cols[1])
	}
	when := ago(e.LastUsed)
	if e.UseCount > 1 {
		when += fmt.Sprintf(" · %d×", e.UseCount)
	}
	marker := "  "
	if current {
		marker = "● "
	}
	if selected || th.Mono {
		return " " + marker + tview.Escape(addr+label) + "   " + when
	}
	if current {
		marker = tSuccess() + "● " + reset
	}
	return " " + marker + tText() + tview.Escape(addr) + reset +
		tAccent() + tview.Escape(label) + reset + "   " + tMuted() + when + reset
}

// labelServer edits the label of a history entry, then returns to the picker.
func (a *App) labelServer(e history.Entry, errMsg string) {
	reopen := func() { a.serverPicker(errMsg) }
	a.form(formSpec{
		title: "Label " + e.Address,
		hint:  "A short name shown next to the address, e.g. “staging” or “prod (careful!)”. Leave empty to clear.",
		build: func(f *tview.Form) {
			f.AddInputField("Label", e.Label, 40, nil, nil)
		},
		onOK: func(f *tview.Form) error {
			a.hist.SetLabel(e.Address, strings.TrimSpace(inputText(f, "Label")))
			a.saveHistory()
			return nil
		},
		afterOK:  reopen,
		onCancel: reopen,
	})
}

// friendlyConnError explains a failed ping in terms of what to check.
func friendlyConnError(addr string, err error) string {
	var dnsErr *net.DNSError
	msg := err.Error()
	switch {
	case errors.As(err, &dnsErr):
		return fmt.Sprintf("Host not found for %s. Check the spelling, DNS or VPN.", addr)
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout"):
		return fmt.Sprintf("No answer from %s within %s. Check the address, VPN and firewall.", addr, connectTimeout)
	case strings.Contains(msg, "connection refused"):
		return fmt.Sprintf("Nothing is listening on %s. Is Riak running, and is this its HTTP port (default 8098)?", addr)
	case strings.Contains(msg, "riak:"):
		return fmt.Sprintf("%s answered, but not like Riak's HTTP API (%s). Check the port and any path prefix.", addr, strings.TrimPrefix(msg, "riak: "))
	case strings.Contains(msg, "tls") || strings.Contains(msg, "x509") || strings.Contains(msg, "malformed HTTP"):
		return fmt.Sprintf("TLS/protocol mismatch talking to %s (%s). Use http:// or https:// to match the server.", addr, msg)
	}
	return fmt.Sprintf("Could not connect to %s: %s", addr, msg)
}
