package scan

import (
	"bufio"
	"bytes"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// BTMRecord is one item from `sfltool dumpbtm`.
type BTMRecord struct {
	UID            int
	Name           string
	Developer      string
	Type           string // "legacy daemon", "login item", "app", ...
	Enabled        bool
	Allowed        bool
	Identifier     string // e.g. "16.com.cloudflare.cloudflared"
	URL            string // raw URL field; relative for bundled items
	ExecutablePath string
	BundleID       string
	ParentID       string
}

// Label strips the numeric type prefix from the identifier
// ("16.com.foo" → "com.foo").
func (r BTMRecord) Label() string {
	if i := strings.IndexByte(r.Identifier, '.'); i > 0 {
		if _, err := strconv.Atoi(r.Identifier[:i]); err == nil {
			return r.Identifier[i+1:]
		}
	}
	return r.Identifier
}

// Path returns the URL as a file system path. Relative URLs (bundled items)
// come back unchanged but unescaped.
func (r BTMRecord) Path() string {
	if r.URL == "" || r.URL == "(null)" {
		return ""
	}
	if u, err := url.Parse(r.URL); err == nil && (u.Scheme == "file" || u.Scheme == "") {
		return strings.TrimSuffix(u.Path, "/")
	}
	return r.URL
}

// IsStartup reports whether the record describes something that runs on its
// own, as opposed to a developer or app grouping record or a Quick Look or
// Spotlight plugin.
func (r BTMRecord) IsStartup() bool {
	switch r.Type {
	case "legacy daemon", "legacy agent", "login item", "agent", "daemon":
		return true
	}
	return false
}

// BTM is the parsed database, plus an index for resolving parent apps.
type BTM struct {
	Records []BTMRecord
	byID    map[string]BTMRecord
}

var (
	recordsFor = regexp.MustCompile(`^\s*Records for UID (-?\d+)\b`)
	itemStart  = regexp.MustCompile(`^\s*#\d+:\s*$`)
	field      = regexp.MustCompile(`^\s*([A-Z][A-Za-z. ]*):\s?(.*)$`)
	typeName   = regexp.MustCompile(`^(.*?)\s*\(0x[0-9a-fA-F]+\)$`)
)

// ParseBTM parses `sfltool dumpbtm` output.
func ParseBTM(out []byte) *BTM {
	b := &BTM{byID: map[string]BTMRecord{}}
	uid := 0
	var cur *BTMRecord
	inItems := false
	flush := func() {
		if cur != nil && cur.Identifier != "" {
			b.Records = append(b.Records, *cur)
			if _, seen := b.byID[cur.Identifier]; !seen {
				b.byID[cur.Identifier] = *cur
			}
		}
		cur = nil
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if g := recordsFor.FindStringSubmatch(line); g != nil {
			flush()
			uid, _ = strconv.Atoi(g[1])
			inItems = false
			continue
		}
		if strings.TrimSpace(line) == "Items:" {
			inItems = true
			continue
		}
		if !inItems {
			continue
		}
		if itemStart.MatchString(line) {
			flush()
			cur = &BTMRecord{UID: uid}
			continue
		}
		if cur == nil {
			continue
		}
		g := field.FindStringSubmatch(line)
		if g == nil {
			continue
		}
		val := strings.TrimSpace(g[2])
		switch g[1] {
		case "Name":
			cur.Name = nullable(val)
		case "Developer Name":
			cur.Developer = nullable(val)
		case "Type":
			if t := typeName.FindStringSubmatch(val); t != nil {
				val = t[1]
			}
			cur.Type = val
		case "Disposition":
			cur.Enabled, cur.Allowed = parseDisposition(val)
		case "Identifier":
			cur.Identifier = val
		case "URL":
			cur.URL = nullable(val)
		case "Executable Path":
			cur.ExecutablePath = nullable(val)
		case "Bundle Identifier":
			cur.BundleID = nullable(val)
		case "Parent Identifier":
			cur.ParentID = nullable(val)
		}
	}
	flush()
	return b
}

func nullable(s string) string {
	if s == "(null)" {
		return ""
	}
	return s
}

// parseDisposition reads "[enabled, allowed, visible, notified] (0xb)".
func parseDisposition(s string) (enabled, allowed bool) {
	if i := strings.IndexByte(s, ']'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "[")
	for _, f := range strings.Split(s, ",") {
		switch strings.TrimSpace(f) {
		case "enabled":
			enabled = true
		case "allowed":
			allowed = true
		}
	}
	return enabled, allowed
}

// Parent returns the record a bundled item belongs to, if known.
func (b *BTM) Parent(r BTMRecord) (BTMRecord, bool) {
	p, ok := b.byID[r.ParentID]
	return p, ok
}

// ResolvedPath returns an absolute path for the record: the URL for legacy
// items, or the parent app joined with the relative URL for bundled ones.
func (b *BTM) ResolvedPath(r BTMRecord) string {
	p := r.Path()
	if p == "" || strings.HasPrefix(p, "/") {
		return p
	}
	if parent, ok := b.Parent(r); ok {
		if pp := parent.Path(); strings.HasPrefix(pp, "/") {
			return pp + "/" + p
		}
	}
	return ""
}
