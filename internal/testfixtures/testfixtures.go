// Package testfixtures locates shared cross-SDK parity fixtures for tests.
package testfixtures

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReadFile reads name from the first available fixture directory. The search
// supports both this standalone repository and the Keelson monorepo checkout.
func ReadFile(name string) ([]byte, error) {
	dirs := make([]string, 0, 3)
	if dir := strings.TrimSpace(os.Getenv("KEELSON_SDK_FIXTURES_DIR")); dir != "" {
		dirs = append(dirs, dir)
	}
	dirs = append(dirs, "../fixtures/parity", "../../fixtures/parity")

	paths := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		path := filepath.Join(dir, name)
		paths = append(paths, path)
		data, err := os.ReadFile(path)
		if err == nil {
			return data, nil
		}
	}

	return nil, fmt.Errorf("fixture %q not found; tried %s", name, strings.Join(paths, ", "))
}
