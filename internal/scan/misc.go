package scan

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"howett.net/plist"

	"starter-upper/internal/items"
)

// ParseSystemExtensions parses `systemextensionsctl list`.
func ParseSystemExtensions(out []byte) []items.Item {
	var res []items.Item
	category := ""
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "--- ") {
			category = strings.TrimPrefix(strings.Fields(line)[1], "com.apple.system_extension.")
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 6 || strings.HasPrefix(line, "enabled") {
			continue
		}
		bundle := f[3]
		if i := strings.Index(bundle, " ("); i > 0 {
			bundle = bundle[:i]
		}
		res = append(res, items.Item{
			Source:    items.SystemExtension,
			Label:     bundle,
			Name:      f[4],
			Note:      strings.ReplaceAll(category, "_", " ") + " extension " + f[5],
			Loaded:    strings.TrimSpace(f[1]) == "*",
			RunAtLoad: true,
		})
	}
	return res
}

// ParseCrontab turns schedule lines of `crontab -l` into items.
func ParseCrontab(out []byte) []items.Item {
	var res []items.Item
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		raw := strings.TrimSpace(sc.Text())
		line, disabled := strings.CutPrefix(raw, items.CronDisabledPrefix)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		c := line[0]
		if !(c == '*' || c == '@' || (c >= '0' && c <= '9')) {
			continue // environment assignment
		}
		f := strings.Fields(line)
		cmdStart := 5
		if c == '@' {
			cmdStart = 1
		}
		if len(f) <= cmdStart {
			continue
		}
		it := items.Item{
			Source:   items.Cron,
			Label:    raw,
			Name:     filepath.Base(f[cmdStart]),
			Program:  f[cmdStart],
			Args:     f[cmdStart:],
			Calendar: true,
			Note:     line,
		}
		if disabled {
			it.Override = items.OverrideDisabled
		}
		res = append(res, it)
	}
	return res
}

// StartupItems lists the obsolete /Library/StartupItems bundles.
func StartupItems(dir string) []items.Item {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var res []items.Item
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		res = append(res, items.Item{
			Source:    items.StartupItem,
			Label:     e.Name(),
			Name:      e.Name(),
			PlistPath: p,
			Program:   filepath.Join(p, e.Name()),
		})
	}
	return res
}

// LoginHooks reads LoginHook/LogoutHook from loginwindow preference files.
func LoginHooks(prefFiles []string) []items.Item {
	var res []items.Item
	for _, f := range prefFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var prefs map[string]any
		if _, err := plist.Unmarshal(data, &prefs); err != nil {
			continue
		}
		for _, key := range []string{"LoginHook", "LogoutHook"} {
			v, ok := prefs[key].(string)
			if !ok || v == "" {
				continue
			}
			res = append(res, items.Item{
				Source:    items.LoginHook,
				Label:     key,
				Name:      key + ": " + filepath.Base(v),
				PlistPath: f,
				Program:   v,
				RunAtLoad: true,
			})
		}
	}
	return res
}
