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
		{"known", "[wrapping]\nfn f() { }\n", true},
		{"typo", "[wraping]\nfn f() { }\n", false},
		{"unknown", "[pure]\nfn f() { }\n", false},
		{"dangling", "[wrapping]\n", false},
		{"class ok", "[selector(@e)] class C { }\n", true},
		{"class unknown", "[wrapping]\n[selector(@e)] class C { }\n", true},
		// Two attributes in ONE list, which is the whole point of the
		// merged syntax.
		{"merged list", "[wrapping, export]\nfn f() { }\n", true},
		{"merged with trigger", "[load, every(\"5s\")]\nfn f() { }\n", true},
		// A repeated attribute is almost always a typo: keeping the last
		// one would make [every("1s"), every("5s")] look like two schedules.
		{"duplicate", "[every(\"1s\"), every(\"5s\")]\nfn f() { }\n", false},
		// The period has to be a literal: it decides the dispatcher's name
		// and its counter objective, both of which are fixed at compile time.
		{"every needs a string", "[every(5s)]\nfn f() { }\n", false},
		{"every rejects sub-tick", "[every(\"100ms\")]\nfn f() { }\n", false},
		{"every accepts 1tick", "[every(\"1tick\")]\nfn f() { }\n", true},
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
