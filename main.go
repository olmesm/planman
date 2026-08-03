// planman is a self-contained local markdown review tool. `planman open
// file.md` serves the file in a browser UI for block-level commenting and
// blocks until the reviewer hands control back, the timeout elapses, or
// the process is interrupted. Comments persist inside the markdown file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/olmesm/planman/internal/server"
)

var version = "dev" // overridden by goreleaser via -ldflags

const usage = `planman — markdown reviews for coding agents

Usage:
  planman open <file.md> [flags]   Review a file; blocks until handback
  planman version                  Print version

Flags for open:
  --json           Emit machine-readable JSON events on stdout
  --timeout DUR    Give up after DUR (default 30m, e.g. 45m, 2h, 90s)
  --port N         Listen on a fixed port (default: ephemeral)
  --no-browser     Don't open the browser automatically

Exit codes: 0 review handed back · 2 timeout · 130 interrupted
`

type event struct {
	Event         string  `json:"event"`
	File          string  `json:"file,omitempty"`
	URL           string  `json:"url,omitempty"`
	CommentsAdded int     `json:"comments_added,omitempty"`
	CommentsTotal int     `json:"comments_total,omitempty"`
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
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(1)
	}
}

func runOpen(args []string) int {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "emit JSON events")
	timeout := fs.Duration("timeout", 30*time.Minute, "review timeout")
	port := fs.Int("port", 0, "port to listen on (0 = ephemeral)")
	noBrowser := fs.Bool("no-browser", false, "don't open the browser")
	_ = fs.Parse(args)
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 1
	}
	path := rest[0]
	if len(rest) > 1 { // allow flags after the file argument too
		_ = fs.Parse(rest[1:])
		if fs.NArg() > 0 {
			fmt.Fprint(os.Stderr, usage)
			return 1
		}
	}

	emit := func(e event) {
		if *jsonOut {
			b, _ := json.Marshal(e)
			fmt.Println(string(b))
		}
	}
	fail := func(err error) int {
		if *jsonOut {
			emit(event{Event: "error", Error: err.Error()})
		} else {
			fmt.Fprintln(os.Stderr, "planman:", err)
		}
		return 1
	}

	srv, err := server.New(path)
	if err != nil {
		return fail(err)
	}
	initialComments := srv.CommentCount()

	ln, url, err := srv.Listen(*port)
	if err != nil {
		return fail(err)
	}
	go func() { _ = http.Serve(ln, srv.Handler()) }()

	emit(event{Event: "ready", File: srv.Path, URL: url})
	if !*jsonOut {
		fmt.Printf("planman: reviewing %s\nplanman: open %s (timeout %s)\n", srv.Path, url, timeout)
	}
	if !*noBrowser {
		if err := server.OpenBrowser(url); err != nil && !*jsonOut {
			fmt.Fprintln(os.Stderr, "planman: could not open browser:", err)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	start := time.Now()

	final := func(name string) event {
		total := srv.CommentCount()
		return event{
			Event:         name,
			File:          srv.Path,
			CommentsAdded: total - initialComments,
			CommentsTotal: total,
			DurationSecs:  time.Since(start).Round(time.Millisecond).Seconds(),
		}
	}

	select {
	case <-srv.Handback:
		e := final("handback")
		emit(e)
		if !*jsonOut {
			fmt.Printf("planman: review handed back (%d comment(s) added)\n", e.CommentsAdded)
		}
		return 0
	case <-time.After(*timeout):
		e := final("timeout")
		emit(e)
		if !*jsonOut {
			fmt.Printf("planman: timed out after %s (%d comment(s) added)\n", timeout, e.CommentsAdded)
		}
		return 2
	case <-sigCh:
		e := final("interrupted")
		emit(e)
		if !*jsonOut {
			fmt.Println("planman: interrupted")
		}
		return 130
	}
}
