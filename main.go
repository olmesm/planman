// planman is a self-contained local review tool for coding agents.
//
// `planman open file.md` serves a markdown file for block-level review;
// comments persist inside the file as CriticMarkup. `planman diff`
// serves a GitHub-style review of a git repo's changes; comments persist
// in a sidecar under .git/planman. Both block until the reviewer hands
// control back, the timeout elapses, or the process is interrupted.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/olmesm/planman/internal/diffmode"
	"github.com/olmesm/planman/internal/docmode"
	"github.com/olmesm/planman/internal/server"
)

var version = "dev" // overridden by goreleaser via -ldflags

const usage = `planman — reviews for coding agents

Usage:
  planman open <file.md> [flags]   Review a markdown file; blocks until handback
  planman diff [path] [flags]      Review a repo's git diff; blocks until handback
  planman version                  Print version

Shared flags:
  --json           Emit machine-readable JSON events on stdout
  --timeout DUR    Give up after DUR (default 30m, e.g. 45m, 2h, 90s)
  --port N         Listen on a fixed port (default: ephemeral)
  --no-browser     Don't open the browser automatically

Diff flags:
  --scope S        working | branch | all (default working)
  --base REF       Base ref for branch/all scopes (default: origin default branch)
  --stay           Serve until interrupted instead of blocking on handback;
                   binds ports 7350-7359 so agents can discover the server

Exit codes: 0 review handed back · 2 timeout · 130 interrupted
`

type event struct {
	Event         string  `json:"event"`
	File          string  `json:"file,omitempty"`
	Root          string  `json:"root,omitempty"`
	URL           string  `json:"url,omitempty"`
	CommentsAdded int     `json:"comments_added"`
	CommentsOpen  int     `json:"comments_open"`
	CommentsTotal int     `json:"comments_total"`
	Export        string  `json:"export,omitempty"`
	DurationSecs  float64 `json:"duration_seconds,omitempty"`
	Error         string  `json:"error,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println("planman", version)
	case "open":
		os.Exit(runOpen(os.Args[2:]))
	case "diff":
		os.Exit(runDiff(os.Args[2:]))
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
}

// sharedFlags are the flags every review command takes.
type sharedFlags struct {
	json      *bool
	timeout   *time.Duration
	port      *int
	noBrowser *bool
}

func addSharedFlags(fs *flag.FlagSet) sharedFlags {
	return sharedFlags{
		json:      fs.Bool("json", false, "emit JSON events"),
		timeout:   fs.Duration("timeout", 30*time.Minute, "review timeout"),
		port:      fs.Int("port", 0, "port to listen on (0 = ephemeral)"),
		noBrowser: fs.Bool("no-browser", false, "don't open the browser"),
	}
}

// parseWithTrailingFlags parses flags, allows one positional argument,
// then parses any flags that followed it. Returns the positional arg.
func parseWithTrailingFlags(fs *flag.FlagSet, args []string) (string, bool) {
	_ = fs.Parse(args)
	rest := fs.Args()
	pos := ""
	if len(rest) > 0 {
		pos = rest[0]
		_ = fs.Parse(rest[1:])
		if fs.NArg() > 0 {
			return "", false
		}
	}
	return pos, true
}

func runOpen(args []string) int {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	shared := addSharedFlags(fs)
	path, ok := parseWithTrailingFlags(fs, args)
	if !ok || path == "" {
		fmt.Fprint(os.Stderr, usage)
		return 1
	}
	mode, err := docmode.New(path)
	if err != nil {
		return failEarly(shared, err)
	}
	return runReview(mode, shared, false, reviewInfo{
		file:  mode.Path(),
		label: mode.Path(),
	})
}

func runDiff(args []string) int {
	fs := flag.NewFlagSet("diff", flag.ExitOnError)
	shared := addSharedFlags(fs)
	scope := fs.String("scope", "working", "diff scope: working, branch, or all")
	base := fs.String("base", "", "base ref for branch/all scopes")
	stay := fs.Bool("stay", false, "serve until interrupted instead of blocking on handback")
	path, ok := parseWithTrailingFlags(fs, args)
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		return 1
	}
	if path == "" {
		path = "."
	}
	mode, err := diffmode.New(path, *base, *scope)
	if err != nil {
		return failEarly(shared, err)
	}
	return runReview(mode, shared, *stay, reviewInfo{
		root:   mode.Root(),
		label:  mode.Root(),
		export: mode.WriteExport,
	})
}

// reviewInfo carries the mode-specific bits of the CLI contract.
type reviewInfo struct {
	file   string // doc mode: reviewed file
	root   string // diff mode: repo root
	label  string
	export func(time.Time) (string, error) // written on handback
}

func failEarly(shared sharedFlags, err error) int {
	if shared.json != nil && *shared.json {
		b, _ := json.Marshal(event{Event: "error", Error: err.Error()})
		fmt.Println(string(b))
	} else {
		fmt.Fprintln(os.Stderr, "planman:", err)
	}
	return 1
}

// stayPorts is the range agents scan to discover a --stay server.
const stayPortFrom, stayPortTo = 7350, 7359

func runReview(mode server.Mode, shared sharedFlags, stay bool, info reviewInfo) int {
	jsonOut := *shared.json
	emit := func(e event) {
		if jsonOut {
			b, _ := json.Marshal(e)
			fmt.Println(string(b))
		}
	}
	fail := func(err error) int {
		if jsonOut {
			emit(event{Event: "error", Error: err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, "planman:", err)
		}
		return 1
	}

	srv, err := server.New(mode, stay)
	if err != nil {
		return fail(err)
	}
	initialTotal, _ := srv.CommentCounts()

	var url string
	var ln net.Listener
	if stay && *shared.port == 0 {
		ln, url, err = srv.ListenRange(stayPortFrom, stayPortTo)
	} else {
		ln, url, err = srv.Listen(*shared.port)
	}
	if err != nil {
		return fail(err)
	}
	go func() { _ = http.Serve(ln, srv.Handler()) }()

	emit(event{Event: "ready", File: info.file, Root: info.root, URL: url})
	if !jsonOut {
		fmt.Printf("planman: reviewing %s\nplanman: open %s", info.label, url)
		if stay {
			fmt.Printf(" (serving until interrupted)\n")
		} else {
			fmt.Printf(" (timeout %s)\n", *shared.timeout)
		}
	}
	if !*shared.noBrowser {
		if err := server.OpenBrowser(url); err != nil && !jsonOut {
			fmt.Fprintln(os.Stderr, "planman: could not open browser:", err)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	start := time.Now()

	final := func(name string) event {
		total, open := srv.CommentCounts()
		return event{
			Event:         name,
			File:          info.file,
			Root:          info.root,
			CommentsAdded: total - initialTotal,
			CommentsOpen:  open,
			CommentsTotal: total,
			DurationSecs:  time.Since(start).Round(time.Millisecond).Seconds(),
		}
	}

	if stay {
		<-sigCh
		if !jsonOut {
			fmt.Println("planman: interrupted")
		}
		return 0
	}

	select {
	case <-srv.Handback:
		e := final("handback")
		if info.export != nil {
			if path, err := info.export(time.Now()); err == nil {
				e.Export = path
			}
		}
		emit(e)
		if !jsonOut {
			fmt.Printf("planman: review handed back (%d comment(s) added, %d open)\n", e.CommentsAdded, e.CommentsOpen)
			if e.Export != "" {
				fmt.Printf("planman: export written to %s\n", e.Export)
			}
		}
		return 0
	case <-time.After(*shared.timeout):
		e := final("timeout")
		emit(e)
		if !jsonOut {
			fmt.Printf("planman: timed out after %s (%d comment(s) added)\n", *shared.timeout, e.CommentsAdded)
		}
		return 2
	case <-sigCh:
		e := final("interrupted")
		emit(e)
		if !jsonOut {
			fmt.Println("planman: interrupted")
		}
		return 130
	}
}
