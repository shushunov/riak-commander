package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/rivo/tview"
)

// How long a message stays before the status line falls back to the
// contextual hint. Errors linger longer so they are not missed.
const (
	msgTTL = 6 * time.Second
	errTTL = 15 * time.Second
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type severity int

const (
	sevInfo severity = iota
	sevSuccess
	sevWarn
	sevError
)

// StatusBar is the message line (left: messages/spinner/hint, right:
// counts) plus the key bar below it.
type StatusBar struct {
	app    *App
	row    *tview.Flex
	left   *tview.TextView
	info   *tview.TextView
	keybar *tview.TextView

	msg      string // current message ("" → show hint)
	sev      severity
	msgGen   int // bumps on every message; stale fade timers compare it
	busyDesc string
	busyStop chan struct{}
	frame    int

	lastKeys, lastInfo, lastLeft string
}

func newStatusBar(a *App) *StatusBar {
	s := &StatusBar{
		app:    a,
		left:   tview.NewTextView().SetDynamicColors(true).SetWrap(false),
		info:   tview.NewTextView().SetDynamicColors(true).SetWrap(false).SetTextAlign(tview.AlignRight),
		keybar: tview.NewTextView().SetDynamicColors(true).SetWrap(false),
	}
	for _, tv := range []*tview.TextView{s.left, s.info, s.keybar} {
		tv.SetBackgroundColor(th.Surface)
	}
	s.keybar.SetBackgroundColor(th.Bg)
	s.row = tview.NewFlex().
		AddItem(s.left, 0, 3, false).
		AddItem(s.info, 0, 1, false)
	return s
}

// Info, Success, Warn and Err show a message that fades back to the
// contextual hint after a few seconds.
func (s *StatusBar) Info(format string, args ...any) { s.set(sevInfo, fmt.Sprintf(format, args...)) }
func (s *StatusBar) Success(format string, args ...any) {
	s.set(sevSuccess, fmt.Sprintf(format, args...))
}
func (s *StatusBar) Warn(format string, args ...any) { s.set(sevWarn, fmt.Sprintf(format, args...)) }
func (s *StatusBar) Err(err error)                   { s.set(sevError, err.Error()) }

func (s *StatusBar) set(sev severity, msg string) {
	s.msg, s.sev = msg, sev
	s.msgGen++
	gen := s.msgGen
	ttl := msgTTL
	if sev == sevError {
		ttl = errTTL
	}
	time.AfterFunc(ttl, func() {
		s.app.tv.QueueUpdateDraw(func() {
			if s.msgGen == gen {
				s.msg = ""
				s.renderLeft()
			}
		})
	})
	s.renderLeft()
}

// startBusy shows a spinner with desc until stopBusy is called.
func (s *StatusBar) startBusy(desc string) {
	s.stopBusy()
	s.busyDesc = desc
	s.frame = 0
	stop := make(chan struct{})
	s.busyStop = stop
	go func() {
		t := time.NewTicker(90 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				s.app.tv.QueueUpdateDraw(func() {
					if s.busyStop == stop {
						s.frame++
						s.renderLeft()
					}
				})
			}
		}
	}()
	s.renderLeft()
}

// spinner is the current spinner frame; panes showing a loading state use
// it so every indicator on screen turns in step.
func (s *StatusBar) spinner() string { return spinnerFrames[s.frame%len(spinnerFrames)] }

func (s *StatusBar) stopBusy() {
	if s.busyStop != nil {
		close(s.busyStop)
		s.busyStop = nil
	}
	s.busyDesc = ""
	s.renderLeft()
}

func (s *StatusBar) renderLeft() {
	var text string
	switch {
	case s.busyDesc != "":
		text = tAccent() + s.spinner() + reset + " " +
			tText() + tview.Escape(s.busyDesc) + "…" + reset
		if p := s.app.pending; p != nil {
			text += tMuted() + " · " + reset + tAccent() + "Esc" + reset + tMuted() + " to " + p.escAction() + reset
		}
	case s.msg != "":
		icon, colour := "ℹ", tText()
		switch s.sev {
		case sevSuccess:
			icon, colour = "✔", tSuccess()
		case sevWarn:
			icon, colour = "▲", tWarning()
		case sevError:
			icon, colour = "✖", tDanger()
		}
		text = colour + icon + " " + tview.Escape(s.msg) + reset
	default:
		text = tMuted() + tview.Escape(s.app.contextHint()) + reset
	}
	text = " " + text
	if text != s.lastLeft {
		s.lastLeft = text
		s.left.SetText(text)
	}
}

func (s *StatusBar) setInfo(text string) {
	text = tMuted() + tview.Escape(text) + reset + " "
	if text != s.lastInfo {
		s.lastInfo = text
		s.info.SetText(text)
	}
	// the hint depends on context, so refresh it with the rest of the chrome
	if s.msg == "" && s.busyDesc == "" {
		s.renderLeft()
	}
}

// KeyHint is one entry in the key bar.
type KeyHint struct {
	Key   string // "F1", "?", "Enter", …
	Label string
}

// setKeys renders the key bar: key chips in the accent colour followed by
// their labels.
func (s *StatusBar) setKeys(keys []KeyHint) {
	// Drop hints from the end (keeping the final Quit) until the bar fits.
	_, _, width, _ := s.keybar.GetRect()
	chip := func(k KeyHint) int { return len([]rune(k.Key)) + len([]rune(k.Label)) + 5 }
	total := 0
	for _, k := range keys {
		total += chip(k)
	}
	for width > 0 && total > width && len(keys) > 2 {
		drop := len(keys) - 2
		total -= chip(keys[drop])
		keys = append(keys[:drop:drop], keys[drop+1:]...)
	}
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(fgbg(th.OnAccent, th.Accent) + tBold() + " " + tview.Escape(k.Key) + " " + reset)
		label := fgbg(th.Text, th.Surface)
		if th.Mono {
			label = ""
		}
		b.WriteString(label + " " + tview.Escape(k.Label) + " " + reset)
	}
	text := b.String()
	if text != s.lastKeys {
		s.lastKeys = text
		s.keybar.SetText(text)
	}
}

func pluralize(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
