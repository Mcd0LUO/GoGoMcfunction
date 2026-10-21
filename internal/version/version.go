// Package version holds the release identity of the compiler.
//
// Two version numbers matter here and they are not the same one.
//
//	Version      what this binary does
//	SpecVersion  what the language is
//
// The compiler implements a subset of the language. So the binary being
// at 0.1 does not mean the language is at 0.1, and the language being at
// 1.0 does not mean every sentence of the specification is implemented --
// several entries in its checklist are marked as planned.
//
// Printing both, together, is the only way to say that without a
// footnote. See docs/BASELINE.md for what each number currently covers.
package version

// Version is the compiler release, in semantic-versioning form.
//
// While the major number is 0 the language and the generated code may
// change in incompatible ways between minor releases; that is the point
// of staying below 1.0. Every released version is a baseline: a git tag,
// a golden output tree under testdata/golden, and a CHANGELOG entry.
const Version = "0.26.0"

// SpecVersion is the language specification this build targets.
const SpecVersion = "1.0"

// Target is the Minecraft version the generated code runs on.
const Target = "1.12.2"

// String renders the release identity in one line.
func String() string {
	return "ggm " + Version + " (language spec " + SpecVersion + ", target " + Target + ")"
}
