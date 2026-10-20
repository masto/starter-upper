package scan

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"howett.net/plist"

	"starter-upper/internal/items"
)

// launchdPlist is the subset of a launchd job definition the tool looks at.
type launchdPlist struct {
	Label                 string         `plist:"Label"`
	Program               string         `plist:"Program"`
	ProgramArguments      []string       `plist:"ProgramArguments"`
	RunAtLoad             bool           `plist:"RunAtLoad"`
	KeepAlive             any            `plist:"KeepAlive"`
	StartInterval         int            `plist:"StartInterval"`
	StartCalendarInterval any            `plist:"StartCalendarInterval"`
	WatchPaths            []string       `plist:"WatchPaths"`
	QueueDirectories      []string       `plist:"QueueDirectories"`
	MachServices          map[string]any `plist:"MachServices"`
	Sockets               map[string]any `plist:"Sockets"`
	LaunchEvents          map[string]any `plist:"LaunchEvents"`
	Disabled              bool           `plist:"Disabled"`
	BundleProgram         string         `plist:"BundleProgram"`
}

// ReadPlist builds an Item from a launchd plist. Read or parse failures are
// recorded on the item rather than returned, so broken plists still show up.
func ReadPlist(path string, src items.Source) items.Item {
	it := items.Item{
		Source:     src,
		PlistPath:  path,
		Label:      strings.TrimSuffix(filepath.Base(path), ".plist"),
		PlistOwner: -1,
	}
	if fi, err := os.Stat(path); err == nil {
		it.PlistMode = fi.Mode()
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			it.PlistOwner = int(st.Uid)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		it.ReadErr = err.Error()
		it.Name = it.Label
		return it
	}
	var raw map[string]any
	if _, err := plist.Unmarshal(data, &raw); err == nil && len(raw) == 0 {
		it.Empty = true
		it.Name = it.Label
		return it
	}
	var p launchdPlist
	if _, err := plist.Unmarshal(data, &p); err != nil {
		it.ReadErr = "unparsable plist: " + err.Error()
		it.Name = it.Label
		return it
	}
	applyPlist(&it, p)
	return it
}

func applyPlist(it *items.Item, p launchdPlist) {
	if p.Label != "" {
		it.Label = p.Label
	}
	it.Args = p.ProgramArguments
	switch {
	case p.Program != "":
		it.Program = p.Program
	case len(p.ProgramArguments) > 0:
		it.Program = p.ProgramArguments[0]
	case p.BundleProgram != "":
		it.Program = p.BundleProgram
	}
	it.RunAtLoad = p.RunAtLoad
	it.KeepAlive = keepAliveOn(p.KeepAlive)
	it.StartInterval = p.StartInterval
	it.Calendar = p.StartCalendarInterval != nil
	it.WatchPaths = len(p.WatchPaths) > 0 || len(p.QueueDirectories) > 0
	it.OnDemand = len(p.MachServices) > 0 || len(p.Sockets) > 0 || len(p.LaunchEvents) > 0
	it.PlistDisabled = p.Disabled
	it.Name = displayName(it.Label, it.Program)
}

// keepAliveOn treats any dictionary form of KeepAlive as "kept alive", since
// launchd restarts the job when its conditions hold.
func keepAliveOn(v any) bool {
	switch k := v.(type) {
	case bool:
		return k
	case map[string]any:
		return len(k) > 0
	}
	return false
}

// displayName prefers the .app name in the program path, falling back to the
// label.
func displayName(label, program string) string {
	if i := strings.Index(program, ".app/"); i >= 0 {
		return filepath.Base(program[:i])
	}
	if strings.HasSuffix(program, ".app") {
		return strings.TrimSuffix(filepath.Base(program), ".app")
	}
	return label
}

// ScanDir reads every plist in a launchd directory, in file name order.
// A missing directory yields nothing.
func ScanDir(dir string, src items.Source, domain string) []items.Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []items.Item
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plist") {
			continue
		}
		it := ReadPlist(filepath.Join(dir, e.Name()), src)
		it.Domain = domain
		out = append(out, it)
	}
	return out
}
