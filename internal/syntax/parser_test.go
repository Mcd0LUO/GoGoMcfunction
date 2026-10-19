package syntax

import "testing"

// An attribute the compiler does not implement must be rejected.
//
// The parser used to collect attributes into a set and read exactly one
// of them, so every other name -- including a typo of the one that
// exists -- parsed successfully and did nothing.
func TestUnknownAttributeIsRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
		ok   bool
	}{
		{"known", "#[wrapping]\nfn f() { }\n", true},
		{"typo", "#[wraping]\nfn f() { }\n", false},
		{"unknown", "#[pure]\nfn f() { }\n", false},
		{"dangling", "#[wrapping]\n", false},
		{"class unknown", "#[wrapping]\n[selector(@e)] class C { }\n", false},
		{"class ok", "[selector(@e)] class C { }\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
