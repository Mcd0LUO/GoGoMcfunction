// Command cmcf compiles CMCF source to Minecraft 1.12.2 mcfunction files.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cmcf/internal/diag"
	"cmcf/internal/driver"
	"cmcf/internal/optimize"
	"cmcf/internal/target"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cmcf: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		outDir   = flag.String("out", "data/functions", "output directory for .mcfunction files")
		check    = flag.Bool("check", false, "check only; produce no files")
		quiet    = flag.Bool("q", false, "suppress per-file listing")
		tgtName  = flag.String("target", target.V1_12_2.Name, "Minecraft version to target")
		levelStr = flag.String("O", "2", "optimisation level: 0, 1, 2 or 3")
		showSkip = flag.Bool("v", false, "explain which optimisations the level omits")
	)
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: cmcf [flags] <file.cmcf> ...")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		return fmt.Errorf("no input files")
	}
	level, ok := optimize.ParseLevel(*levelStr)
	if !ok {
		return fmt.Errorf("invalid optimisation level %q", *levelStr)
	}
	if *tgtName != target.V1_12_2.Name {
		return fmt.Errorf("unknown target %q (only %s is implemented)", *tgtName, target.V1_12_2.Name)
	}

	failed := false
	for _, in := range flag.Args() {
		src, err := os.ReadFile(in)
		if err != nil {
			return err
		}
		out, diags := driver.Compile(string(src), in, driver.Options{
			Target: target.V1_12_2,
			Level:  level,
		})
		for _, d := range diags {
			fmt.Fprintf(os.Stderr, "%s:%s\n", in, d.String())
			if d.Severity == diag.Error {
				failed = true
			}
		}
		if *showSkip && out != nil {
			for _, sk := range out.Skipped {
				fmt.Printf("skipped at -O%d: %s\n", int(level), sk)
			}
		}
		if *check || out == nil || failed {
			continue
		}
		if err := write(out, *outDir); err != nil {
			return err
		}
		if !*quiet {
			paths := make([]string, 0, len(out.Files))
			for p := range out.Files {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			for _, p := range paths {
				fmt.Printf("%s  (%d lines)\n", p, strings.Count(out.Files[p], "\n"))
			}
			fmt.Printf("total: %d files, %d lines\n", len(out.Files), out.CountLines())
		}
	}
	if failed {
		return fmt.Errorf("compilation failed")
	}
	return nil
}

// write lays files out as <out>/<ns>/<path>.mcfunction, matching the 1.12.2
// save layout where functions live in the world save rather than a data pack.
func write(out *driver.Output, outDir string) error {
	paths := make([]string, 0, len(out.Files))
	for p := range out.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if strings.Count(p, ":") != 1 {
			return fmt.Errorf("internal: malformed function path %q", p)
		}
		ns, rel := splitPath(p)
		dst := filepath.Join(outDir, ns, filepath.FromSlash(rel)+".mcfunction")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(out.Files[p]), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func splitPath(p string) (string, string) {
	if i := strings.Index(p, ":"); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "minecraft", p
}