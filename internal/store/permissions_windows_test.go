//go:build windows

package store

import "golang.org/x/sys/windows"

func setInsecureStateAccess(path string) error {
	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	userSID := tokenUser.User.Sid.String()
	security, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + userSID + ")(A;;FR;;;WD)")
	if err != nil {
		return err
	}
	dacl, _, err := security.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}
