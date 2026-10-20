package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/masto/starter-upper/internal/items"
)

func codes(it items.Item) map[string]bool {
	m := map[string]bool{}
	for _, is := range it.Issues {
		m[is.Code] = true
	}
	return m
}

func TestChecks(t *testing.T) {
	dir := t.TempDir()
	prog := filepath.Join(dir, "prog")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plistPath := func(name string) string { return filepath.Join(dir, name+".plist") }

	tests := []struct {
		name string
		item items.Item
		want []string
		not  []string
	}{
		{
			name: "missing program",
			item: items.Item{Source: items.UserAgent, Label: "a", PlistPath: plistPath("a"), Program: "/nonexistent/thing", RunAtLoad: true},
			want: []string{"missing-program"},
		},
		{
			name: "open -W missing app",
			item: items.Item{Source: items.GlobalAgent, Label: "b", PlistPath: plistPath("b"), Program: "/usr/bin/open",
				Args: []string{"/usr/bin/open", "-W", "/Applications/Definitely Not Here.app"}, RunAtLoad: true},
			want: []string{"missing-target"},
		},
		{
			name: "inert",
			item: items.Item{Source: items.UserAgent, Label: "c", PlistPath: plistPath("c"), Program: prog},
			want: []string{"inert"},
		},
		{
			name: "on-demand is not inert",
			item: items.Item{Source: items.UserAgent, Label: "d", PlistPath: plistPath("d"), Program: prog, OnDemand: true},
			not:  []string{"inert"},
		},
		{
			name: "secret in world-readable plist",
			item: items.Item{Source: items.Daemon, Label: "e", PlistPath: plistPath("e"), Program: prog, RunAtLoad: true,
				Args: []string{prog, "tunnel", "run", "--token", "abc"}, PlistMode: 0o644},
			want: []string{"secret-in-args"},
		},
		{
			name: "label mismatch ignores case",
			item: items.Item{Source: items.Daemon, Label: "com.Foo.bar", PlistPath: plistPath("com.foo.bar"), Program: prog, RunAtLoad: true},
			not:  []string{"label-mismatch"},
		},
		{
			name: "label mismatch",
			item: items.Item{Source: items.Daemon, Label: "fans", PlistPath: plistPath("com.vendor.fans"), Program: prog, RunAtLoad: true},
			want: []string{"label-mismatch"},
		},
		{
			name: "runs from tmp",
			item: items.Item{Source: items.UserAgent, Label: "f", PlistPath: plistPath("f"), Program: "/tmp/x", RunAtLoad: true},
			want: []string{"suspicious-path"},
		},
		{
			name: "fake apple label",
			item: items.Item{Source: items.UserAgent, Label: "com.apple.updater", PlistPath: plistPath("com.apple.updater"), Program: prog, RunAtLoad: true},
			want: []string{"fake-apple"},
		},
		{
			name: "blocked in settings",
			item: items.Item{Source: items.GlobalAgent, Label: "g", PlistPath: plistPath("g"), Program: prog, RunAtLoad: true,
				BTM: []items.BTMState{{UID: 501, Enabled: true, Allowed: false}}},
			want: []string{"blocked"},
		},
		{
			name: "empty plist",
			item: items.Item{Source: items.UserAgent, Label: "h", PlistPath: plistPath("h"), Empty: true},
			want: []string{"empty"},
			not:  []string{"no-program", "inert"},
		},
		{
			name: "world-writable system plist",
			item: items.Item{Source: items.Daemon, Label: "i", PlistPath: plistPath("i"), Program: prog, RunAtLoad: true, PlistMode: 0o666},
			want: []string{"plist-writable"},
		},
		{
			name: "obsolete startup item",
			item: items.Item{Source: items.StartupItem, Label: "Old", PlistPath: dir},
			want: []string{"obsolete"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := []items.Item{tt.item}
			Run(list, Env{UID: 501})
			got := codes(list[0])
			for _, c := range tt.want {
				if !got[c] {
					t.Errorf("missing %q; got %v", c, got)
				}
			}
			for _, c := range tt.not {
				if got[c] {
					t.Errorf("unexpected %q", c)
				}
			}
		})
	}
}

func TestDuplicateLabels(t *testing.T) {
	list := []items.Item{
		{Source: items.UserAgent, Label: "dup", PlistPath: "/a/dup.plist", RunAtLoad: true},
		{Source: items.GlobalAgent, Label: "dup", PlistPath: "/b/dup.plist", RunAtLoad: true},
	}
	Run(list, Env{UID: 501})
	for i := range list {
		if !list[i].HasIssue("duplicate-label") {
			t.Errorf("item %d missing duplicate-label", i)
		}
	}
}

func TestSecretArgs(t *testing.T) {
	jwt := "eyJhIjoiZXhhbXBsZS1hY2NvdW50IiwidCI6Im5vdC1hLXJlYWwtdG9rZW4ifQ" // base64 of a made-up payload
	tests := []struct {
		args []string
		want []int
	}{
		{[]string{"cf", "tunnel", "--token", "x"}, []int{3}},
		{[]string{"cf", "--password=hunter2"}, []int{1}},
		{[]string{"cf", jwt}, []int{1}},
		{[]string{"/usr/bin/open", "-W", "/Applications/Some/Very/Long/Path/That/Is/Over/Forty/Chars.app"}, nil},
		{[]string{"prog", "--verbose"}, nil},
	}
	for _, tt := range tests {
		got := SecretArgs(tt.args)
		if len(got) != len(tt.want) {
			t.Errorf("SecretArgs(%q) = %v, want %v", tt.args, got, tt.want)
			continue
		}
		for _, i := range tt.want {
			if !got[i] {
				t.Errorf("SecretArgs(%q) missing index %d", tt.args, i)
			}
		}
	}
}
