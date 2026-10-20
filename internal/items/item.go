// Package items defines the startup item model shared by the scanner,
// the checks, the actions and the TUI.
package items

import (
	"io/fs"
	"strconv"
	"strings"
)

// Source says where an item was found, which also decides what can be done
// to it.
type Source int

const (
	UserAgent       Source = iota // ~/Library/LaunchAgents
	GlobalAgent                   // /Library/LaunchAgents, loaded for every user
	Daemon                        // /Library/LaunchDaemons, run as root
	LoginItem                     // "Open at Login" list (System Events)
	AppHelper                     // helper bundled inside an app (SMAppService / LoginItems)
	BTMOnly                       // known only from the background task database
	SystemExtension               // network, camera, driver extensions
	Cron                          // crontab line
	StartupItem                   // /Library/StartupItems (obsolete)
	LoginHook                     // com.apple.loginwindow LoginHook/LogoutHook (obsolete)
)

func (s Source) String() string {
	switch s {
	case UserAgent:
		return "user agent"
	case GlobalAgent:
		return "global agent"
	case Daemon:
		return "daemon"
	case LoginItem:
		return "login item"
	case AppHelper:
		return "app helper"
	case BTMOnly:
		return "bg record"
	case SystemExtension:
		return "sys ext"
	case Cron:
		return "cron"
	case StartupItem:
		return "startup item"
	case LoginHook:
		return "login hook"
	}
	return "?"
}

// NeedsRoot reports whether changing an item from this source needs sudo.
func (s Source) NeedsRoot() bool {
	return s == GlobalAgent || s == Daemon || s == StartupItem || s == LoginHook
}

// CronDisabledPrefix marks crontab lines this tool commented out, so they can
// be told apart from the user's own comments and re-enabled.
const CronDisabledPrefix = "#starter-upper-disabled: "

// Severity of an Issue. Higher is worse.
type Severity int

const (
	Info Severity = iota
	Warning
	Problem
)

func (s Severity) String() string {
	switch s {
	case Problem:
		return "problem"
	case Warning:
		return "warning"
	}
	return "info"
}

// Issue is a finding from a heuristic check.
type Issue struct {
	Severity Severity
	Code     string // stable short id, e.g. "missing-program"
	Message  string
	Fix      string // suggested remedy, may be empty
}

// Override is launchd's persistent enable/disable state for a label
// (launchctl print-disabled).
type Override int

const (
	NoOverride Override = iota
	OverrideEnabled
	OverrideDisabled
)

// BTMState is one background-task-database record's view of an item, for one
// user.
type BTMState struct {
	UID     int // -2 is the system-wide record set
	Enabled bool
	Allowed bool // false means switched off in System Settings
}

// Item is one thing that can start automatically.
type Item struct {
	Source Source
	Label  string // launchd label, bundle id, or another stable key
	Name   string // human-friendly name

	PlistPath string // launchd plist (or bundle path for login items/helpers)
	Program   string // executable or app the item launches
	Args      []string
	ParentApp string // owning .app, for bundled helpers

	// launchd triggers
	RunAtLoad     bool
	KeepAlive     bool
	StartInterval int
	Calendar      bool
	WatchPaths    bool
	OnDemand      bool // MachServices, Sockets or LaunchEvents
	PlistDisabled bool // "Disabled" key inside the plist

	Domain   string // launchctl domain target: "gui/501" or "system"
	Override Override
	Loaded   bool
	PID      int

	BTM []BTMState

	PlistMode  fs.FileMode
	PlistOwner int
	ReadErr    string // plist could not be read or parsed
	Empty      bool   // plist is an empty dictionary

	Note   string // extra free-form detail (cron line, extension state...)
	Issues []Issue
}

// Key identifies an item across rescans.
func (it *Item) Key() string {
	return it.Source.String() + "|" + it.Label + "|" + it.PlistPath
}

// Worst returns the highest issue severity, or -1 with no issues.
func (it *Item) Worst() Severity {
	w := Severity(-1)
	for _, is := range it.Issues {
		if is.Severity > w {
			w = is.Severity
		}
	}
	return w
}

// HasIssue reports whether an issue with the code is attached.
func (it *Item) HasIssue(code string) bool {
	for _, is := range it.Issues {
		if is.Code == code {
			return true
		}
	}
	return false
}

// BTMFor returns the database record for a user, if any.
func (it *Item) BTMFor(uid int) (BTMState, bool) {
	for _, b := range it.BTM {
		if b.UID == uid {
			return b, true
		}
	}
	return BTMState{}, false
}

// Blocked reports whether the item is switched off in System Settings for
// the user (or system-wide).
func (it *Item) Blocked(uid int) bool {
	if b, ok := it.BTMFor(uid); ok {
		return !b.Allowed
	}
	if b, ok := it.BTMFor(-2); ok && it.Source == Daemon {
		return !b.Allowed
	}
	return false
}

// Disabled reports whether launchd will refuse to load the item.
func (it *Item) Disabled() bool {
	if it.Override == OverrideDisabled {
		return true
	}
	return it.Override == NoOverride && it.PlistDisabled
}

// AutoStarts reports whether launchd starts the item without being asked.
func (it *Item) AutoStarts() bool {
	return it.RunAtLoad || it.KeepAlive || it.StartInterval > 0 || it.Calendar || it.WatchPaths
}

// Triggers describes when launchd runs the item.
func (it *Item) Triggers() string {
	var t []string
	if it.RunAtLoad {
		t = append(t, "at load")
	}
	if it.KeepAlive {
		t = append(t, "kept alive")
	}
	if it.StartInterval > 0 {
		t = append(t, "every "+humanSeconds(it.StartInterval))
	}
	if it.Calendar {
		t = append(t, "on a schedule")
	}
	if it.WatchPaths {
		t = append(t, "when files change")
	}
	if it.OnDemand {
		t = append(t, "on demand")
	}
	if len(t) == 0 {
		return "never on its own"
	}
	return strings.Join(t, ", ")
}

func humanSeconds(s int) string {
	switch {
	case s%3600 == 0:
		return strconv.Itoa(s/3600) + "h"
	case s%60 == 0:
		return strconv.Itoa(s/60) + "m"
	}
	return strconv.Itoa(s) + "s"
}
