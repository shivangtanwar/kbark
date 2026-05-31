// SPDX-License-Identifier: Apache-2.0

package components

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

// Table wraps bubbles/table with kbark conventions: themed header and
// selection styling, and a stable View() that the parent Model can drop
// into its layout without further configuration.
type Table struct {
	inner table.Model
	th    theme.Theme
}

func NewTable(th theme.Theme, columns []table.Column, rows []table.Row) Table {
	s := table.DefaultStyles()
	s.Header = th.TableHeader
	s.Selected = th.TableSelected
	s.Cell = th.TableCell

	m := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithStyles(s),
	)
	return Table{inner: m, th: th}
}

func (t Table) SetSize(width, height int) Table {
	t.inner.SetWidth(width)
	t.inner.SetHeight(height)
	return t
}

func (t Table) SetRows(rows []table.Row) Table {
	t.inner.SetRows(rows)
	return t
}

func (t Table) SetColumns(columns []table.Column) Table {
	t.inner.SetColumns(columns)
	return t
}

func (t Table) SelectedRow() table.Row {
	return t.inner.SelectedRow()
}

// Cursor returns the index of the currently-selected row. Returns -1
// when the table is empty. Used by views that hold a parallel typed
// objects slice and need to look up the typed object behind the row
// by index (independent of what the first column happens to show).
func (t Table) Cursor() int {
	if len(t.inner.Rows()) == 0 {
		return -1
	}
	return t.inner.Cursor()
}

func (t Table) Update(msg tea.Msg) (Table, tea.Cmd) {
	var cmd tea.Cmd
	t.inner, cmd = t.inner.Update(msg)
	return t, cmd
}

func (t Table) View() string {
	return t.inner.View()
}

// TruncateCell shrinks s to fit `width` columns, replacing the trailing
// characters with a single-cell ellipsis. Returns s unchanged if already
// short enough. Width <= 0 returns an empty string.
func TruncateCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

// FormatAge is FormatAgeRelative bound to time.Now(). Use the relative
// form in tests so the output is deterministic.
func FormatAge(t time.Time) string {
	return FormatAgeRelative(t, time.Now())
}

// FormatAgeRelative renders the age of `t` relative to `now` in the
// compact k9s-style: 47s, 5m, 2h, 3d, 12w, 3y. Negative ages (future
// timestamps) round to "0s".
func FormatAgeRelative(t, now time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		return "0s"
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}

// ColorPhase applies the theme's phase color to the given text.
// Returns text unchanged if the phase is unknown.
func ColorPhase(th theme.Theme, phase, text string) string {
	return th.Phase(phase).Render(text)
}
