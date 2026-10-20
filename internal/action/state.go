package action

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/masto/starter-upper/internal/items"
)

// cronPlan rewrites the current crontab, applying edit to each line. edit
// returns the replacement and whether the line matched; an empty
// replacement for a matched line deletes it.
func cronPlan(verb, summary string, edit func(string) (string, bool)) (*Plan, error) {
	cur, err := exec.Command("crontab", "-l").Output()
	if err != nil {
		return nil, unsupported("can't read your crontab: %v", err)
	}
	lines := strings.SplitAfter(string(cur), "\n")
	var b strings.Builder
	matched := false
	for _, l := range lines {
		nl := strings.HasSuffix(l, "\n")
		repl, ok := edit(strings.TrimRight(l, "\n"))
		if ok {
			matched = true
			if repl == "" {
				continue
			}
		}
		b.WriteString(repl)
		if nl {
			b.WriteString("\n")
		}
	}
	if !matched {
		return nil, unsupported("the line is no longer in your crontab; rescan with r")
	}
	return &Plan{
		Verb:    verb,
		Summary: summary,
		Steps:   []Step{{Argv: []string{"crontab", "-"}, Stdin: []byte(b.String())}},
	}, nil
}

// SavedLoginItem is an Open at Login item this tool removed and can put
// back.
type SavedLoginItem struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Hidden bool   `json:"hidden"`
}

func (d Dirs) statePath() string { return filepath.Join(d.Base, "disabled-login-items.json") }

// SavedLoginItems returns the login items disabled by this tool.
func SavedLoginItems(d Dirs) ([]SavedLoginItem, error) {
	data, err := os.ReadFile(d.statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s []SavedLoginItem
	return s, json.Unmarshal(data, &s)
}

// SaveLoginItem records a removed login item.
func SaveLoginItem(d Dirs, li SavedLoginItem) error {
	cur, err := SavedLoginItems(d)
	if err != nil {
		return err
	}
	var out []SavedLoginItem
	for _, s := range cur {
		if s.Name != li.Name {
			out = append(out, s)
		}
	}
	return writeSaved(d, append(out, li))
}

// ForgetLoginItem drops a saved login item.
func ForgetLoginItem(d Dirs, name string) error {
	cur, err := SavedLoginItems(d)
	if err != nil {
		return err
	}
	var out []SavedLoginItem
	for _, s := range cur {
		if s.Name != name {
			out = append(out, s)
		}
	}
	return writeSaved(d, out)
}

func writeSaved(d Dirs, s []SavedLoginItem) error {
	if err := os.MkdirAll(d.Base, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(d.statePath(), data, 0o600)
}

// SavedAsItems turns saved login items into disabled LoginItem entries for
// the list.
func SavedAsItems(saved []SavedLoginItem) []items.Item {
	var out []items.Item
	for _, s := range saved {
		it := items.Item{
			Source:    items.LoginItem,
			Label:     s.Name,
			Name:      s.Name,
			PlistPath: s.Path,
			Program:   s.Path,
			Override:  items.OverrideDisabled,
			RunAtLoad: true,
		}
		if s.Hidden {
			it.Note = "opens hidden"
		}
		out = append(out, it)
	}
	return out
}

func removeLoginItemStep(name string) Step {
	return Step{Argv: []string{"osascript", "-l", "JavaScript", "-e",
		`function run(argv) { const se = Application("System Events"); se.loginItems.byName(argv[0]).delete(); }`, name}}
}

func addLoginItemStep(path string, hidden bool) Step {
	h := "false"
	if hidden {
		h = "true"
	}
	return Step{Argv: []string{"osascript", "-l", "JavaScript", "-e",
		`function run(argv) { const se = Application("System Events"); se.loginItems.push(se.LoginItem({path: argv[0], hidden: argv[1] === "true"})); }`, path, h}}
}
