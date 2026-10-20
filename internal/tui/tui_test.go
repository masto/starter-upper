package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/masto/starter-upper/internal/check"
	"github.com/masto/starter-upper/internal/items"
	"github.com/masto/starter-upper/internal/scan"
)

func testModel(t *testing.T) Model {
	t.Helper()
	list := []items.Item{
		{Source: items.Daemon, Label: "com.cloudflare.cloudflared", Name: "com.cloudflare.cloudflared",
			PlistPath: "/Library/LaunchDaemons/com.cloudflare.cloudflared.plist", Program: "/opt/homebrew/bin/cloudflared",
			Args: []string{"/opt/homebrew/bin/cloudflared", "tunnel", "run", "--token", "SECRETVALUE"}, RunAtLoad: true,
			Domain: "system", Loaded: true, PID: 837, PlistMode: 0o644},
		{Source: items.UserAgent, Label: "com.example.gone", Name: "Gone", PlistPath: "/Users/x/Library/LaunchAgents/com.example.gone.plist",
			Program: "/Applications/Gone.app/Contents/MacOS/Gone", RunAtLoad: true, Domain: "gui/501"},
		{Source: items.LoginItem, Label: "Maccy", Name: "Maccy", PlistPath: "/Applications/Maccy.app", Program: "/Applications/Maccy.app", RunAtLoad: true},
	}
	check.Run(list, check.Env{UID: 501})
	sortItems(list)
	m := New(scan.Config{UID: 501})
	m.dirs.Base = t.TempDir()
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mm, _ = mm.Update(scanDoneMsg{scan.Result{Items: list}})
	return mm.(Model)
}

func key(m Model, k string) Model {
	var msg tea.KeyMsg
	switch k {
	case "down":
		msg = tea.KeyMsg{Type: tea.KeyDown}
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	mm, _ := m.Update(msg)
	return mm.(Model)
}

func plain(m Model) string { return ansi.Strip(m.View()) }

func TestViewFitsAndMasksSecrets(t *testing.T) {
	m := testModel(t)
	for m.selected().Source != items.Daemon {
		m = key(m, "down")
	}
	v := plain(m)
	if os.Getenv("SHOW_VIEW") != "" {
		t.Log("\n" + v)
	}
	lines := strings.Split(v, "\n")
	if len(lines) != 30 {
		t.Errorf("view has %d lines, want exactly 30", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 100 {
			t.Errorf("line %d is %d wide: %q", i, w, l)
		}
	}
	if strings.Contains(v, "SECRETVALUE") {
		t.Error("secret argument shown in the detail pane")
	}
	if !strings.Contains(v, "••••••") {
		t.Error("masked secret placeholder missing")
	}
}

func TestFilterAndFlagged(t *testing.T) {
	m := testModel(t)
	m = key(m, "f")
	if len(m.visible) != 2 {
		t.Errorf("flagged-only shows %d items, want 2", len(m.visible))
	}
	m = key(m, "f")
	m = key(m, "/")
	for _, r := range "maccy" {
		m = key(m, string(r))
	}
	if len(m.visible) != 1 || m.selected().Name != "Maccy" {
		t.Errorf("filter left %d items", len(m.visible))
	}
	m = key(m, "esc")
	if len(m.visible) != 3 {
		t.Errorf("esc should clear the filter, %d visible", len(m.visible))
	}
}

func TestDeleteConfirmCancel(t *testing.T) {
	m := testModel(t)
	for m.selected().Name != "Gone" {
		m = key(m, "down")
	}
	m = key(m, "x")
	if m.mode != confirming || m.plan == nil || m.plan.Verb != "delete" {
		t.Fatalf("x didn't open a delete confirmation (mode %v)", m.mode)
	}
	v := plain(m)
	if os.Getenv("SHOW_VIEW") != "" {
		t.Log("\n" + v)
	}
	if !strings.Contains(v, "rm -f") || !strings.Contains(v, "A copy is saved") || strings.Contains(v, "administrator") {
		t.Errorf("confirmation doesn't show the command and backup:\n%s", v)
	}
	m = key(m, "n")
	if m.mode != browsing || m.plan != nil {
		t.Error("n should cancel")
	}
}

func TestUnsupportedActionShowsReason(t *testing.T) {
	m := testModel(t)
	m.all = append(m.all, items.Item{Source: items.AppHelper, Name: "X: Helper", ParentApp: "/Applications/X.app"})
	m.refilter()
	m.cursor = len(m.visible) - 1
	m = key(m, "x")
	if m.mode != browsing || !m.statusErr || !strings.Contains(m.status, "System Settings") {
		t.Errorf("status = %q (err %v, mode %v)", m.status, m.statusErr, m.mode)
	}
}
