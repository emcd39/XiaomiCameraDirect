//go:build windows

package store

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows does not enforce POSIX mode bits. Inspect the file DACL instead and
// accept grants only to the current account, LocalSystem, or local admins.
func checkStatePermissions(path string, _ os.FileInfo) error {
	security, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return ErrInsecureState
	}

	owner, _, err := security.Owner()
	if err != nil || owner == nil {
		return ErrInsecureState
	}
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || tokenUser == nil || tokenUser.User.Sid == nil {
		return ErrInsecureState
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return ErrInsecureState
	}
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return ErrInsecureState
	}
	trusted := []*windows.SID{tokenUser.User.Sid, system, admins}
	if !matchesTrustedSID(owner, trusted) {
		return ErrInsecureState
	}

	dacl, _, err := security.DACL()
	if err != nil || dacl == nil || dacl.AceCount == 0 {
		return ErrInsecureState
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil || ace == nil {
			return ErrInsecureState
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			if ace.Mask == 0 {
				continue
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !matchesTrustedSID(sid, trusted) {
				return ErrInsecureState
			}
		default:
			// Object/callback ACEs are not expected on this local state file.
			// Refuse to guess whether an unfamiliar grant widens access.
			return ErrInsecureState
		}
	}
	return nil
}

// secureStateFile removes inherited grants from the atomically written state
// file. The parent directory can have broader access without widening access
// to the runtime's selected-camera state.
func secureStateFile(path string) error {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || tokenUser == nil || tokenUser.User.Sid == nil {
		return ErrInsecureState
	}
	userSID := tokenUser.User.Sid.String()
	security, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + userSID + ")")
	if err != nil {
		return ErrInsecureState
	}
	dacl, _, err := security.DACL()
	if err != nil || dacl == nil {
		return ErrInsecureState
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}

func matchesTrustedSID(candidate *windows.SID, trusted []*windows.SID) bool {
	if candidate == nil {
		return false
	}
	for _, sid := range trusted {
		if sid != nil && windows.EqualSid(candidate, sid) {
			return true
		}
	}
	return false
}
