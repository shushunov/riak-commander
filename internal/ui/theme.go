package ui

import (
	"fmt"
	"os"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Theme is the semantic palette every widget draws from. Code never uses raw
// colour names; it asks for a role (accent, muted, danger, …) so palettes can
// be swapped wholesale.
type Theme struct {
	Name string

	Bg         tcell.Color // pane background
	Surface    tcell.Color // header, status line, dialogs
	SurfaceAlt tcell.Color // input fields, unfocused selection
	Border     tcell.Color // inactive borders
	Accent     tcell.Color // focus border, highlights, key chips
	OnAccent   tcell.Color // text drawn on Accent
	Text       tcell.Color
	Muted      tcell.Color // secondary text, hints
	Success    tcell.Color
	Warning    tcell.Color
	Danger     tcell.Color

	// JSON syntax colours
	JSONKey    tcell.Color
	JSONString tcell.Color
	JSONNumber tcell.Color
	JSONBool   tcell.Color
	JSONNull   tcell.Color

	// Mono drops all colour and relies on bold/reverse/dim (NO_COLOR).
	Mono bool
}

func hexColor(s string) tcell.Color { return tcell.GetColor(s) }

// DarkTheme is the default: deep slate with a teal accent.
var DarkTheme = Theme{
	Name:       "dark",
	Bg:         hexColor("#161a20"),
	Surface:    hexColor("#1f252d"),
	SurfaceAlt: hexColor("#2b333e"),
	Border:     hexColor("#3b4452"),
	Accent:     hexColor("#4fd1c5"),
	OnAccent:   hexColor("#0d1117"),
	Text:       hexColor("#d8dee6"),
	Muted:      hexColor("#7f8a98"),
	Success:    hexColor("#7ed491"),
	Warning:    hexColor("#f0c674"),
	Danger:     hexColor("#f47c7c"),
	JSONKey:    hexColor("#8ab4f8"),
	JSONString: hexColor("#a5d6a7"),
	JSONNumber: hexColor("#f8b878"),
	JSONBool:   hexColor("#c7a0f0"),
	JSONNull:   hexColor("#7f8a98"),
}

// LightTheme suits light terminal backgrounds.
var LightTheme = Theme{
	Name:       "light",
	Bg:         hexColor("#fbfbf8"),
	Surface:    hexColor("#eceff2"),
	SurfaceAlt: hexColor("#dce2e8"),
	Border:     hexColor("#b4bcc6"),
	Accent:     hexColor("#0b7f75"),
	OnAccent:   hexColor("#ffffff"),
	Text:       hexColor("#1f2328"),
	Muted:      hexColor("#66707b"),
	Success:    hexColor("#1a7f37"),
	Warning:    hexColor("#9a6700"),
	Danger:     hexColor("#c62828"),
	JSONKey:    hexColor("#0550ae"),
	JSONString: hexColor("#116329"),
	JSONNumber: hexColor("#953800"),
	JSONBool:   hexColor("#8250df"),
	JSONNull:   hexColor("#66707b"),
}

// MonoTheme uses the terminal's own colours only (NO_COLOR, or --theme mono).
var MonoTheme = Theme{
	Name: "mono", Mono: true,
	Bg: tcell.ColorDefault, Surface: tcell.ColorDefault, SurfaceAlt: tcell.ColorDefault,
	Border: tcell.ColorDefault, Accent: tcell.ColorDefault, OnAccent: tcell.ColorDefault,
	Text: tcell.ColorDefault, Muted: tcell.ColorDefault, Success: tcell.ColorDefault,
	Warning: tcell.ColorDefault, Danger: tcell.ColorDefault,
	JSONKey: tcell.ColorDefault, JSONString: tcell.ColorDefault, JSONNumber: tcell.ColorDefault,
	JSONBool: tcell.ColorDefault, JSONNull: tcell.ColorDefault,
}

// ThemeNames lists the accepted --theme values.
var ThemeNames = []string{"dark", "light", "mono"}

// ThemeByName resolves a --theme value. An empty name picks mono when the
// NO_COLOR convention (https://no-color.org) is in effect, dark otherwise.
func ThemeByName(name string) (Theme, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		if os.Getenv("NO_COLOR") != "" {
			return MonoTheme, nil
		}
		return DarkTheme, nil
	case "dark":
		return DarkTheme, nil
	case "light":
		return LightTheme, nil
	case "mono", "none", "no-color":
		return MonoTheme, nil
	}
	return Theme{}, fmt.Errorf("unknown theme %q (choose %s)", name, strings.Join(ThemeNames, ", "))
}

// th is the active theme. It is set once by New before any widget exists.
var th = DarkTheme

// apply installs the theme as tview's global defaults.
func (t Theme) apply() {
	tview.Styles = tview.Theme{
		PrimitiveBackgroundColor:    t.Bg,
		ContrastBackgroundColor:     t.SurfaceAlt,
		MoreContrastBackgroundColor: t.Border,
		BorderColor:                 t.Border,
		TitleColor:                  t.Text,
		GraphicsColor:               t.Border,
		PrimaryTextColor:            t.Text,
		SecondaryTextColor:          t.Muted,
		TertiaryTextColor:           t.Muted,
		InverseTextColor:            t.OnAccent,
		ContrastSecondaryTextColor:  t.Muted,
	}
	tview.Borders.Horizontal = '─'
	tview.Borders.Vertical = '│'
	tview.Borders.TopLeft = '╭'
	tview.Borders.TopRight = '╮'
	tview.Borders.BottomLeft = '╰'
	tview.Borders.BottomRight = '╯'
	tview.Borders.HorizontalFocus = '─'
	tview.Borders.VerticalFocus = '│'
	tview.Borders.TopLeftFocus = '╭'
	tview.Borders.TopRightFocus = '╮'
	tview.Borders.BottomLeftFocus = '╰'
	tview.Borders.BottomRightFocus = '╯'
}

// fg returns a tview colour tag for c ("" in mono mode, so text keeps the
// terminal default).
func fg(c tcell.Color) string {
	if th.Mono || c == tcell.ColorDefault {
		return ""
	}
	return fmt.Sprintf("[%s]", colorName(c))
}

// fgbg returns a tag setting foreground and background.
func fgbg(f, b tcell.Color) string {
	if th.Mono {
		return "[::r]"
	}
	return fmt.Sprintf("[%s:%s]", colorName(f), colorName(b))
}

// reset ends any colour/attribute tag.
const reset = "[-:-:-]"

func colorName(c tcell.Color) string {
	if c == tcell.ColorDefault {
		return "-"
	}
	return fmt.Sprintf("#%06x", c.Hex())
}

// Role shorthands used in formatted text.
func tAccent() string  { return fg(th.Accent) }
func tMuted() string   { return monoAttr(fg(th.Muted), "[::d]") }
func tText() string    { return fg(th.Text) }
func tSuccess() string { return fg(th.Success) }
func tWarning() string { return monoAttr(fg(th.Warning), "[::b]") }
func tDanger() string  { return monoAttr(fg(th.Danger), "[::b]") }
func tBold() string    { return "[::b]" }

func monoAttr(colour, mono string) string {
	if th.Mono {
		return mono
	}
	return colour
}

// ---- widget styling helpers ----

func styleBox(b *tview.Box) {
	b.SetBackgroundColor(th.Bg)
	b.SetBorderColor(th.Border)
	b.SetTitleColor(th.Text)
}

func selectedStyle(focused bool) tcell.Style {
	if th.Mono {
		if focused {
			return tcell.StyleDefault.Reverse(true)
		}
		return tcell.StyleDefault.Underline(true)
	}
	if focused {
		return tcell.StyleDefault.Background(th.Accent).Foreground(th.OnAccent).Bold(true)
	}
	return tcell.StyleDefault.Background(th.SurfaceAlt).Foreground(th.Text)
}

func styleList(l *tview.List) {
	l.SetBackgroundColor(th.Bg)
	l.SetMainTextStyle(tcell.StyleDefault.Foreground(th.Text).Background(th.Bg))
	l.SetSecondaryTextStyle(tcell.StyleDefault.Foreground(th.Muted).Background(th.Bg))
	l.SetSelectedStyle(selectedStyle(true))
}

func styleInput(i *tview.InputField) {
	i.SetBackgroundColor(th.Surface) // the row beyond the field width
	i.SetFieldStyle(tcell.StyleDefault.Background(th.SurfaceAlt).Foreground(th.Text))
	i.SetPlaceholderStyle(tcell.StyleDefault.Background(th.SurfaceAlt).Foreground(th.Muted))
	i.SetLabelStyle(tcell.StyleDefault.Foreground(th.Muted).Background(th.Surface))
	i.SetAutocompleteStyles(th.SurfaceAlt,
		tcell.StyleDefault.Background(th.SurfaceAlt).Foreground(th.Text),
		selectedStyle(true))
	if th.Mono {
		i.SetFieldStyle(tcell.StyleDefault.Underline(true))
		i.SetPlaceholderStyle(tcell.StyleDefault.Dim(true).Underline(true))
	}
}

func styleForm(f *tview.Form) {
	f.SetBackgroundColor(th.Surface)
	f.SetLabelColor(th.Muted)
	f.SetFieldStyle(tcell.StyleDefault.Background(th.SurfaceAlt).Foreground(th.Text))
	f.SetButtonStyle(tcell.StyleDefault.Background(th.SurfaceAlt).Foreground(th.Text))
	f.SetButtonActivatedStyle(selectedStyle(true))
	if th.Mono {
		f.SetFieldStyle(tcell.StyleDefault.Underline(true))
		f.SetButtonStyle(tcell.StyleDefault)
	}
}

func styleModal(m *tview.Modal) {
	m.SetBackgroundColor(th.Surface)
	m.SetTextColor(th.Text)
	m.SetBorderColor(th.Accent)
	m.SetTitleColor(th.Text)
	m.SetButtonStyle(tcell.StyleDefault.Background(th.SurfaceAlt).Foreground(th.Text))
	m.SetButtonActivatedStyle(selectedStyle(true))
}

func styleDropDown(d *tview.DropDown) {
	d.SetBackgroundColor(th.Surface)
	d.SetTextOptions(" ", " ", "", "", "")
	d.SetFieldStyle(tcell.StyleDefault.Background(th.SurfaceAlt).Foreground(th.Text))
	d.SetFocusedStyle(selectedStyle(true))
	d.SetPrefixStyle(tcell.StyleDefault.Background(th.SurfaceAlt).Foreground(th.Muted))
	d.SetListStyles(tcell.StyleDefault.Background(th.SurfaceAlt).Foreground(th.Text), selectedStyle(true))
	if th.Mono {
		d.SetFieldStyle(tcell.StyleDefault.Underline(true))
	}
}

// keyHelp renders "key label · key label" footers: keys in the accent
// colour, labels muted.
func keyHelp(pairs ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(pairs); i += 2 {
		if i > 0 {
			b.WriteString(tMuted() + " · " + reset)
		}
		b.WriteString(tAccent() + tview.Escape(pairs[i]) + reset + " " + tMuted() + tview.Escape(pairs[i+1]) + reset)
	}
	return b.String()
}

// styleDialogBox styles the outer frame of a dialog.
func styleDialogBox(b *tview.Box) {
	b.SetBackgroundColor(th.Surface)
	b.SetBorderColor(th.Accent)
	b.SetTitleColor(th.Text)
	b.SetBorderPadding(0, 0, 1, 1)
}

// newDialogFrame returns a bordered, titled column layout for dialogs. Flex
// does not clear its own background, so the frame paints its interior
// (including padding) before its children draw; otherwise whatever is
// underneath the dialog would show through.
func newDialogFrame(title string) *tview.Flex {
	f := tview.NewFlex().SetDirection(tview.FlexRow)
	styleDialogBox(f.Box)
	f.SetBorder(true).SetTitle(" " + title + " ")
	f.SetDrawFunc(func(screen tcell.Screen, x, y, w, h int) (int, int, int, int) {
		style := tcell.StyleDefault.Background(th.Surface).Foreground(th.Text)
		for row := y + 1; row < y+h-1; row++ {
			for col := x + 1; col < x+w-1; col++ {
				screen.SetContent(col, row, ' ', nil, style)
			}
		}
		return x + 2, y + 1, w - 4, h - 2
	})
	return f
}
