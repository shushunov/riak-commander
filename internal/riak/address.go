package riak

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// DefaultPort is Riak's default HTTP listener port.
const DefaultPort = "8098"

// NormalizeAddress turns user input into a canonical base URL and a short
// display form. Accepted inputs:
//
//	host                      → http://host:8098
//	host:port                 → http://host:port
//	http://host[:port][/path] → as given (port defaults to 8098)
//	https://host[:port][/path]→ as given (port defaults to 443)
//
// The display form drops the "http://" scheme so the common case stays
// short ("10.0.0.5:8098"); https and path prefixes are kept so the display
// form is itself a valid input that round-trips to the same base URL.
// Credentials embedded in the URL are rejected: they would end up in the
// server history file in plain text.
func NormalizeAddress(input string) (base, display string, err error) {
	s := strings.TrimSpace(input)
	if s == "" {
		return "", "", fmt.Errorf("address is empty")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", "", fmt.Errorf("invalid address %q: %v", input, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("invalid address %q: scheme must be http or https", input)
	}
	if u.User != nil {
		return "", "", fmt.Errorf("invalid address %q: credentials in the address are not supported", input)
	}
	if u.Hostname() == "" {
		return "", "", fmt.Errorf("invalid address %q: missing host", input)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", "", fmt.Errorf("invalid address %q: query strings are not supported", input)
	}
	port := u.Port()
	if port == "" {
		port = DefaultPort
		if u.Scheme == "https" {
			port = "443"
		}
	}
	host := net.JoinHostPort(strings.ToLower(u.Hostname()), port)
	path := strings.TrimRight(u.EscapedPath(), "/")

	base = u.Scheme + "://" + host + path
	display = host + path
	if u.Scheme == "https" {
		display = base
	}
	return base, display, nil
}
