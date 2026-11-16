//go:build !windows

package store

import "os"

func checkStatePermissions(_ string, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return ErrInsecureState
	}
	return nil
}

func secureStateFile(_ string) error {
	return nil
}
