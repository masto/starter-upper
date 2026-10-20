package action

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"starter-upper/internal/items"
)

func argvs(p *Plan) []string {
	var out []string
	for _, s := range p.Steps {
		out = append(out, s.String())
	}
	return out
}

func TestDisableEnableLaunchd(t *testing.T) {
	daemon := &items.Item{Source: items.Daemon, Label: "com.x", Domain: "system", PlistPath: "/Library/LaunchDaemons/com.x.plist"}
	p, err := Disable(daemon)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sudo launchctl disable system/com.x", "sudo launchctl bootout system/com.x"}
	if strings.Join(argvs(p), "|") != strings.Join(want, "|") || !p.NeedsRoot() {
		t.Errorf("disable steps = %v", argvs(p))
	}
	if _, err := Enable(daemon); err == nil {
		t.Error("Enable of a non-disabled item should fail")
	}

	daemon.Override = items.OverrideDisabled
	if _, err := Disable(daemon); err == nil {
		t.Error("Disable of a disabled item should fail")
	}
	p, err = Enable(daemon)
	if err != nil {
		t.Fatal(err)
	}
	if argvs(p)[1] != "sudo launchctl bootstrap system /Library/LaunchDaemons/com.x.plist" {
		t.Errorf("enable steps = %v", argvs(p))
	}

	agent := &items.Item{Source: items.GlobalAgent, Label: "com.y", Domain: "gui/501"}
	p, _ = Disable(agent)
	if p.NeedsRoot() {
		t.Error("disabling a global agent for the current user shouldn't need root")
	}
	if p.Warning == "" {
		t.Error("global agent disable should warn that other users are unaffected")
	}
}

func TestUnsupportedSources(t *testing.T) {
	for _, src := range []items.Source{items.AppHelper, items.BTMOnly, items.SystemExtension} {
		it := &items.Item{Source: src, Name: "x", ParentApp: "/Applications/X.app"}
		for name, f := range map[string]func(*items.Item) (*Plan, error){"disable": Disable, "delete": Delete} {
			_, err := f(it)
			var u ErrUnsupported
			if !errors.As(err, &u) {
				t.Errorf("%s %s: err = %v, want ErrUnsupported", name, src, err)
			}
		}
	}
}

func TestDeleteBacksUpThenRemoves(t *testing.T) {
	dir := t.TempDir()
	plist := filepath.Join(dir, "com.z.plist")
	if err := os.WriteFile(plist, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	it := &items.Item{Source: items.UserAgent, Label: "com.z", Domain: "gui/501", PlistPath: plist}
	p, err := Delete(it)
	if err != nil {
		t.Fatal(err)
	}
	if p.NeedsRoot() {
		t.Error("deleting a user agent shouldn't need root")
	}

	dirs := Dirs{Base: filepath.Join(dir, "state"), Now: func() time.Time { return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC) }}
	var ran []string
	run := func(s Step) ([]byte, error) {
		ran = append(ran, s.String())
		if s.Argv[0] == "launchctl" {
			return []byte("Boot-out failed: 3: No such process"), errors.New("exit 3")
		}
		return ExecStep(s)
	}
	res := Execute(p, run, dirs)
	if res.Err != nil {
		t.Fatalf("Execute: %v (ran %v)", res.Err, ran)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Error("plist still exists")
	}
	backup := filepath.Join(res.BackupDir, "com.z.plist")
	if data, err := os.ReadFile(backup); err != nil || string(data) != "<plist/>" {
		t.Errorf("backup %s: %q, %v", backup, data, err)
	}
	if _, err := os.Stat(backup + ".restore.txt"); err != nil {
		t.Errorf("restore note missing: %v", err)
	}
}

func TestExecuteStopsOnFailure(t *testing.T) {
	p := &Plan{Verb: "disable", Steps: []Step{{Argv: []string{"first"}}, {Argv: []string{"second"}}}}
	var ran []string
	res := Execute(p, func(s Step) ([]byte, error) {
		ran = append(ran, s.Argv[0])
		return []byte("boom"), errors.New("exit 1")
	}, Dirs{Base: t.TempDir(), Now: time.Now})
	if res.Err == nil || len(ran) != 1 || !strings.Contains(res.Err.Error(), "boom") {
		t.Errorf("err = %v, ran = %v", res.Err, ran)
	}
}

func TestLoginItemRoundTrip(t *testing.T) {
	dirs := Dirs{Base: t.TempDir(), Now: time.Now}
	it := &items.Item{Source: items.LoginItem, Name: "Maccy", Program: "/Applications/Maccy.app"}
	p, err := Disable(it)
	if err != nil {
		t.Fatal(err)
	}
	if res := Execute(p, func(Step) ([]byte, error) { return nil, nil }, dirs); res.Err != nil {
		t.Fatal(res.Err)
	}
	saved, _ := SavedLoginItems(dirs)
	if len(saved) != 1 || saved[0].Path != "/Applications/Maccy.app" {
		t.Fatalf("saved = %+v", saved)
	}

	restored := SavedAsItems(saved)[0]
	if !restored.Disabled() {
		t.Error("saved login item should show as disabled")
	}
	p, err = Enable(&restored)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(argvs(p)[0], "/Applications/Maccy.app") {
		t.Errorf("enable step = %v", argvs(p))
	}
	if res := Execute(p, func(Step) ([]byte, error) { return nil, nil }, dirs); res.Err != nil {
		t.Fatal(res.Err)
	}
	if saved, _ := SavedLoginItems(dirs); len(saved) != 0 {
		t.Errorf("still saved after enable: %+v", saved)
	}
}
