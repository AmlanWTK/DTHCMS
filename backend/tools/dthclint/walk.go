package main

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// walkGoFiles returns a WalkDirFunc that calls fn for every Go source file,
// skipping vendored code, generated output and testdata fixtures.
//
// testdata is skipped deliberately: it holds files that violate the rules on purpose,
// so that the checkers themselves can be tested. The Go toolchain ignores testdata for
// the same reason.
func walkGoFiles(fn func(path string) error) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "vendor", "testdata", "node_modules":
				return filepath.SkipDir
			}
			// Hidden directories are skipped — but "." and ".." are path elements, not
			// hidden directories. Treating ".." as hidden made a walk rooted at a
			// relative parent path skip everything and report success, which is the
			// worst failure mode a guardrail has: it does not break, it goes quiet.
			// Callers now pass absolute paths (RunPHI and RunArch resolve them), and
			// this stays as the second line of defence.
			if name := d.Name(); strings.HasPrefix(name, ".") && name != "." && name != ".." {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		return fn(path)
	}
}
