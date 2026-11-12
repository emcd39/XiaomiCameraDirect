//go:build !linux && !windows

package localmedia

import "syscall"

func BindSocketToInterface(_, _ string, _ syscall.RawConn) error {
	return ErrLocalInterfaceUnverified
}
