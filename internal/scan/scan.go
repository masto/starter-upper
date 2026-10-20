// Package scan collects startup items from every place macOS starts things.
package scan

import (
	"os"
	"path/filepath"
	"strconv"

	"github.com/masto/starter-upper/internal/items"
)

// Config says where to look. Fields are overridable for tests.
type Config struct {
	UID     int
	Home    string
	Root    string   // prefix for system paths, "" in normal use
	AppDirs []string // where to look for bundled helpers
	Run     Runner
	BTM     *BTM // optional; nil when the database hasn't been read

	SkipLoginItems bool // avoid the System Events call (tests, scripts)
}

// DefaultConfig returns a Config for the current user.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		UID:     os.Getuid(),
		Home:    home,
		AppDirs: []string{"/Applications", filepath.Join(home, "Applications")},
		Run:     ExecRunner,
	}
}

// Result is a full scan.
type Result struct {
	Items    []items.Item
	Warnings []string // collectors that failed, shown in the UI
}

// All runs every collector and merges launchd and background-database state
// into the items.
func All(c Config) Result {
	var r Result
	gui := "gui/" + strconv.Itoa(c.UID)
	sys := func(p string) string { return filepath.Join(c.Root, p) }

	r.Items = append(r.Items, ScanDir(filepath.Join(c.Home, "Library/LaunchAgents"), items.UserAgent, gui)...)
	r.Items = append(r.Items, ScanDir(sys("/Library/LaunchAgents"), items.GlobalAgent, gui)...)
	r.Items = append(r.Items, ScanDir(sys("/Library/LaunchDaemons"), items.Daemon, "system")...)
	r.Items = append(r.Items, AppHelpers(c.AppDirs, strconv.Itoa(c.UID))...)

	if !c.SkipLoginItems {
		li, err := LoginItems(c.Run)
		if err != nil {
			r.Warnings = append(r.Warnings, "login items: System Events query failed (automation permission?): "+err.Error())
		}
		r.Items = append(r.Items, li...)
	}
	if out, err := c.Run("systemextensionsctl", "list"); err == nil {
		r.Items = append(r.Items, ParseSystemExtensions(out)...)
	}
	if out, err := c.Run("crontab", "-l"); err == nil {
		r.Items = append(r.Items, ParseCrontab(out)...)
	}
	r.Items = append(r.Items, StartupItems(sys("/Library/StartupItems"))...)
	r.Items = append(r.Items, LoginHooks([]string{
		sys("/Library/Preferences/com.apple.loginwindow.plist"),
		filepath.Join(c.Home, "Library/Preferences/com.apple.loginwindow.plist"),
	})...)

	domains := map[string]DomainState{
		gui:      ReadDomain(c.Run, gui),
		"system": ReadDomain(c.Run, "system"),
	}
	for i := range r.Items {
		applyDomain(&r.Items[i], domains)
	}
	if c.BTM != nil {
		r.Items = MergeBTM(r.Items, c.BTM)
	}
	return r
}

func applyDomain(it *items.Item, domains map[string]DomainState) {
	ds, ok := domains[it.Domain]
	if !ok {
		return
	}
	if d, ok := ds.Disabled[it.Label]; ok {
		if d {
			it.Override = items.OverrideDisabled
		} else {
			it.Override = items.OverrideEnabled
		}
	}
	if pid, ok := ds.Services[it.Label]; ok {
		it.Loaded = true
		it.PID = pid
	}
}

// MergeBTM attaches background-database records to the matching items and
// adds items for records nothing else found: other users' agents, and
// stale entries whose files are gone.
func MergeBTM(list []items.Item, b *BTM) []items.Item {
	byPath := map[string]int{}
	byLabel := map[string]int{}
	for i, it := range list {
		if it.PlistPath != "" {
			byPath[it.PlistPath] = i
		}
		if it.Source == items.AppHelper {
			byLabel[it.Label] = i
		}
	}
	extra := map[string]int{} // identifier+path → index in list

	for _, rec := range b.Records {
		isLoginApp := rec.Type == "app" && rec.Enabled
		if !rec.IsStartup() && !isLoginApp {
			continue
		}
		state := items.BTMState{UID: rec.UID, Enabled: rec.Enabled, Allowed: rec.Allowed}
		path := b.ResolvedPath(rec)

		idx, found := byPath[path]
		if !found && path == "" {
			idx, found = byLabel[rec.Label()]
		}
		if !found && rec.BundleID != "" {
			idx, found = byLabel[rec.BundleID]
		}
		if !found {
			key := rec.Identifier + "|" + path
			idx, found = extra[key]
			if !found {
				list = append(list, btmOnlyItem(rec, b, path))
				idx = len(list) - 1
				extra[key] = idx
				found = true
			}
		}
		list[idx].BTM = append(list[idx].BTM, state)
	}
	return list
}

func btmOnlyItem(rec BTMRecord, b *BTM, path string) items.Item {
	it := items.Item{
		Source:    items.BTMOnly,
		Label:     rec.Label(),
		Name:      rec.Name,
		PlistPath: path,
		Program:   rec.ExecutablePath,
		Note:      rec.Type,
	}
	if it.Program == "" {
		it.Program = path
	}
	if it.Name == "" {
		it.Name = it.Label
	}
	if parent, ok := b.Parent(rec); ok && parent.Type == "app" {
		it.ParentApp = parent.Path()
	}
	if rec.Type == "app" {
		it.Note = "open at login (another user)"
		it.ParentApp = ""
	}
	return it
}
