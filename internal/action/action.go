// Package action builds and runs the changes the tool can make: disabling
// and re-enabling items without losing anything, and deleting them after a
// backup.
//
// Every change is a Plan of plain commands so the UI can show exactly what
// will run before asking for confirmation.
package action

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/masto/starter-upper/internal/items"
)

// Step is one command. Root steps run through `sudo -n`, so the caller must
// have refreshed sudo's timestamp first (see Plan.NeedsRoot).
type Step struct {
	Argv      []string
	Stdin     []byte
	Root      bool
	IgnoreErr bool // e.g. bootout of a job that isn't loaded
}

func (s Step) String() string {
	cmd := strings.Join(quoteAll(s.Argv), " ")
	if s.Root {
		cmd = "sudo " + cmd
	}
	if s.Stdin != nil {
		cmd += "  (new contents on stdin)"
	}
	return cmd
}

// Plan is a ready-to-run change.
type Plan struct {
	Verb    string // "disable", "enable", "delete"
	Summary string
	Warning string // extra caution shown in the confirmation, may be empty
	Steps   []Step

	// Backup copies BackupFrom into a timestamped folder before any step
	// runs. Empty means no backup.
	BackupFrom string
	BackupRoot bool // source isn't readable without root

	// State changes for login items, applied after the steps succeed.
	RememberLoginItem *SavedLoginItem
	ForgetLoginItem   string
}

// NeedsRoot reports whether any step or the backup needs sudo.
func (p *Plan) NeedsRoot() bool {
	if p.BackupRoot {
		return true
	}
	for _, s := range p.Steps {
		if s.Root {
			return true
		}
	}
	return false
}

// ErrUnsupported is returned when an item can't be changed by this tool.
type ErrUnsupported struct{ Reason string }

func (e ErrUnsupported) Error() string { return e.Reason }

func unsupported(format string, a ...any) error {
	return ErrUnsupported{Reason: fmt.Sprintf(format, a...)}
}

// Disable stops an item from starting, in a way Enable can undo.
func Disable(it *items.Item) (*Plan, error) {
	switch it.Source {
	case items.UserAgent, items.GlobalAgent, items.Daemon:
		if it.Disabled() {
			return nil, unsupported("already disabled")
		}
		target := it.Domain + "/" + it.Label
		root := it.Domain == "system"
		p := &Plan{
			Verb:    "disable",
			Summary: "Disable " + it.Label + " in launchd (" + it.Domain + ") and stop it if running. The plist stays put.",
			Steps: []Step{
				{Argv: []string{"launchctl", "disable", target}, Root: root},
				{Argv: []string{"launchctl", "bootout", target}, Root: root, IgnoreErr: true},
			},
		}
		if it.Source == items.GlobalAgent {
			p.Warning = "This affects only your account. Other users still load it; delete it to remove it for everyone."
		}
		return p, nil
	case items.LoginItem:
		if it.Override == items.OverrideDisabled {
			return nil, unsupported("already disabled")
		}
		return &Plan{
			Verb:              "disable",
			Summary:           "Remove " + it.Name + " from Open at Login, remembering it so it can be restored.",
			Steps:             []Step{removeLoginItemStep(it.Name)},
			RememberLoginItem: &SavedLoginItem{Name: it.Name, Path: it.Program, Hidden: it.Note == "opens hidden"},
		}, nil
	case items.Cron:
		if strings.HasPrefix(it.Label, items.CronDisabledPrefix) {
			return nil, unsupported("already disabled")
		}
		return cronPlan("disable", "Comment out this crontab line: "+it.Label, func(line string) (string, bool) {
			if strings.TrimSpace(line) == it.Label {
				return items.CronDisabledPrefix + line, true
			}
			return line, false
		})
	}
	return nil, settingsOnly(it)
}

// Enable undoes Disable.
func Enable(it *items.Item) (*Plan, error) {
	switch it.Source {
	case items.UserAgent, items.GlobalAgent, items.Daemon:
		if !it.Disabled() {
			return nil, unsupported("not disabled")
		}
		target := it.Domain + "/" + it.Label
		root := it.Domain == "system"
		return &Plan{
			Verb:    "enable",
			Summary: "Re-enable " + it.Label + " in launchd (" + it.Domain + ") and load it now.",
			Steps: []Step{
				{Argv: []string{"launchctl", "enable", target}, Root: root},
				{Argv: []string{"launchctl", "bootstrap", it.Domain, it.PlistPath}, Root: root, IgnoreErr: true},
			},
		}, nil
	case items.LoginItem:
		if it.Override != items.OverrideDisabled {
			return nil, unsupported("not disabled")
		}
		return &Plan{
			Verb:            "enable",
			Summary:         "Add " + it.Name + " back to Open at Login.",
			Steps:           []Step{addLoginItemStep(it.Program, it.Note == "opens hidden")},
			ForgetLoginItem: it.Name,
		}, nil
	case items.Cron:
		if !strings.HasPrefix(it.Label, items.CronDisabledPrefix) {
			return nil, unsupported("not disabled")
		}
		return cronPlan("enable", "Uncomment this crontab line: "+strings.TrimPrefix(it.Label, items.CronDisabledPrefix), func(line string) (string, bool) {
			if strings.TrimSpace(line) == it.Label {
				return strings.TrimPrefix(line, items.CronDisabledPrefix), true
			}
			return line, false
		})
	}
	return nil, settingsOnly(it)
}

// Delete removes an item for good, after backing up its file.
func Delete(it *items.Item) (*Plan, error) {
	switch it.Source {
	case items.UserAgent, items.GlobalAgent, items.Daemon:
		root := it.Source != items.UserAgent
		p := &Plan{
			Verb:       "delete",
			Summary:    "Stop " + it.Label + " and delete " + it.PlistPath + ".",
			BackupFrom: it.PlistPath,
			BackupRoot: exists(it.PlistPath) && !readable(it.PlistPath),
			Steps: []Step{
				{Argv: []string{"launchctl", "bootout", it.Domain + "/" + it.Label}, Root: it.Domain == "system", IgnoreErr: true},
				{Argv: []string{"rm", "-f", "--", it.PlistPath}, Root: root},
			},
		}
		if it.Source == items.GlobalAgent {
			p.Warning = "This removes it for every user on this Mac."
		}
		if it.ParentApp == "" && strings.HasPrefix(it.Program, "/Applications/") && exists(it.Program) {
			p.Warning = strings.TrimSpace(p.Warning + " The app is still installed and may put this back the next time it runs.")
		}
		return p, nil
	case items.LoginItem:
		p := &Plan{
			Verb:    "delete",
			Summary: "Remove " + it.Name + " from Open at Login. The app itself is not touched.",
		}
		if it.Override == items.OverrideDisabled {
			p.Summary = "Forget the saved login item " + it.Name + "."
		} else {
			p.Steps = []Step{removeLoginItemStep(it.Name)}
		}
		p.ForgetLoginItem = it.Name
		return p, nil
	case items.Cron:
		return cronPlan("delete", "Delete this crontab line: "+it.Label, func(line string) (string, bool) {
			return "", strings.TrimSpace(line) == it.Label
		})
	case items.StartupItem:
		return &Plan{
			Verb:       "delete",
			Summary:    "Delete the obsolete startup item " + it.PlistPath + ".",
			BackupFrom: it.PlistPath,
			BackupRoot: true,
			Steps:      []Step{{Argv: []string{"rm", "-rf", "--", it.PlistPath}, Root: true}},
		}, nil
	case items.LoginHook:
		root := strings.HasPrefix(it.PlistPath, "/Library/")
		domain := "com.apple.loginwindow"
		if root {
			domain = strings.TrimSuffix(it.PlistPath, ".plist")
		}
		return &Plan{
			Verb:       "delete",
			Summary:    "Remove the " + it.Label + " setting (" + it.Program + "). The script itself is kept.",
			BackupFrom: it.PlistPath,
			Steps:      []Step{{Argv: []string{"defaults", "delete", domain, it.Label}, Root: root}},
		}, nil
	}
	return nil, settingsOnly(it)
}

func settingsOnly(it *items.Item) error {
	switch it.Source {
	case items.AppHelper:
		return unsupported("%s is registered by its app; switch it off in System Settings (press s) or uninstall %s", it.Name, filepath.Base(it.ParentApp))
	case items.BTMOnly:
		return unsupported("this is only a record in macOS's background-item database; System Settings (press s) controls it")
	case items.SystemExtension:
		return unsupported("system extensions are managed in System Settings → General → Login Items & Extensions (press s)")
	}
	return unsupported("not supported for %s items", it.Source)
}

// Result reports how a plan went.
type Result struct {
	BackupDir string
	Output    string
	Err       error
}

// Runner executes one step. Tests replace it.
type Runner func(s Step) ([]byte, error)

// ExecStep runs a step for real, through `sudo -n` for root steps.
func ExecStep(s Step) ([]byte, error) {
	argv := s.Argv
	if s.Root {
		argv = append([]string{"sudo", "-n"}, argv...)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if s.Stdin != nil {
		cmd.Stdin = bytes.NewReader(s.Stdin)
	}
	return cmd.CombinedOutput()
}

// Execute backs up, runs the steps in order and stops at the first failure.
func Execute(p *Plan, run Runner, dirs Dirs) Result {
	var r Result
	var out strings.Builder
	if p.BackupFrom != "" {
		dir, err := backup(p.BackupFrom, p.BackupRoot, run, dirs)
		if err != nil {
			r.Err = fmt.Errorf("backup failed, nothing was changed: %w", err)
			return r
		}
		r.BackupDir = dir
	}
	for _, s := range p.Steps {
		b, err := run(s)
		out.Write(b)
		if err != nil && !s.IgnoreErr {
			r.Output = out.String()
			r.Err = fmt.Errorf("%s: %w", s, errWithOutput(err, b))
			return r
		}
	}
	r.Output = out.String()
	if p.RememberLoginItem != nil {
		r.Err = SaveLoginItem(dirs, *p.RememberLoginItem)
	}
	if p.ForgetLoginItem != "" {
		r.Err = ForgetLoginItem(dirs, p.ForgetLoginItem)
	}
	return r
}

func errWithOutput(err error, out []byte) error {
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		return err
	}
	return errors.New(msg)
}

// Dirs is where the tool keeps its own files.
type Dirs struct {
	Base string // ~/Library/Application Support/starter-upper
	Now  func() time.Time
}

// DefaultDirs returns the standard location.
func DefaultDirs() Dirs {
	home, _ := os.UserHomeDir()
	return Dirs{Base: filepath.Join(home, "Library", "Application Support", "starter-upper"), Now: time.Now}
}

// BackupsDir is where deleted files are copied.
func (d Dirs) BackupsDir() string { return filepath.Join(d.Base, "backups") }

func backup(src string, root bool, run Runner, d Dirs) (string, error) {
	dir := filepath.Join(d.BackupsDir(), d.Now().Format("20060102-150405"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, filepath.Base(src))
	if _, err := os.Stat(dst); err == nil {
		dst += "." + strconv.FormatInt(d.Now().UnixNano(), 10)
	}
	note := "Restore with:\n  " + restoreHint(src, dst) + "\n"
	if err := os.WriteFile(filepath.Join(dir, filepath.Base(dst)+".restore.txt"), []byte("Original: "+src+"\n"+note), 0o600); err != nil {
		return "", err
	}
	steps := []Step{{Argv: []string{"cp", "-Rp", "--", src, dst}, Root: root}}
	if root {
		steps = append(steps, Step{Argv: []string{"chown", "-R", strconv.Itoa(os.Getuid()), dst}, Root: true})
	}
	for _, s := range steps {
		if b, err := run(s); err != nil {
			return "", errWithOutput(err, b)
		}
	}
	return dir, nil
}

func restoreHint(src, dst string) string {
	cmd := "cp -Rp " + quote(dst) + " " + quote(src)
	if !strings.HasPrefix(src, homeDir()) {
		cmd = "sudo " + cmd + " && sudo chown -R root:wheel " + quote(src)
	}
	return cmd
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h + "/"
}

func readable(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func quoteAll(a []string) []string {
	out := make([]string, len(a))
	for i, s := range a {
		out[i] = quote(s)
	}
	return out
}

func quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"\\$`!*?&;|<>()[]{}#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
