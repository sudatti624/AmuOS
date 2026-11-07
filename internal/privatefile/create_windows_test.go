package privatefile

import (
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretHasProtectedDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity")
	f, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("secret inherits parent permissions")
	}
	sddl := sd.String()
	if strings.Contains(sddl, ";;;WD)") || strings.Contains(sddl, ";;;BU)") || strings.Contains(sddl, ";;;AU)") {
		t.Fatalf("public ACL: %s", sddl)
	}
}
