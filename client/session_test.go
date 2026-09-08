// client/session_test.go
package main

import "testing"

func TestBuildWSURL(t *testing.T) {
	cases := []struct{ host string; port int; want string }{
		{"localhost", 8080, "ws://localhost:8080/ws"},
		{"192.168.1.5", 80, "ws://192.168.1.5:80/ws"},
	}
	for _, c := range cases {
		if got := buildWSURL(c.host, c.port); got != c.want {
			t.Errorf("buildWSURL(%q,%d) = %q, want %q", c.host, c.port, got, c.want)
		}
	}
}
