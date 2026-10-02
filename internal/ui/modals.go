package ui

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const modalPage = "modal"

// centered draws a dialog of a preferred size in the middle of the screen,
// shrinking it to fit small terminals. Sizing happens at draw time, so it
// also adapts when the terminal is resized while the dialog is open.
type centered struct {
	*tview.Box
	p             tview.Primitive
	width, height int
}

func center(p tview.Primitive, width, height int) *centered {
	return &centered{Box: tview.NewBox(), p: p, width: width, height: height}
}

func (c *centered) Draw(screen tcell.Screen) {
	sw, sh := screen.Size()
	w, h := min(c.width, sw-2), min(c.height, sh-2)
	c.p.SetRect((sw-w)/2, (sh-h)/2, w, h)
	c.p.Draw(screen)
}

func (c *centered) Focus(delegate func(p tview.Primitive)) { delegate(c.p) }
func (c *centered) HasFocus() bool                         { return c.p.HasFocus() }

func (c *centered) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return c.p.InputHandler()
}

func (c *centered) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		consumed, capture := c.p.MouseHandler()(action, event, setFocus)
		// clicks outside the dialog must not reach the panes underneath
		return consumed || action != tview.MouseMove, capture
	}
}

func (a *App) openModal(p tview.Primitive, width, height int) {
	if a.modalOpen {
		a.closeModal()
	}
	a.lastFocus = a.tv.GetFocus()
	a.modalOpen = true
	a.pages.AddPage(modalPage, center(p, width, height), true, true)
	a.tv.SetFocus(p)
}

func (a *App) closeModal() {
	if !a.modalOpen {
		return
	}
	a.pages.RemovePage(modalPage)
	a.modalOpen = false
	if a.lastFocus != nil {
		a.tv.SetFocus(a.lastFocus)
	}
}

func (a *App) newModal(title, msg string, buttons []string, onDone func(label string)) *tview.Modal {
	m := tview.NewModal().
		SetText(msg).
		AddButtons(buttons).
		SetDoneFunc(func(_ int, label string) {
			a.closeModal()
			if onDone != nil {
				onDone(label)
			}
		})
	styleModal(m)
	m.SetTitle(" " + title + " ")
	return m
}

// modalHeight estimates the rows a tview.Modal needs for msg at width.
func modalHeight(msg string, width int) int {
	lines := 0
	for _, l := range strings.Split(msg, "\n") {
		lines += 1 + tview.TaggedStringWidth(l)/(width-8)
	}
	return lines + 6
}

// confirm shows a yes/no dialog; "No" is focused by default.
func (a *App) confirm(title, msg string, onYes func()) {
	m := a.newModal(title, msg, []string{"Yes", "No"}, func(label string) {
		if label == "Yes" {
			onYes()
		}
	})
	m.SetFocus(1)
	a.openModal(m, 64, modalHeight(msg, 64))
}

// choice shows a modal with arbitrary buttons; onDone receives the label
// ("" when dismissed with Esc).
func (a *App) choice(title, msg string, buttons []string, onDone func(label string)) {
	m := a.newModal(title, msg, buttons, onDone)
	a.openModal(m, 64, modalHeight(msg, 64))
}

// alert shows an error/info dialog.
func (a *App) alert(title, msg string) {
	m := a.newModal(title, msg, []string{"OK"}, nil)
	a.openModal(m, 64, modalHeight(msg, 64))
}

// formSpec describes a dialog form.
type formSpec struct {
	title   string
	hint    string // explanatory text shown under the fields
	width   int    // default 70
	okLabel string // default "OK"
	build   func(f *tview.Form)
	// onOK validates and acts; a non-nil error is shown inside the dialog
	// and keeps it open.
	onOK func(f *tview.Form) error
	// onCancel (optional) runs after the dialog is dismissed without OK.
	onCancel func()
	// afterOK (optional) runs after a successful OK has closed the dialog,
	// e.g. to open another dialog.
	afterOK func()
}

// form builds a modal form with OK/Cancel semantics, an explanatory hint,
// inline validation errors and a key-help footer. Enter in a single-line
// field submits; Esc cancels.
func (a *App) form(spec formSpec) {
	if spec.width == 0 {
		spec.width = 70
	}
	if spec.okLabel == "" {
		spec.okLabel = "OK"
	}
	f := tview.NewForm()
	styleForm(f)
	f.SetBorderPadding(1, 0, 1, 1)
	spec.build(f)
	for i := 0; i < f.GetFormItemCount(); i++ {
		switch it := f.GetFormItem(i).(type) {
		case *tview.InputField:
			styleInput(it)
		case *tview.DropDown:
			styleDropDown(it)
		}
	}

	errView := tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	errView.SetBackgroundColor(th.Surface)
	hintView := tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	hintView.SetBackgroundColor(th.Surface)
	hintView.SetText(tMuted() + tview.Escape(spec.hint) + reset)
	footer := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight)
	footer.SetBackgroundColor(th.Surface)
	footer.SetText(keyHelp("Enter", "confirm", "Tab", "next field", "Esc", "cancel"))

	frame := newDialogFrame(tview.Escape(spec.title))

	submit := func() {
		if err := spec.onOK(f); err != nil {
			errView.SetText(tDanger() + "✖ " + tview.Escape(err.Error()) + reset)
			frame.ResizeItem(errView, 1+len(err.Error())/(spec.width-4), 0)
			return
		}
		a.closeModal()
		if spec.afterOK != nil {
			spec.afterOK()
		}
	}
	cancel := func() {
		a.closeModal()
		if spec.onCancel != nil {
			spec.onCancel()
		}
	}
	f.AddButton(spec.okLabel, submit)
	f.AddButton("Cancel", cancel)
	f.SetCancelFunc(cancel)
	f.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEscape:
			cancel()
			return nil
		case tcell.KeyEnter:
			idx, _ := f.GetFocusedItemIndex()
			if idx >= 0 {
				if in, ok := f.GetFormItem(idx).(*tview.InputField); ok && !hasAutocomplete[in] {
					submit()
					return nil
				}
			}
		case tcell.KeyCtrlS:
			submit()
			return nil
		}
		return ev
	})

	formRows := 1 + 1 // top padding + buttons row
	for i := 0; i < f.GetFormItemCount(); i++ {
		formRows += f.GetFormItem(i).GetFieldHeight() + 1 // item + spacing
	}
	hintRows := 0
	if spec.hint != "" {
		hintRows = 1 + len([]rune(spec.hint))/(spec.width-4)
	}
	frame.AddItem(f, formRows, 0, true).
		AddItem(errView, 0, 0, false).
		AddItem(spacer(), 1, 0, false).
		AddItem(hintView, hintRows, 0, false).
		AddItem(spacer(), 1, 0, false).
		AddItem(footer, 1, 0, false)
	a.openModal(frame, spec.width, formRows+hintRows+3+2) // + spacers, footer, borders
}

// hasAutocomplete marks input fields whose Enter key belongs to the
// autocomplete list rather than form submission.
var hasAutocomplete = map[*tview.InputField]bool{}

// inputText returns the text of the input field labelled label.
func inputText(f *tview.Form, label string) string {
	if in, ok := f.GetFormItemByLabel(label).(*tview.InputField); ok {
		return in.GetText()
	}
	return ""
}

// placeholder sets the placeholder of the input field labelled label.
func placeholder(f *tview.Form, label, text string) {
	if in, ok := f.GetFormItemByLabel(label).(*tview.InputField); ok {
		in.SetPlaceholder(text)
	}
}

// dropdownValue returns the selected option of the dropdown labelled label.
func dropdownValue(f *tview.Form, label string) string {
	if dd, ok := f.GetFormItemByLabel(label).(*tview.DropDown); ok {
		_, v := dd.GetCurrentOption()
		return v
	}
	return ""
}

// textModal shows scrollable read-only text (bucket props).
func (a *App) textModal(title, text string, width, height int) {
	tv := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	tv.SetBackgroundColor(th.Surface)
	tv.SetText(text)
	frame := newDialogFrame(tview.Escape(title)).AddItem(tv, 0, 1, true)
	footer := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignRight)
	footer.SetBackgroundColor(th.Surface)
	footer.SetText(keyHelp("↑↓", "scroll", "Esc", "close"))
	frame.AddItem(footer, 1, 0, false)
	tv.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch {
		case ev.Key() == tcell.KeyEscape, ev.Key() == tcell.KeyEnter, ev.Rune() == 'q':
			a.closeModal()
			return nil
		case ev.Rune() == 'j':
			return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
		case ev.Rune() == 'k':
			return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
		}
		return ev
	})
	a.openModal(frame, width, height)
}
