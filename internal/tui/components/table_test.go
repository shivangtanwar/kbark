// SPDX-License-Identifier: Apache-2.0

package components_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/table"

	"github.com/shivangtanwar/kbark/internal/tui/components"
	"github.com/shivangtanwar/kbark/internal/tui/theme"
)

var updateGolden = flag.Bool("update", false, "update goldenfiles")

// TestMain disables ANSI colors so the goldenfile is byte-for-byte stable
// regardless of the developer's terminal capabilities.
func TestMain(m *testing.M) {
	_ = os.Setenv("NO_COLOR", "1")
	_ = os.Setenv("TERM", "dumb")
	os.Exit(m.Run())
}

func TestFormatAgeRelative(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		offset time.Duration
		want   string
	}{
		{0, "0s"},
		{30 * time.Second, "30s"},
		{2 * time.Minute, "2m"},
		{3 * time.Hour, "3h"},
		{2 * 24 * time.Hour, "2d"},
		{3 * 7 * 24 * time.Hour, "3w"},
		{2 * 365 * 24 * time.Hour, "2y"},
		{-1 * time.Hour, "0s"}, // future timestamp clamps to 0s
	}
	for _, c := range cases {
		got := components.FormatAgeRelative(now.Add(-c.offset), now)
		if got != c.want {
			t.Errorf("FormatAgeRelative(-%v) = %q, want %q", c.offset, got, c.want)
		}
	}
}

func TestTruncateCell(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"short", 10, "short"},
		{"toolong", 5, "tool…"},
		{"abc", 1, "…"},
		{"abc", 0, ""},
		{"abc", -3, ""},
	}
	for _, c := range cases {
		got := components.TruncateCell(c.in, c.width)
		if got != c.want {
			t.Errorf("TruncateCell(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
		}
	}
}

func TestTable_renders200Rows(t *testing.T) {
	rows := makeFixtureRows(200)
	columns := []table.Column{
		{Title: "NAME", Width: 16},
		{Title: "READY", Width: 7},
		{Title: "STATUS", Width: 12},
		{Title: "RESTARTS", Width: 10},
		{Title: "AGE", Width: 8},
	}

	tbl := components.NewTable(theme.Default(), columns, rows)
	tbl = tbl.SetSize(80, 25)
	got := tbl.View()

	goldenPath := filepath.Join("testdata", "table_200rows.golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run `go test ./internal/tui/components/... -update` to create): %v", err)
	}
	if string(want) != got {
		t.Errorf("table output drifted; rerun with `-update` if change is intentional\n--- got (%d bytes) ---\n%s\n--- want (%d bytes) ---\n%s",
			len(got), got, len(want), want)
	}
}

func makeFixtureRows(n int) []table.Row {
	reference := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	rows := make([]table.Row, n)
	for i := 0; i < n; i++ {
		age := reference.Add(-time.Duration(i+1) * time.Minute)
		rows[i] = table.Row{
			fmt.Sprintf("pod-%03d", i+1),
			"1/1",
			phaseFor(i),
			strconv.Itoa(i % 3),
			components.FormatAgeRelative(age, reference),
		}
	}
	return rows
}

func phaseFor(i int) string {
	switch i % 5 {
	case 0:
		return "Running"
	case 1:
		return "Pending"
	case 2:
		return "Failed"
	case 3:
		return "Succeeded"
	default:
		return "Unknown"
	}
}
