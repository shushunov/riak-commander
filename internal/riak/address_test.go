package riak

import "testing"

func TestNormalizeAddress(t *testing.T) {
	cases := []struct {
		in, base, display string
	}{
		{"localhost", "http://localhost:8098", "localhost:8098"},
		{" 127.0.0.1:8098 ", "http://127.0.0.1:8098", "127.0.0.1:8098"},
		{"Riak.Example.com:18098", "http://riak.example.com:18098", "riak.example.com:18098"},
		{"http://10.0.0.5", "http://10.0.0.5:8098", "10.0.0.5:8098"},
		{"http://10.0.0.5:8098/", "http://10.0.0.5:8098", "10.0.0.5:8098"},
		{"https://riak.example.com", "https://riak.example.com:443", "https://riak.example.com:443"},
		{"https://proxy:8443/riak/", "https://proxy:8443/riak", "https://proxy:8443/riak"},
		{"proxy:8080/riak", "http://proxy:8080/riak", "proxy:8080/riak"},
		{"[::1]:8098", "http://[::1]:8098", "[::1]:8098"},
	}
	for _, c := range cases {
		base, display, err := NormalizeAddress(c.in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if base != c.base || display != c.display {
			t.Errorf("%q: got (%q, %q), want (%q, %q)", c.in, base, display, c.base, c.display)
		}
		// the display form must round-trip to the same base URL
		base2, display2, err := NormalizeAddress(display)
		if err != nil || base2 != base || display2 != display {
			t.Errorf("%q: display form %q does not round-trip: (%q, %q, %v)", c.in, display, base2, display2, err)
		}
	}
}

func TestNormalizeAddressRejects(t *testing.T) {
	for _, in := range []string{"", "   ", "ftp://host", "http://user:pw@host", "http://:8098", "host:8098/?x=1"} {
		if _, _, err := NormalizeAddress(in); err == nil {
			t.Errorf("%q: expected an error", in)
		}
	}
}
