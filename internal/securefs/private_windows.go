//go:build windows

package securefs

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func checkPrivatePath(path string) (PrivatePathStatus, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return PrivatePathStatus{}, err
	}
	control, _, err := sd.Control()
	if err != nil {
		return PrivatePathStatus{}, err
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		return PrivatePathStatus{
			Protected: false,
			Detail:    fmt.Sprintf("%s inherits Windows ACLs; move WHALE_HOME to a local NTFS directory or restrict the parent directory", path),
		}, nil
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return PrivatePathStatus{}, err
	}
	if dacl == nil || dacl.AceCount == 0 {
		return PrivatePathStatus{
			Protected: false,
			Detail:    fmt.Sprintf("%s has an empty Windows DACL", path),
		}, nil
	}
	return PrivatePathStatus{
		Protected: true,
		Detail:    fmt.Sprintf("%s has a protected Windows ACL", path),
	}, nil
}

func hardenPrivatePath(path string, isDir bool) error {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if isDir {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entries := []windows.EXPLICIT_ACCESS{
		explicitAccess(user.User.Sid, windows.TRUSTEE_IS_USER, inheritance),
		explicitAccess(systemSID, windows.TRUSTEE_IS_USER, inheritance),
		explicitAccess(adminSID, windows.TRUSTEE_IS_GROUP, inheritance),
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return err
	}
	status, err := checkPrivatePath(path)
	if err != nil {
		return err
	}
	if !status.Protected {
		return fmt.Errorf("%s", status.Detail)
	}
	return nil
}

func explicitAccess(sid *windows.SID, trusteeType windows.TRUSTEE_TYPE, inheritance uint32) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  trusteeType,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}
