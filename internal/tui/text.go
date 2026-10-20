package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

func pad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// wrap word-wraps s to width w, indenting continuation lines by indent.
func wrap(s string, w, indent int) string {
	if w <= indent+10 {
		return s
	}
	lines := strings.Split(ansi.Wrap(s, w-indent, " /"), "\n")
	for i := 1; i < len(lines); i++ {
		lines[i] = strings.Repeat(" ", indent) + lines[i]
	}
	// The first line can use the full width; rewrapping is not worth it.
	return strings.Join(lines, "\n")
}

// clipLines keeps at most n lines and pads to exactly n.
func clipLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[:n]
		if n > 0 {
			lines[n-1] = lipgloss.NewStyle().Foreground(colMuted).Render("…")
		}
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

var home, _ = os.UserHomeDir()

func shortenHome(p string) string {
	if home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
