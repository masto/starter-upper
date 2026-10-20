// Package tui is the interactive browser.
package tui

import (
	"bytes"
	"fmt"
	"os/exec"
	"os/user"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/masto/starter-upper/internal/action"
	"github.com/masto/starter-upper/internal/check"
	"github.com/masto/starter-upper/internal/items"
	"github.com/masto/starter-upper/internal/scan"
)

const loginItemsSettings = "x-apple.systempreferences:com.apple.LoginItems-Settings.extension"

type mode int

const (
	browsing mode = iota
	filtering
	confirming
	helping
	busy
)

// Model is the Bubble Tea model.
type Model struct {
	cfg  scan.Config
	dirs action.Dirs

	all      []items.Item
	visible  []int // indexes into all
	cursor   int
	offset   int
	warnings []string

	mode        mode
	filter      textinput.Model
	flaggedOnly bool

	plan     *action.Plan
	planItem string // key of the item the plan is for

	status    string
	statusErr bool
	btmState  string // shown in the header

	width, height int
}

// New creates the model. Scanning starts in Init.
func New(cfg scan.Config) Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "filter by name, label, path or kind"
	m := Model{cfg: cfg, dirs: action.DefaultDirs(), filter: ti, mode: busy, status: "Scanning…", btmState: "not loaded (press b)"}
	if cfg.BTM != nil {
		m.btmState = "loaded from file"
	}
	return m
}

type scanDoneMsg struct{ res scan.Result }
type btmMsg struct {
	btm    *scan.BTM
	err    error
	silent bool
}
type sudoReadyMsg struct{ err error }
type actionDoneMsg struct{ res action.Result }

func (m Model) Init() tea.Cmd {
	if m.cfg.BTM != nil {
		return m.scanCmd()
	}
	// Try the background-item database without prompting: this works when
	// sudo's timestamp is still fresh. Otherwise the user presses b.
	return tea.Batch(m.scanCmd(), func() tea.Msg {
		out, err := exec.Command("sudo", "-n", "sfltool", "dumpbtm").Output()
		if err != nil {
			return btmMsg{err: err, silent: true}
		}
		return btmMsg{btm: scan.ParseBTM(out)}
	})
}

func (m Model) scanCmd() tea.Cmd {
	cfg := m.cfg
	dirs := m.dirs
	return func() tea.Msg {
		res := scan.All(cfg)
		if saved, err := action.SavedLoginItems(dirs); err == nil {
			res.Items = append(res.Items, action.SavedAsItems(saved)...)
		}
		check.Run(res.Items, check.Env{UID: cfg.UID})
		sortItems(res.Items)
		return scanDoneMsg{res}
	}
}

func sortItems(list []items.Item) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := &list[i], &list[j]
		if a.Worst() != b.Worst() {
			return a.Worst() > b.Worst()
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.filter.Width = max(10, msg.Width-4)
		m.clamp()
		return m, nil

	case scanDoneMsg:
		key := m.selectedKey()
		m.all = msg.res.Items
		m.warnings = msg.res.Warnings
		m.refilter()
		m.selectKey(key)
		if m.mode == busy {
			m.mode = browsing
		}
		if m.status == "Scanning…" {
			m.setStatus(fmt.Sprintf("%d items found.", len(m.all)), false)
		}
		return m, nil

	case btmMsg:
		if msg.err != nil {
			if !msg.silent {
				m.setStatus("Couldn't read the background-item list: "+msg.err.Error(), true)
			}
			return m, nil
		}
		m.cfg.BTM = msg.btm
		m.btmState = "loaded"
		if !msg.silent {
			m.setStatus("Background-item list loaded.", false)
		}
		return m, m.scanCmd()

	case sudoReadyMsg:
		if msg.err != nil {
			m.mode = browsing
			m.plan = nil
			m.setStatus("sudo failed; nothing was changed.", true)
			return m, nil
		}
		return m, m.runPlan()

	case actionDoneMsg:
		m.mode = browsing
		verb := m.plan.Verb
		m.plan = nil
		if msg.res.Err != nil {
			m.setStatus(capitalize(verb)+" failed: "+oneLine(msg.res.Err.Error()), true)
		} else {
			s := capitalize(verb) + " done."
			if msg.res.BackupDir != "" {
				s += " Backup in " + msg.res.BackupDir
			}
			m.setStatus(s, false)
		}
		return m, m.scanCmd()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case busy:
		return m, nil
	case helping:
		m.mode = browsing
		return m, nil
	case filtering:
		switch k.String() {
		case "enter":
			m.mode = browsing
			m.filter.Blur()
			return m, nil
		case "esc":
			m.mode = browsing
			m.filter.Blur()
			m.filter.SetValue("")
			m.refilter()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(k)
		m.refilter()
		return m, cmd
	case confirming:
		if k.String() == "y" || k.String() == "Y" {
			if m.plan.NeedsRoot() {
				m.mode = busy
				m.setStatus("Waiting for sudo…", false)
				c := exec.Command("sudo", "-v", "-p", "starter-upper needs administrator rights to "+m.plan.Verb+" this item.\nPassword for %u: ")
				return m, tea.ExecProcess(c, func(err error) tea.Msg { return sudoReadyMsg{err} })
			}
			m.mode = busy
			return m, m.runPlan()
		}
		m.mode = browsing
		m.plan = nil
		m.setStatus("Cancelled; nothing was changed.", false)
		return m, nil
	}

	// browsing
	switch k.String() {
	case "q", "esc":
		if k.String() == "esc" && m.filter.Value() != "" {
			m.filter.SetValue("")
			m.refilter()
			return m, nil
		}
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+u":
		m.move(-m.listHeight())
	case "pgdown", "ctrl+d", " ":
		m.move(m.listHeight())
	case "home", "g":
		m.move(-len(m.visible))
	case "end", "G":
		m.move(len(m.visible))
	case "/":
		m.mode = filtering
		return m, m.filter.Focus()
	case "f":
		m.flaggedOnly = !m.flaggedOnly
		m.refilter()
	case "?":
		m.mode = helping
	case "r":
		m.setStatus("Rescanning…", false)
		return m, m.scanCmd()
	case "b":
		var buf bytes.Buffer
		c := exec.Command("sudo", "-p", "starter-upper needs administrator rights to read the background-item list (sfltool dumpbtm).\nPassword for %u: ", "sfltool", "dumpbtm")
		c.Stdout = &buf
		return m, tea.ExecProcess(c, func(err error) tea.Msg {
			if err != nil {
				return btmMsg{err: err}
			}
			return btmMsg{btm: scan.ParseBTM(buf.Bytes())}
		})
	case "s":
		_ = exec.Command("open", loginItemsSettings).Start()
		m.setStatus("Opened Login Items & Extensions in System Settings.", false)
	case "o":
		if it := m.selected(); it != nil {
			if p := revealPath(it); p != "" {
				_ = exec.Command("open", "-R", p).Start()
				m.setStatus("Revealed "+p+" in Finder.", false)
			} else {
				m.setStatus("Nothing on disk to reveal for this item.", true)
			}
		}
	case "d":
		if it := m.selected(); it != nil {
			var plan *action.Plan
			var err error
			if it.Disabled() {
				plan, err = action.Enable(it)
			} else {
				plan, err = action.Disable(it)
			}
			m.startConfirm(it, plan, err)
		}
	case "x", "delete", "backspace":
		if it := m.selected(); it != nil {
			plan, err := action.Delete(it)
			m.startConfirm(it, plan, err)
		}
	}
	return m, nil
}

func (m *Model) startConfirm(it *items.Item, plan *action.Plan, err error) {
	if err != nil {
		m.setStatus(capitalize(err.Error())+".", true)
		return
	}
	m.plan = plan
	m.planItem = it.Key()
	m.mode = confirming
}

func (m Model) runPlan() tea.Cmd {
	plan := m.plan
	dirs := m.dirs
	return func() tea.Msg {
		return actionDoneMsg{action.Execute(plan, action.ExecStep, dirs)}
	}
}

func revealPath(it *items.Item) string {
	for _, p := range []string{it.PlistPath, it.Program, it.ParentApp} {
		if strings.HasPrefix(p, "/") && exists(p) {
			return p
		}
	}
	return ""
}

// --- list management

func (m *Model) refilter() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.visible = m.visible[:0]
	for i := range m.all {
		it := &m.all[i]
		if m.flaggedOnly && it.Worst() < items.Warning {
			continue
		}
		if q != "" && !matches(it, q) {
			continue
		}
		m.visible = append(m.visible, i)
	}
	m.clamp()
}

func matches(it *items.Item, q string) bool {
	hay := strings.ToLower(strings.Join([]string{it.Name, it.Label, it.PlistPath, it.Program, it.Source.String(), it.ParentApp}, "\x00"))
	return strings.Contains(hay, q)
}

func (m *Model) move(d int) {
	m.cursor += d
	m.clamp()
}

func (m *Model) clamp() {
	if m.cursor >= len(m.visible) {
		m.cursor = len(m.visible) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *Model) selected() *items.Item {
	if m.cursor < 0 || m.cursor >= len(m.visible) {
		return nil
	}
	return &m.all[m.visible[m.cursor]]
}

func (m *Model) selectedKey() string {
	if it := m.selected(); it != nil {
		return it.Key()
	}
	return ""
}

func (m *Model) selectKey(key string) {
	for i, idx := range m.visible {
		if m.all[idx].Key() == key {
			m.cursor = i
			m.clamp()
			return
		}
	}
}

func (m *Model) setStatus(s string, isErr bool) {
	m.status = s
	m.statusErr = isErr
}

// --- view

var (
	colProblem = lipgloss.AdaptiveColor{Light: "#B42318", Dark: "#F97066"}
	colWarning = lipgloss.AdaptiveColor{Light: "#B54708", Dark: "#FDB022"}
	colInfo    = lipgloss.AdaptiveColor{Light: "#175CD3", Dark: "#84CAFF"}
	colMuted   = lipgloss.AdaptiveColor{Light: "#667085", Dark: "#98A2B3"}
	colOK      = lipgloss.AdaptiveColor{Light: "#067647", Dark: "#75E0A7"}
	colAccent  = lipgloss.AdaptiveColor{Light: "#5925DC", Dark: "#BDB4FE"}

	sTitle    = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	sMuted    = lipgloss.NewStyle().Foreground(colMuted)
	sSelected = lipgloss.NewStyle().Reverse(true)
	sBold     = lipgloss.NewStyle().Bold(true)
	sKey      = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	sErr      = lipgloss.NewStyle().Foreground(colProblem)
	sBox      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colAccent).Padding(0, 1)
)

func sevStyle(s items.Severity) lipgloss.Style {
	switch s {
	case items.Problem:
		return lipgloss.NewStyle().Foreground(colProblem)
	case items.Warning:
		return lipgloss.NewStyle().Foreground(colWarning)
	case items.Info:
		return lipgloss.NewStyle().Foreground(colInfo)
	}
	return lipgloss.NewStyle()
}

func sevMark(s items.Severity) string {
	switch s {
	case items.Problem:
		return "●"
	case items.Warning:
		return "▲"
	case items.Info:
		return "·"
	}
	return " "
}

// StateText is a short description of whether the item runs.
func StateText(it *items.Item, uid int) string {
	switch {
	case it.Source == items.SystemExtension:
		if it.Loaded {
			return "active"
		}
		return "inactive"
	case it.Disabled():
		return "disabled"
	case it.Blocked(uid):
		return "blocked"
	case it.PID > 0:
		return "running"
	case it.Loaded:
		return "loaded"
	case it.Source == items.LoginItem:
		return "on"
	case it.Source == items.BTMOnly:
		if _, ok := it.BTMFor(uid); !ok {
			return "other user"
		}
		return "registered"
	case it.Source == items.Cron:
		return "scheduled"
	}
	return "not loaded"
}

func stateStyle(state string) lipgloss.Style {
	switch state {
	case "running", "active", "on", "scheduled":
		return lipgloss.NewStyle().Foreground(colOK)
	case "disabled", "blocked":
		return sMuted
	}
	return lipgloss.NewStyle()
}

// chromeLines is the header (2), separator (1) and footer (2).
const chromeLines = 5

func (m Model) listHeight() int {
	// The list gets ~45% of what's left; details get the rest.
	avail := m.height - chromeLines
	if avail < 6 {
		return max(1, avail/2)
	}
	return max(3, avail*45/100)
}

func (m Model) View() string {
	if m.width == 0 {
		return "Scanning…"
	}
	switch m.mode {
	case helping:
		return m.helpView()
	case confirming:
		return m.confirmView()
	}

	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n")
	b.WriteString(m.listView())
	b.WriteString(sMuted.Render(strings.Repeat("─", m.width)))
	b.WriteString("\n")
	detailH := m.height - chromeLines - m.listHeight()
	b.WriteString(clipLines(m.detailView(), detailH))
	b.WriteString("\n")
	b.WriteString(m.footerView())
	return b.String()
}

func (m Model) headerView() string {
	counts := map[items.Severity]int{}
	for i := range m.all {
		if w := m.all[i].Worst(); w >= 0 {
			counts[w]++
		}
	}
	left := sTitle.Render("starter-upper") + sMuted.Render(fmt.Sprintf("  %d items  ", len(m.all))) +
		sevStyle(items.Problem).Render(fmt.Sprintf("● %d", counts[items.Problem])) + "  " +
		sevStyle(items.Warning).Render(fmt.Sprintf("▲ %d", counts[items.Warning])) + "  " +
		sevStyle(items.Info).Render(fmt.Sprintf("· %d", counts[items.Info]))
	right := sMuted.Render("background list: " + m.btmState)
	if m.flaggedOnly {
		right = sKey.Render("[flagged only] ") + right
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	line := left + strings.Repeat(" ", max(1, gap)) + right
	second := ""
	if m.mode == filtering {
		second = m.filter.View()
	} else if q := m.filter.Value(); q != "" {
		second = sMuted.Render("filter: ") + q + sMuted.Render(fmt.Sprintf("  (%d shown, esc clears)", len(m.visible)))
	} else if len(m.warnings) > 0 {
		second = sevStyle(items.Warning).Render(truncate(m.warnings[0], m.width))
	}
	return truncate(line, m.width) + "\n" + second
}

func (m Model) listView() string {
	h := m.listHeight()
	var b strings.Builder
	nameW := max(16, (m.width-34)*45/100)
	progW := max(0, m.width-34-nameW)
	for row := 0; row < h; row++ {
		i := m.offset + row
		if i >= len(m.visible) {
			b.WriteString("\n")
			continue
		}
		it := &m.all[m.visible[i]]
		w := it.Worst()
		state := StateText(it, m.cfg.UID)
		prog := it.Program
		if prog == "" {
			prog = it.Note
		}
		cells := fmt.Sprintf(" %s %-11s %-13s %s %s",
			sevMark(w), state, it.Source, pad(truncate(it.Name, nameW), nameW), truncate(shortenHome(prog), progW))
		if i == m.cursor {
			b.WriteString(sSelected.Render(pad(cells, m.width)))
		} else {
			b.WriteString(" " + sevStyle(w).Render(sevMark(w)) + " " +
				stateStyle(state).Render(fmt.Sprintf("%-11s", state)) + " " +
				sMuted.Render(fmt.Sprintf("%-13s", it.Source)) + " " +
				pad(truncate(it.Name, nameW), nameW) + " " +
				sMuted.Render(truncate(shortenHome(prog), progW)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m Model) detailView() string {
	it := m.selected()
	if it == nil {
		if len(m.all) == 0 {
			return sMuted.Render("Nothing to show yet.")
		}
		return sMuted.Render("No items match. Press esc to clear the filter, or f to show everything.")
	}
	w := m.width
	var b strings.Builder
	b.WriteString(sBold.Render(it.Name) + sMuted.Render("  "+it.Source.String()+" · "+StateText(it, m.cfg.UID)) + "\n")

	field := func(k, v string) {
		if v == "" {
			return
		}
		b.WriteString(wrap(sMuted.Render(fmt.Sprintf("%-10s", k))+v, w, 11) + "\n")
	}
	if it.Label != it.Name {
		field("Label", it.Label)
	}
	field("File", it.PlistPath)
	if it.Program != it.PlistPath {
		field("Program", it.Program)
	}
	if len(it.Args) > 1 {
		field("Args", strings.Join(maskedArgs(it.Args)[1:], " "))
	}
	field("App", it.ParentApp)
	if it.Source <= items.Daemon || (it.Source == items.AppHelper && it.Domain != "") {
		run := it.Triggers()
		if it.PID > 0 {
			run += " · pid " + strconv.Itoa(it.PID)
		}
		field("Runs", run)
		field("Domain", it.Domain+overrideText(it.Override))
	}
	if len(it.BTM) > 0 {
		field("Settings", btmText(it))
	}
	field("Note", it.Note)

	for _, is := range it.Issues {
		st := sevStyle(is.Severity)
		b.WriteString(wrap(st.Render(sevMark(is.Severity)+" "+is.Message), w, 2) + "\n")
		if is.Fix != "" {
			b.WriteString(wrap(sMuted.Render("  → "+is.Fix), w, 4) + "\n")
		}
	}
	return b.String()
}

func overrideText(o items.Override) string {
	switch o {
	case items.OverrideDisabled:
		return " · disabled override"
	case items.OverrideEnabled:
		return " · enabled override"
	}
	return ""
}

func btmText(it *items.Item) string {
	var parts []string
	for _, s := range it.BTM {
		who := userName(s.UID)
		st := "allowed"
		if !s.Allowed {
			st = "blocked"
		}
		parts = append(parts, who+": "+st)
	}
	return strings.Join(parts, ", ")
}

var userNames = map[int]string{}

func userName(uid int) string {
	if uid == -2 {
		return "system"
	}
	if n, ok := userNames[uid]; ok {
		return n
	}
	n := "uid " + strconv.Itoa(uid)
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		n = u.Username
	}
	userNames[uid] = n
	return n
}

func maskedArgs(args []string) []string {
	secret := check.SecretArgs(args)
	out := make([]string, len(args))
	for i, a := range args {
		switch {
		case secret[i] && strings.Contains(a, "="):
			out[i] = a[:strings.Index(a, "=")+1] + "••••••"
		case secret[i]:
			out[i] = "••••••"
		default:
			out[i] = a
		}
	}
	return out
}

// footerView is the status line followed by the key hints.
func (m Model) footerView() string {
	st := lipgloss.NewStyle()
	if m.statusErr {
		st = sErr
	}
	var keys []string
	for _, k := range [][2]string{{"d", "disable/enable"}, {"x", "delete"}, {"o", "reveal"}, {"s", "settings"},
		{"b", "bg list"}, {"/", "filter"}, {"f", "flagged"}, {"r", "rescan"}, {"?", "help"}, {"q", "quit"}} {
		keys = append(keys, sKey.Render(k[0])+" "+sMuted.Render(k[1]))
	}
	return st.Render(truncate(m.status, m.width)) + "\n" + truncate(strings.Join(keys, "  "), m.width)
}

func (m Model) confirmView() string {
	p := m.plan
	var b strings.Builder
	title := capitalize(p.Verb) + "?"
	b.WriteString(sBold.Render(title) + "\n\n")
	b.WriteString(wrap(p.Summary, m.width-6, 0) + "\n")
	if p.Warning != "" {
		b.WriteString("\n" + wrap(sevStyle(items.Warning).Render("▲ "+p.Warning), m.width-6, 2) + "\n")
	}
	if p.BackupFrom != "" {
		b.WriteString("\n" + sMuted.Render("A copy is saved first to "+shortenHome(m.dirs.BackupsDir())+"/<timestamp>/") + "\n")
	}
	if len(p.Steps) > 0 {
		b.WriteString("\n" + sMuted.Render("Commands:") + "\n")
		for _, s := range p.Steps {
			b.WriteString(wrap("  "+s.String(), m.width-6, 4) + "\n")
		}
	}
	if p.NeedsRoot() {
		b.WriteString("\n" + sMuted.Render("Needs administrator rights; sudo will ask for your password if it hasn't recently.") + "\n")
	}
	b.WriteString("\n" + sKey.Render("y") + " to " + p.Verb + ", any other key to cancel")
	box := sBox.Width(min(m.width-2, 90)).Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) helpView() string {
	rows := [][2]string{
		{"↑/↓ j/k", "move; pgup/pgdn and g/G jump"},
		{"d", "disable, or re-enable if already disabled. Nothing is deleted:"},
		{"", "launchd items get a launchctl override, login items are removed and"},
		{"", "remembered, cron lines are commented out."},
		{"x", "delete for good after saving a backup copy"},
		{"o", "reveal the item's file in Finder"},
		{"s", "open Login Items & Extensions in System Settings, for items"},
		{"", "that only the owning app or System Settings can change"},
		{"b", "read macOS's background-item list (needs sudo), which adds the"},
		{"", "Allow-in-Background state and other users' items"},
		{"/", "filter; enter keeps it, esc clears it"},
		{"f", "show only items with warnings or problems"},
		{"r", "rescan"},
		{"q", "quit"},
		{"", ""},
		{"●", "problem: broken, missing or risky"},
		{"▲", "warning: worth a look"},
		{"·", "note: informational"},
		{"", ""},
		{"Backups", shortenHome(m.dirs.BackupsDir())},
	}
	var b strings.Builder
	b.WriteString(sBold.Render("starter-upper help") + "\n\n")
	for _, r := range rows {
		b.WriteString(sKey.Render(fmt.Sprintf("%-9s", r[0])) + " " + r[1] + "\n")
	}
	b.WriteString("\n" + sMuted.Render("Press any key to return."))
	box := sBox.Width(min(m.width-2, 90)).Render(b.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
