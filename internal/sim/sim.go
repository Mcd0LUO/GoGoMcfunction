// Package sim runs generated output against the GoMcRuntime engine.
//
// Why a subprocess: GoMcRuntime keeps its implementation in `internal/`,
// which Go forbids importing across modules. Rather than restructure a
// working project, cmcf talks to it through its CLI contract — the same
// contract its own golden tests use.
//
// That boundary is also an asset: the harness exercises exactly what a user
// would run, including the 1.12.2 save layout and function loading rules.
package sim

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Runtime locates the gomc binary. Set CMCF_GOMC to override.
func Runtime() (string, error) {
	if p := os.Getenv("CMCF_GOMC"); p != "" {
		return p, nil
	}
	candidates := []string{
		`D:\code\go\GoMcRuntime\gomc.exe`,
		"gomc",
	}
	for _, c := range candidates {
		if filepath.IsAbs(c) {
			if _, err := os.Stat(c); err == nil {
				return c, nil
			}
			continue
		}
		if _, err := exec.LookPath(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("gomc runtime not found; set CMCF_GOMC to the gomc binary")
}

// World is a temporary 1.12.2 save populated with generated functions.
type World struct {
	Dir   string
	gomc  string
	files map[string]string
}

// NewWorld materialises generated files into a 1.12.2 save layout.
func NewWorld(files map[string]string) (*World, error) {
	gomc, err := Runtime()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "cmcf-world-")
	if err != nil {
		return nil, err
	}
	base := filepath.Join(dir, "data", "functions")
	for p, body := range files {
		if strings.Count(p, ":") != 1 {
			return nil, fmt.Errorf("malformed function path %q", p)
		}
		i := strings.Index(p, ":")
		ns, rel := p[:i], p[i+1:]
		dst := filepath.Join(base, ns, filepath.FromSlash(rel)+".mcfunction")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, []byte(body), 0o644); err != nil {
			return nil, err
		}
	}
	return &World{Dir: dir, gomc: gomc, files: files}, nil
}

// Close removes the temporary world.
func (w *World) Close() { os.RemoveAll(w.Dir) }

// State is the observable world after a run.
type State struct {
	// Scores maps "objective/holder" to value.
	Scores map[string]int
	// Output holds every command's output lines, in order.
	Output []string
}

// Run executes `setup` then `run` as a difftest case and returns the snapshot.
//
// A case file is the runtime's own format, so this stays a supported
// interface rather than a private one.
func (w *World) Run(setup, run []string) (*State, error) {
	casePath := filepath.Join(os.TempDir(), "cmcf-case-"+filepath.Base(w.Dir)+".difftest")
	defer os.Remove(casePath)
	var sb strings.Builder
	sb.WriteString("name: cmcf\nsetup:\n");
	for _, c := range setup {
		sb.WriteString("  " + c + "\n")
	}
	sb.WriteString("run:\n")
	for _, c := range run {
		sb.WriteString("  " + c + "\n")
	}
	if err := os.WriteFile(casePath, []byte(sb.String()), 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command(w.gomc, "difftest", "run", w.Dir, casePath, "--full")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("gomc difftest: %v\n%s", err, out)
	}
	return parseState(string(out)), nil
}

// parseState reads the `state:` block from a difftest observation.
func parseState(text string) *State {
	st := &State{Scores: map[string]int{}}
	lines := strings.Split(text, "\n")
	inState := false
	inScores := false
	for _, l := range lines {
		t := strings.TrimRight(l, "\r")
		trimmed := strings.TrimSpace(t)
		if trimmed == "state:" {
			inState = true
			continue
		}
		if !inState {
			if strings.HasPrefix(trimmed, "feedback: ") {
				st.Output = append(st.Output, strings.TrimPrefix(trimmed, "feedback: "))
			}
			continue
		}
		if trimmed == "scores:" {
			inScores = true
			continue
		}
		if trimmed == "entities:" || trimmed == "blocks:" {
			inScores = false
			continue
		}
		if !inScores || trimmed == "" {
			continue
		}
		// format: <objective>/<holder> = <value>
		eq := strings.LastIndex(trimmed, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eq])
		valStr := strings.TrimSpace(trimmed[eq+1:])
		v, err := strconv.Atoi(valStr)
		if err != nil {
			continue
		}
		st.Scores[key] = v
	}
	return st
}

// Get reads "objective/holder" from a snapshot.
func (s *State) Get(objective, holder string) int {
	return s.Scores[objective+"/"+holder]
}

// Keys lists score keys in sorted order, for stable assertions.
func (s *State) Keys() []string {
	out := make([]string, 0, len(s.Scores))
	for k := range s.Scores {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}