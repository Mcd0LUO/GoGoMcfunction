package version

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A version string that does not parse is worse than no version string,
// because tooling silently stops ordering releases correctly.
var semver = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?$`)

func TestVersionIsSemver(t *testing.T) {
	if !semver.MatchString(Version) {
		t.Fatalf("Version = %q is not a semantic version", Version)
	}
	// Rejecting is the half that makes the check mean anything: a pattern
	// that matches everything would pass the assertion above.
	for _, bad := range []string{"1.0", "1", "v1.0.0", "1.0.0.0", "01.0.0", "1.0.x", ""} {
		if semver.MatchString(bad) {
			t.Errorf("semver accepted %q", bad)
		}
	}
	for _, good := range []string{"0.0.0", "0.1.0", "1.2.3", "10.20.30", "1.0.0-rc.1"} {
		if !semver.MatchString(good) {
			t.Errorf("semver rejected %q", good)
		}
	}
}

// The changelog and the constant live in different files, so they drift.
// Failing here is much cheaper than discovering it from a release that
// claims a version it does not contain.
func TestChangelogTopEntryMatchesVersion(t *testing.T) {
	path := filepath.Join("..", "..", "CHANGELOG.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read changelog: %v", err)
	}
	heading := regexp.MustCompile(`(?m)^## \[([^\]]+)\]`)
	m := heading.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no version heading in %s", path)
	}
	if got := string(m[1]); got != Version {
		t.Fatalf("CHANGELOG newest entry is %q but Version is %q", got, Version)
	}
}

// The baseline document names the version it freezes, for the same reason.
func TestBaselineNamesThisVersion(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "BASELINE.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if !strings.Contains(string(body), "cmcf "+Version) {
		t.Fatalf("docs/BASELINE.md does not mention %q", "cmcf "+Version)
	}
}
