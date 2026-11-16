//go:build !windows

package store

import "os"

func setInsecureStateAccess(path string) error {
	return os.Chmod(path, 0o644)
}
