// Package check applies heuristics to scanned items and attaches issues.
package check

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/masto/starter-upper/internal/items"
)

// Env carries what the checks need beyond the items themselves.
type Env struct {
	UID int
}

// Run replaces the issues on every item.
func Run(list []items.Item, env Env) {
	labels := map[string][]int{}
	for i := range list {
		it := &list[i]
		it.Issues = nil
		checkItem(it, env)
		if isLaunchd(it.Source) && it.Label != "" && !it.Empty {
			labels[it.Label] = append(labels[it.Label], i)
		}
	}
	for label, idx := range labels {
		if len(idx) < 2 {
			continue
		}
		var where []string
		for _, i := range idx {
			where = append(where, list[i].PlistPath)
		}
		for _, i := range idx {
			add(&list[i], items.Warning, "duplicate-label",
				"label "+label+" is defined "+strconv.Itoa(len(idx))+" times: "+strings.Join(where, ", "),
				"Keep one copy; launchd loads whichever it finds first in each domain.")
		}
	}
}

func isLaunchd(s items.Source) bool {
	return s == items.UserAgent || s == items.GlobalAgent || s == items.Daemon
}

func checkItem(it *items.Item, env Env) {
	if it.ReadErr != "" {
		add(it, items.Problem, "unreadable", "plist can't be used: "+it.ReadErr,
			"launchd ignores a broken plist; delete it or reinstall the app that owns it.")
		return
	}

	switch it.Source {
	case items.UserAgent, items.GlobalAgent, items.Daemon:
		checkLaunchd(it, env)
	case items.LoginItem:
		if it.Program != "" && !exists(it.Program) {
			add(it, items.Problem, "missing-program", "login item points to "+it.Program+", which no longer exists",
				"Remove the login item.")
		}
	case items.AppHelper:
		checkProgramPaths(it)
	case items.BTMOnly:
		checkBTMOnly(it)
	case items.Cron:
		if strings.HasPrefix(it.Program, "/") && !exists(it.Program) {
			add(it, items.Problem, "missing-program", "cron job runs "+it.Program+", which no longer exists",
				"Edit your crontab with `crontab -e` and remove the line.")
		}
	case items.StartupItem:
		add(it, items.Warning, "obsolete", "StartupItems haven't run since OS X 10.10; this is a leftover",
			"Delete it.")
	case items.LoginHook:
		add(it, items.Warning, "obsolete", "login and logout hooks are deprecated and unusual on modern macOS",
			"If you didn't set this up, remove it with `sudo defaults delete com.apple.loginwindow "+it.Label+"`.")
		if !exists(it.Program) {
			add(it, items.Problem, "missing-program", "hook script "+it.Program+" no longer exists", "")
		}
	}

	if b, ok := it.BTMFor(env.UID); ok && !b.Allowed {
		add(it, items.Info, "blocked", "switched off in System Settings, so macOS won't start it",
			"If you don't use it any more, delete it rather than leaving it switched off.")
	}
	checkSuspiciousLocation(it)
	checkSecrets(it)
}

func checkLaunchd(it *items.Item, env Env) {
	if it.Empty {
		add(it, items.Info, "empty", "empty plist: launchd ignores it",
			"Sometimes created on purpose to stop an app reinstalling its agent (common for Google's Keystone updater). Delete it if that isn't why it's here.")
		return
	}
	base := strings.TrimSuffix(filepath.Base(it.PlistPath), ".plist")
	if it.Label != "" && !strings.EqualFold(it.Label, base) && it.Label != base+".plist" {
		add(it, items.Info, "label-mismatch", "label "+it.Label+" doesn't match the file name "+base+".plist",
			"Harmless to launchd, but unusual and makes the item harder to find.")
	}
	if it.Program == "" {
		add(it, items.Problem, "no-program", "plist has no Program or ProgramArguments",
			"launchd can't run it; delete it.")
	}
	checkProgramPaths(it)

	if !it.AutoStarts() && !it.OnDemand && !it.Disabled() {
		add(it, items.Info, "inert", "nothing in the plist makes launchd start it (no RunAtLoad, KeepAlive or schedule)",
			"It only runs if an app asks for it. If you don't use the app, delete it.")
	}
	if it.Disabled() {
		add(it, items.Info, "disabled", "disabled in launchd, so the plist is only a leftover", "Delete it if you don't need it back.")
	}
	if it.KeepAlive && it.Loaded && it.PID == 0 && !it.Disabled() && !it.Blocked(env.UID) {
		add(it, items.Warning, "not-running", "set to keep running, but launchd shows no process",
			"It may be crashing at start. Check Console.app or `log show --predicate 'process == \""+filepath.Base(it.Program)+"\"' --last 1h`.")
	}

	// Ownership: launchd refuses group/world-writable plists in system
	// locations, and a writable plist or program there is a privilege
	// escalation path.
	if it.Source != items.UserAgent {
		if it.PlistOwner > 0 {
			add(it, items.Problem, "plist-owner", "plist is owned by "+userName(it.PlistOwner)+" instead of root",
				"A non-root owner can change what this runs. `sudo chown root:wheel` it, or delete it.")
		}
		if it.PlistMode&0o022 != 0 {
			add(it, items.Problem, "plist-writable", "plist is writable by group or others ("+it.PlistMode.String()+")",
				"launchd ignores it and anyone can edit it; fix with `sudo chmod 644`.")
		}
	}
	// Only daemons: apps dragged into /Applications are owned by whoever
	// installed them, so global agents would almost all match.
	if it.Source == items.Daemon {
		if why := writableByNonRoot(it.Program); why != "" && exists(it.Program) {
			add(it, items.Warning, "program-writable", "runs as root but the program "+why,
				"Whoever can change the program gets root. Usual for Homebrew on a single-user Mac; otherwise move it somewhere root-owned.")
		}
	}
}

// checkProgramPaths flags a missing executable, and app paths passed as
// arguments (like `/usr/bin/open -W /Applications/Foo.app`).
func checkProgramPaths(it *items.Item) {
	if it.Program != "" && strings.HasPrefix(it.Program, "/") && !exists(it.Program) {
		add(it, items.Problem, "missing-program", "program "+it.Program+" no longer exists",
			"The app was probably uninstalled. Delete this leftover.")
		return
	}
	for _, a := range it.Args[min(1, len(it.Args)):] {
		if looksLikeAppPath(a) && !exists(a) {
			add(it, items.Problem, "missing-target", "launches "+a+", which no longer exists",
				"The app was probably uninstalled or moved. Delete this leftover.")
		}
	}
}

func checkBTMOnly(it *items.Item) {
	p := it.Program
	if p == "" {
		p = it.PlistPath
	}
	if strings.HasPrefix(p, "/") && !exists(p) {
		add(it, items.Warning, "stale-record", "macOS still has a background-item record for "+p+", which no longer exists",
			"Harmless. Only `sfltool resetbtm` clears it, and that resets every approval in System Settings.")
	}
	if len(it.BTM) > 0 && allOtherUsers(it) {
		add(it, items.Info, "other-user", "belongs to another user account on this Mac", "")
	}
}

func allOtherUsers(it *items.Item) bool {
	uid := os.Getuid()
	for _, b := range it.BTM {
		if b.UID == uid || b.UID == -2 {
			return false
		}
	}
	return true
}

var (
	secretFlag  = regexp.MustCompile(`(?i)^--?(token|password|passwd|pass|secret|api[-_]?key|auth|credentials?)(=.+)?$`)
	secretValue = regexp.MustCompile(`^[A-Za-z0-9+/_\-=.]{40,}$`)
)

// SecretArgs returns the indexes of arguments that look like secrets: the
// value after a --token style flag, an inline --token=value, or a long
// opaque string such as a JWT or base64 blob.
func SecretArgs(args []string) map[int]bool {
	m := map[int]bool{}
	for i, a := range args {
		if g := secretFlag.FindStringSubmatch(a); g != nil {
			if g[2] != "" {
				m[i] = true
			} else if i+1 < len(args) {
				m[i+1] = true
			}
			continue
		}
		if i > 0 && secretValue.MatchString(a) && !strings.Contains(a, "/") {
			m[i] = true
		}
	}
	return m
}

func checkSecrets(it *items.Item) {
	if len(SecretArgs(it.Args)) == 0 {
		return
	}
	readable := it.PlistMode&0o004 != 0
	if readable {
		add(it, items.Problem, "secret-in-args", "a secret is passed on the command line and the plist is readable by every user",
			"Any local user can read it, and it shows in `ps` while running. Move it to a root-only file (chmod 600), then rotate it.")
		return
	}
	add(it, items.Warning, "secret-in-args", "a secret is passed on the command line, so it shows in `ps` while running",
		"Move it to a file the program reads, if it supports that.")
}

// checkSuspiciousLocation flags places persistence malware likes to run from.
func checkSuspiciousLocation(it *items.Item) {
	p := it.Program
	if p == "" {
		return
	}
	for _, prefix := range []string{"/tmp/", "/private/tmp/", "/var/tmp/", "/private/var/tmp/", "/Users/Shared/"} {
		if strings.HasPrefix(p, prefix) {
			add(it, items.Problem, "suspicious-path", "runs from "+prefix+", a world-writable location",
				"Legitimate software doesn't start from here. Investigate before deleting.")
			return
		}
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			add(it, items.Warning, "hidden-path", "runs from a hidden folder ("+part+")",
				"Unusual for real apps; check what it is.")
			break
		}
	}
	if strings.HasPrefix(it.Label, "com.apple.") && it.Source != items.SystemExtension && it.ParentApp == "" &&
		!strings.HasPrefix(p, "/System/") && !strings.HasPrefix(p, "/usr/") && !strings.HasPrefix(p, "/Library/Apple/") {
		add(it, items.Problem, "fake-apple", "uses an Apple-style label but doesn't run Apple software",
			"Apple's own jobs live in /System. Investigate this one.")
	}
}

// writableByNonRoot explains why a non-root user could modify path, or
// returns "".
func writableByNonRoot(path string) string {
	if path == "" || !strings.HasPrefix(path, "/") {
		return ""
	}
	if why := ownerOrModeWritable(path); why != "" {
		return why
	}
	if why := ownerOrModeWritable(filepath.Dir(path)); why != "" {
		return "is in a folder that " + strings.TrimPrefix(why, "is ")
	}
	return ""
}

func ownerOrModeWritable(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != 0 {
		return "is owned by " + userName(int(st.Uid))
	}
	if fi.Mode().Perm()&0o002 != 0 {
		return "is writable by everyone"
	}
	return ""
}

func looksLikeAppPath(s string) bool {
	return strings.HasPrefix(s, "/") && (strings.HasSuffix(s, ".app") || strings.Contains(s, ".app/"))
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func add(it *items.Item, sev items.Severity, code, msg, fix string) {
	it.Issues = append(it.Issues, items.Issue{Severity: sev, Code: code, Message: msg, Fix: fix})
}

func userName(uid int) string {
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		return u.Username
	}
	return "uid " + strconv.Itoa(uid)
}
