package main

import "testing"

func TestOverrideHostPort(t *testing.T) {
	cases := []struct {
		addr, host string
		port       int
		want       string
	}{
		{"", "", 18098, "http://127.0.0.1:18098"},
		{"", "riak.local", 0, "http://riak.local:8098"},
		{"10.0.0.5:8098", "", 9000, "http://10.0.0.5:9000"},
		{"https://proxy:8443/riak", "other", 0, "https://other:8443/riak"},
	}
	for _, c := range cases {
		got, err := overrideHostPort(c.addr, c.host, c.port)
		if err != nil || got != c.want {
			t.Errorf("overrideHostPort(%q, %q, %d) = %q, %v; want %q", c.addr, c.host, c.port, got, err, c.want)
		}
	}
}
