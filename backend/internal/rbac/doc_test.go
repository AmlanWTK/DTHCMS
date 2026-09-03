package rbac_test

import (
	"os"
	"path/filepath"
)

// readDoc reads a file under the repository's docs directory.
func readDoc(name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", name))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
