package scan

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"starter-upper/internal/items"
)

const btmFixture = `========================
 Records for UID -2 : FFFFEEEE-DDDD-CCCC-BBBB-AAAAFFFFFFFE
========================

 ServiceManagement migrated: true
 LaunchServices registered: false

 Items:

 #1:
                 UUID: 00000000-0000-0000-0000-000000000001
                 Name: cloudflared
       Developer Name: (null)
                 Type: legacy daemon (0x10010)
                Flags: [ legacy ] (0x1)
          Disposition: [enabled, allowed, visible, notified] (0xb)
           Identifier: 16.com.cloudflare.cloudflared
                  URL: file:///Library/LaunchDaemons/com.cloudflare.cloudflared.plist
      Executable Path: /opt/homebrew/bin/cloudflared
           Generation: 1
    Parent Identifier: Unknown Developer

========================
 Records for UID 501 : 00000000-0000-0000-0000-000000000501
========================

 ServiceManagement migrated: true
 LaunchServices registered: true

 Items:

 #1:
                 UUID: 1
                 Name: 1Password
       Developer Name: AgileBits Inc.
                 Type: app (0x2)
                Flags: [  ] (0)
          Disposition: [disabled, allowed, visible, not notified] (0x2)
           Identifier: 2.com.1password.1password
                  URL: file:///Applications/1Password.app/
           Generation: 1
    Bundle Identifier: com.1password.1password
  Embedded Item Identifiers:
    #1: 4.com.1password.1password-launcher

 #2:
                 UUID: 2
                 Name: 1Password Launcher
       Developer Name: AgileBits Inc.
                 Type: login item (0x4)
                Flags: [  ] (0)
          Disposition: [enabled, disallowed, visible, notified] (0x9)
           Identifier: 4.com.1password.1password-launcher
                  URL: Contents/Library/LoginItems/1Password%20Launcher.app
           Generation: 1
    Bundle Identifier: com.1password.1password-launcher
    Parent Identifier: 2.com.1password.1password

 #3:
                 UUID: 3
                 Name: Maccy
       Developer Name: (null)
                 Type: app (0x2)
                Flags: [  ] (0)
          Disposition: [enabled, allowed, visible, notified] (0xb)
           Identifier: 2.org.p0deje.Maccy
                  URL: file:///Applications/Maccy.app/
           Generation: 1

 #4:
                 UUID: 4
                 Name: Gone Helper
       Developer Name: (null)
                 Type: legacy agent (0x10008)
                Flags: [ legacy ] (0x1)
          Disposition: [enabled, allowed, visible, notified] (0xb)
           Identifier: 8.com.example.gone
                  URL: file:///Library/LaunchAgents/com.example.gone.plist
      Executable Path: /Applications/Gone.app/Contents/MacOS/Gone
           Generation: 1

 #5:
                 UUID: 5
                 Name: Some Thumbnailer
                 Type: quicklook (0x800)
          Disposition: [enabled, allowed, visible, notified] (0xb)
           Identifier: 2048.com.example.ql
                  URL: Contents/PlugIns/Thumb.appex
           Generation: 1
`

func TestParseBTM(t *testing.T) {
	b := ParseBTM([]byte(btmFixture))
	if len(b.Records) != 6 {
		t.Fatalf("got %d records, want 6", len(b.Records))
	}
	cf := b.Records[0]
	if cf.UID != -2 || cf.Type != "legacy daemon" || !cf.Enabled || !cf.Allowed {
		t.Errorf("cloudflared record = %+v", cf)
	}
	if got := cf.Path(); got != "/Library/LaunchDaemons/com.cloudflare.cloudflared.plist" {
		t.Errorf("Path() = %q", got)
	}
	if got := cf.Label(); got != "com.cloudflare.cloudflared" {
		t.Errorf("Label() = %q", got)
	}
	if cf.Developer != "" {
		t.Errorf("(null) developer should be empty, got %q", cf.Developer)
	}

	launcher := b.Records[2]
	if launcher.UID != 501 || launcher.Allowed || !launcher.Enabled {
		t.Errorf("launcher record = %+v", launcher)
	}
	if got := b.ResolvedPath(launcher); got != "/Applications/1Password.app/Contents/Library/LoginItems/1Password Launcher.app" {
		t.Errorf("ResolvedPath = %q", got)
	}
	if b.Records[5].IsStartup() {
		t.Error("quicklook plugin counted as startup")
	}
}

func TestMergeBTM(t *testing.T) {
	list := []items.Item{
		{Source: items.Daemon, Label: "com.cloudflare.cloudflared", PlistPath: "/Library/LaunchDaemons/com.cloudflare.cloudflared.plist"},
		{Source: items.AppHelper, Label: "com.1password.1password-launcher", PlistPath: "/Applications/1Password.app/Contents/Library/LoginItems/1Password Launcher.app"},
		{Source: items.LoginItem, Label: "Maccy", PlistPath: "/Applications/Maccy.app"},
	}
	got := MergeBTM(list, ParseBTM([]byte(btmFixture)))

	if len(got) != 4 {
		t.Fatalf("got %d items, want 4 (3 matched + 1 BTM-only)", len(got))
	}
	if b, ok := got[0].BTMFor(-2); !ok || !b.Allowed {
		t.Errorf("cloudflared BTM = %+v", got[0].BTM)
	}
	if !got[1].Blocked(501) {
		t.Error("1Password launcher should be blocked for uid 501")
	}
	if _, ok := got[2].BTMFor(501); !ok {
		t.Error("Maccy login item didn't pick up its app record")
	}
	extra := got[3]
	if extra.Source != items.BTMOnly || extra.Program != "/Applications/Gone.app/Contents/MacOS/Gone" {
		t.Errorf("BTM-only item = %+v", extra)
	}
}

func TestParseDisabled(t *testing.T) {
	out := []byte("disabled services = {\n\t\"com.a\" => disabled\n\t\"com.b\" => enabled\n\t\"com.c\" => true\n}\n")
	got := ParseDisabled(out)
	want := map[string]bool{"com.a": true, "com.b": false, "com.c": true}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
}

func TestParseServices(t *testing.T) {
	out := []byte("system = {\n\tsubmitted jobs = {\n\t\t0 - nope\n\t}\n\tservices = {\n\t\t     835      - \tcom.bjango.istatmenus.daemon\n\t\t       0      - \tcom.vix.cron\n\t}\n\tother = {\n\t\t1 - also.nope\n\t}\n}\n")
	got := ParseServices(out)
	if len(got) != 2 || got["com.bjango.istatmenus.daemon"] != 835 || got["com.vix.cron"] != 0 {
		t.Errorf("ParseServices = %v", got)
	}
}

func TestParseCrontab(t *testing.T) {
	out := []byte("MAILTO=me\n# comment\n*/5 * * * * /usr/local/bin/thing --flag\n@reboot /bin/echo hi\n" +
		items.CronDisabledPrefix + "0 3 * * * /bin/old\n")
	got := ParseCrontab(out)
	if len(got) != 3 {
		t.Fatalf("got %d cron items, want 3: %+v", len(got), got)
	}
	if got[0].Program != "/usr/local/bin/thing" || got[1].Program != "/bin/echo" {
		t.Errorf("programs = %q, %q", got[0].Program, got[1].Program)
	}
	if !got[2].Disabled() || got[2].Program != "/bin/old" {
		t.Errorf("disabled cron line = %+v", got[2])
	}
}

func TestParseSystemExtensions(t *testing.T) {
	out := []byte("2 extension(s)\n--- com.apple.system_extension.network_extension (Go to ...)\nenabled\tactive\tteamID\tbundleID (version)\tname\t[state]\n*\t*\tMLZF7K7B5R\tat.obdev.littlesnitch.networkextension (6.2.1/7149)\tLittle Snitch Network Extension\t[activated enabled]\n")
	got := ParseSystemExtensions(out)
	if len(got) != 1 || got[0].Label != "at.obdev.littlesnitch.networkextension" || !got[0].Loaded {
		t.Errorf("ParseSystemExtensions = %+v", got)
	}
}

func TestAllWithFakeSystem(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, "Library/LaunchAgents/com.example.agent.plist"), `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><dict>
<key>Label</key><string>com.example.agent</string>
<key>ProgramArguments</key><array><string>/bin/sleep</string><string>60</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
</dict></plist>`)
	write(filepath.Join(root, "Library/LaunchDaemons/broken.plist"), "not a plist {")
	write(filepath.Join(home, "Applications/Tool.app/Contents/Library/LoginItems/Helper.app/Contents/Info.plist"),
		`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.helper</string></dict></plist>`)

	run := func(name string, args ...string) ([]byte, error) {
		if name == "launchctl" && len(args) == 2 && args[0] == "print" && args[1] == "gui/501" {
			return []byte("gui/501 = {\n\tservices = {\n\t\t  4242   - \tcom.example.agent\n\t}\n}\n"), nil
		}
		return nil, errors.New("not available in test")
	}
	res := All(Config{UID: 501, Home: home, Root: root, AppDirs: []string{filepath.Join(home, "Applications")}, Run: run, SkipLoginItems: true})

	byLabel := map[string]items.Item{}
	for _, it := range res.Items {
		byLabel[it.Label] = it
	}
	agent := byLabel["com.example.agent"]
	if !agent.RunAtLoad || !agent.KeepAlive || agent.PID != 4242 || len(agent.Args) != 2 {
		t.Errorf("agent = %+v", agent)
	}
	if byLabel["broken"].ReadErr == "" {
		t.Error("broken plist should record a read error")
	}
	helper := byLabel["com.example.helper"]
	if helper.Source != items.AppHelper || filepath.Base(helper.ParentApp) != "Tool.app" {
		t.Errorf("helper = %+v", helper)
	}
}
