// starter-upper examines, validates, cleans up and adjusts macOS startup
// items.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"starter-upper/internal/check"
	"starter-upper/internal/items"
	"starter-upper/internal/scan"
	"starter-upper/internal/tui"
)

func main() {
	btmFile := flag.String("btm", "", "read `sfltool dumpbtm` output from this file instead of running it with sudo")
	noLogin := flag.Bool("no-login-items", false, "skip the System Events query for Open at Login items")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `Usage: starter-upper [flags] [report]

With no command, opens the interactive browser. "report" prints every item
and its findings as plain text instead.

Flags:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	cfg := scan.DefaultConfig()
	cfg.SkipLoginItems = *noLogin
	if *btmFile != "" {
		data, err := os.ReadFile(*btmFile)
		if err != nil {
			fatal(err)
		}
		cfg.BTM = scan.ParseBTM(data)
	}

	switch flag.Arg(0) {
	case "report":
		report(os.Stdout, cfg)
	case "":
		p := tea.NewProgram(tui.New(cfg), tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			fatal(err)
		}
	default:
		flag.Usage()
		os.Exit(2)
	}
}

func report(w io.Writer, cfg scan.Config) {
	res := scan.All(cfg)
	check.Run(res.Items, check.Env{UID: cfg.UID})
	list := res.Items
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Worst() != list[j].Worst() {
			return list[i].Worst() > list[j].Worst()
		}
		return list[i].Source < list[j].Source
	})
	for _, wmsg := range res.Warnings {
		fmt.Fprintln(w, "note:", wmsg)
	}
	if cfg.BTM == nil {
		fmt.Fprintln(w, "note: background-item database not read; pass --btm or use the TUI to include it")
	}
	for _, it := range list {
		fmt.Fprintf(w, "%-13s %-40s %s\n", it.Source, it.Name, tui.StateText(&it, cfg.UID))
		if it.PlistPath != "" {
			fmt.Fprintf(w, "    %s\n", it.PlistPath)
		}
		for _, is := range it.Issues {
			fmt.Fprintf(w, "    [%s] %s\n", strings.ToUpper(is.Severity.String()), is.Message)
		}
	}
	counts := map[items.Severity]int{}
	for _, it := range list {
		for _, is := range it.Issues {
			counts[is.Severity]++
		}
	}
	fmt.Fprintf(w, "\n%d items, %d problems, %d warnings, %d notes\n",
		len(list), counts[items.Problem], counts[items.Warning], counts[items.Info])
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "starter-upper:", err)
	os.Exit(1)
}
