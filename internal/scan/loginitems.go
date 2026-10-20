package scan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"howett.net/plist"

	"starter-upper/internal/items"
)

const loginItemsJXA = `JSON.stringify(Application("System Events").loginItems().map(i => ({name: i.name(), path: i.path(), hidden: i.hidden()})))`

// ParseLoginItems decodes the JSON printed by loginItemsJXA.
func ParseLoginItems(out []byte) ([]items.Item, error) {
	var raw []struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		Hidden bool   `json:"hidden"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, err
	}
	var res []items.Item
	for _, r := range raw {
		it := items.Item{
			Source:    items.LoginItem,
			Label:     r.Name,
			Name:      r.Name,
			PlistPath: r.Path,
			Program:   r.Path,
			RunAtLoad: true,
		}
		if r.Hidden {
			it.Note = "opens hidden"
		}
		res = append(res, it)
	}
	return res, nil
}

// LoginItems lists the "Open at Login" items via System Events. The first
// call may trigger a macOS automation permission prompt for the terminal.
func LoginItems(run Runner) ([]items.Item, error) {
	out, err := run("osascript", "-l", "JavaScript", "-e", loginItemsJXA)
	if err != nil {
		return nil, err
	}
	return ParseLoginItems(out)
}

// AppHelpers finds helpers bundled inside apps in the given directories:
// Contents/Library/LoginItems/*.app and launchd plists under
// Contents/Library/LaunchAgents and LaunchDaemons. Apps register these with
// SMAppService, so they never appear in the /Library folders.
func AppHelpers(appDirs []string, uid string) []items.Item {
	var out []items.Item
	for _, dir := range appDirs {
		for _, app := range findApps(dir, 2) {
			out = append(out, helpersIn(app, uid)...)
		}
	}
	return out
}

// findApps returns .app bundles in dir, descending into plain folders (like
// /Applications/Utilities) up to depth levels.
func findApps(dir string, depth int) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var apps []string
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if !e.IsDir() && e.Type()&os.ModeSymlink == 0 {
			continue
		}
		if strings.HasSuffix(e.Name(), ".app") {
			apps = append(apps, p)
		} else if depth > 1 && e.IsDir() {
			apps = append(apps, findApps(p, depth-1)...)
		}
	}
	return apps
}

func helpersIn(app, uid string) []items.Item {
	var out []items.Item
	lib := filepath.Join(app, "Contents", "Library")
	appName := strings.TrimSuffix(filepath.Base(app), ".app")

	if entries, err := os.ReadDir(filepath.Join(lib, "LoginItems")); err == nil {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".app") {
				continue
			}
			p := filepath.Join(lib, "LoginItems", e.Name())
			id := bundleID(p)
			if id == "" {
				id = strings.TrimSuffix(e.Name(), ".app")
			}
			out = append(out, items.Item{
				Source:    items.AppHelper,
				Label:     id,
				Name:      appName + ": " + strings.TrimSuffix(e.Name(), ".app"),
				PlistPath: p,
				Program:   p,
				ParentApp: app,
				Domain:    "gui/" + uid,
				Note:      "login item helper",
			})
		}
	}
	for _, sub := range []struct{ dir, domain, note string }{
		{"LaunchAgents", "gui/" + uid, "bundled launch agent"},
		{"LaunchDaemons", "system", "bundled launch daemon"},
	} {
		for _, it := range ScanDir(filepath.Join(lib, sub.dir), items.AppHelper, sub.domain) {
			it.ParentApp = app
			it.Note = sub.note
			// BundleProgram is relative to the app bundle.
			if it.Program != "" && !strings.HasPrefix(it.Program, "/") {
				it.Program = filepath.Join(app, it.Program)
			}
			it.Name = appName + ": " + it.Label
			out = append(out, it)
		}
	}
	return out
}

func bundleID(app string) string {
	data, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	var info struct {
		ID string `plist:"CFBundleIdentifier"`
	}
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return ""
	}
	return info.ID
}
