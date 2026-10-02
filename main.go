// Riak Commander is a Midnight Commander-style terminal UI for browsing and
// safely editing data in a Riak KV cluster over its HTTP API.
//
// Usage:
//
//	riak-commander [flags] [address]
//
// The address may be host, host:port or an http(s):// URL. Without one, the
// most recently used server is reconnected; on first run a server picker
// opens. Run "riak-commander -h" for flags, and press F1 or ? inside the app
// for help.
package main

import (
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/shushunov/riak-commander/internal/history"
	"github.com/shushunov/riak-commander/internal/riak"
	"github.com/shushunov/riak-commander/internal/ui"
)

// Set by the release build via -ldflags "-X main.version=… -X main.commit=…".
var (
	version = ""
	commit  = ""
)

func main() {
	os.Exit(run())
}

func run() int {
	fs := flag.NewFlagSet("riak-commander", flag.ContinueOnError)
	host := fs.String("host", "", "Riak host (overrides the host part of the address)")
	port := fs.Int("port", 0, "Riak HTTP port (overrides the port part of the address)")
	maxKeys := fs.Int("max-keys", 1000, "key-listing page size; listings stream and stop here")
	timeout := fs.Duration("timeout", 15*time.Second, "per-request timeout (key listings get 4x)")
	types := fs.String("types", "", "comma-separated bucket types to always list, e.g. sessions,carts")
	theme := fs.String("theme", os.Getenv("RIAK_COMMANDER_THEME"), "colour theme: "+strings.Join(ui.ThemeNames, ", ")+" (default dark; mono if NO_COLOR is set)")
	noHistory := fs.Bool("no-history", false, "do not read or write the server history")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, `Riak Commander %s: a terminal UI for browsing and editing Riak KV over HTTP.

Usage:
  riak-commander [flags] [address]

The address may be host, host:port (default port 8098) or an http(s):// URL,
e.g. 10.0.0.5, riak.local:8098, https://riak.example.com/riak.
Without an address the most recently used server is reconnected; on the
first run a server picker opens. Press F1 or ? in the app for help.

Flags:
`, versionString())
		fs.PrintDefaults()
		fmt.Fprintf(out, `
Environment:
  RIAK_COMMANDER_THEME  default for --theme
  NO_COLOR              when set (and --theme is not), use the mono theme
  XDG_CONFIG_HOME       base directory of the server history file
`)
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Println("riak-commander", versionString())
		return 0
	}

	th, err := ui.ThemeByName(*theme)
	if err != nil {
		fmt.Fprintln(os.Stderr, "riak-commander:", err)
		return 2
	}
	if *maxKeys < 1 {
		fmt.Fprintln(os.Stderr, "riak-commander: --max-keys must be at least 1")
		return 2
	}

	hist := history.Disabled()
	if !*noHistory {
		path, err := history.DefaultPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, "riak-commander: server history disabled:", err)
		} else if hist, err = history.Load(path); err != nil {
			fmt.Fprintln(os.Stderr, "riak-commander: warning:", err)
		}
	}

	var address string
	switch fs.NArg() {
	case 0:
		if last, ok := hist.Last(); ok {
			address = last.Address
		}
	case 1:
		address = fs.Arg(0)
	default:
		fs.Usage()
		return 2
	}
	if *host != "" || *port != 0 {
		if address, err = overrideHostPort(address, *host, *port); err != nil {
			fmt.Fprintln(os.Stderr, "riak-commander:", err)
			return 2
		}
	}
	if address != "" {
		if _, _, err := riak.NormalizeAddress(address); err != nil {
			fmt.Fprintln(os.Stderr, "riak-commander:", err)
			return 2
		}
	}

	var extraTypes []string
	for _, t := range strings.Split(*types, ",") {
		if t = strings.TrimSpace(t); t != "" {
			extraTypes = append(extraTypes, t)
		}
	}

	app := ui.New(address, ui.Options{
		MaxKeys:    *maxKeys,
		Timeout:    *timeout,
		Theme:      th,
		History:    hist,
		ExtraTypes: extraTypes,
		Version:    versionString(),
	})
	if err := app.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "riak-commander:", err)
		return 1
	}
	return 0
}

// overrideHostPort applies --host/--port to an address (which may be empty,
// meaning 127.0.0.1:8098). Scheme and path prefix are kept.
func overrideHostPort(address, host string, port int) (string, error) {
	if address == "" {
		address = "127.0.0.1"
	}
	base, _, err := riak.NormalizeAddress(address)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	h, p := u.Hostname(), u.Port()
	if host != "" {
		h = host
	}
	if port != 0 {
		p = strconv.Itoa(port)
	}
	u.Host = net.JoinHostPort(h, p)
	return u.String(), nil
}

func versionString() string {
	v := version
	if v == "" {
		v = "dev"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
	}
	if commit != "" {
		v += " (" + commit + ")"
	}
	return v
}
