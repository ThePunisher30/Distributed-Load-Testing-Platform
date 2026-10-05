package handlers

import "testing"

// TestTargetAllowed covers the Phase 8 safety check: only hosts in the allowlist
// are permitted, the port is ignored, matching is case-insensitive, and anything
// unparseable or empty is denied (fail closed).
func TestTargetAllowed(t *testing.T) {
	h := &Handler{AllowedTargetHosts: map[string]bool{"target": true, "localhost": true}}

	cases := []struct {
		url  string
		want bool
	}{
		{"http://target:8081/fast", true},      // allowed host, port ignored
		{"http://localhost/", true},            // allowed, no port
		{"https://TARGET/path", true},          // case-insensitive
		{"http://example.com/", false},         // not on the list
		{"http://169.254.169.254/meta", false}, // SSRF target, denied
		{"", false},                            // empty -> deny
		{"://nope", false},                     // unparseable -> deny
	}
	for _, c := range cases {
		if got := h.targetAllowed(c.url); got != c.want {
			t.Errorf("targetAllowed(%q) = %v, want %v", c.url, got, c.want)
		}
	}

	// With an empty allowlist, everything is denied (deny by default).
	empty := &Handler{AllowedTargetHosts: map[string]bool{}}
	if empty.targetAllowed("http://target/") {
		t.Error("empty allowlist must deny all targets")
	}
}
