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
			if strings.HasPrefix(d.Name(), ".") && d.Name() != "." {
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
