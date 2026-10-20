package scan

import (
	"bufio"
	"bytes"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Runner runs an external command and returns its stdout. Tests swap it out.
type Runner func(name string, args ...string) ([]byte, error)

// ExecRunner runs commands for real.
func ExecRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

var disabledLine = regexp.MustCompile(`^\s*"([^"]+)"\s*=>\s*(enabled|disabled|true|false)\s*$`)

// ParseDisabled parses `launchctl print-disabled <domain>` into label →
// disabled. Older macOS versions print true/false instead of
// disabled/enabled.
func ParseDisabled(out []byte) map[string]bool {
	m := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if g := disabledLine.FindStringSubmatch(sc.Text()); g != nil {
			m[g[1]] = g[2] == "disabled" || g[2] == "true"
		}
	}
	return m
}

// ParseServices parses the "services = { ... }" block of `launchctl print
// <domain>` into label → PID (0 when loaded but not running).
func ParseServices(out []byte) map[string]int {
	m := map[string]int{}
	in := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if !in {
			// Only the domain's own top-level services block, one tab deep.
			if line == "\tservices = {" {
				in = true
			}
			continue
		}
		if trimmed == "}" {
			break
		}
		f := strings.Fields(trimmed)
		if len(f) < 3 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		m[f[len(f)-1]] = pid
	}
	return m
}

// DomainState is what launchd knows about one domain.
type DomainState struct {
	Disabled map[string]bool
	Services map[string]int
}

// ReadDomain queries launchctl for a domain such as "gui/501" or "system".
// Both queries work without root.
func ReadDomain(run Runner, domain string) DomainState {
	ds := DomainState{Disabled: map[string]bool{}, Services: map[string]int{}}
	if out, err := run("launchctl", "print-disabled", domain); err == nil {
		ds.Disabled = ParseDisabled(out)
	}
	if out, err := run("launchctl", "print", domain); err == nil {
		ds.Services = ParseServices(out)
	}
	return ds
}
