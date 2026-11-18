package provision

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// cloudbase-init through guestinfo (ADR-0039): its VMware GuestInfo
// service reads guestinfo.metadata and guestinfo.userdata as cloud-init's
// data source does, and the metadata's admin-username, admin-password and
// public-keys-data for the user it makes.

// adminUser is the user cloudbase-init makes or sets the password of.
type adminUser struct {
	username string
	password string
	keys     []string
}

// cloudbaseMetadata is cloud-init's metadata (cloudInitMetadata: host name,
// network version 2, which cloudbase-init parses too) with the user.
func cloudbaseMetadata(instanceID, hostname, domain string, nics []ciNIC, admin adminUser) ([]byte, error) {
	raw, err := cloudInitMetadata(instanceID, hostname, domain, nics)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if admin.username != "" {
		m["admin-username"] = admin.username
	}
	if admin.password != "" {
		m["admin-password"] = admin.password
	}
	if len(admin.keys) > 0 {
		m["public-keys-data"] = strings.Join(admin.keys, "\n")
	}
	return json.Marshal(m)
}

// rpctool is where VMware Tools puts the tool that sets guestinfo keys
// from the guest; cloudbase-init reads them with it.
const rpctool = `$env:ProgramFiles\VMware\VMware Tools\rpctool.exe`

// cloudbaseUserData is a Windows VM's user data: the template's and the
// item's, merged as for cloud-init (mergeUserData), and with a password,
// a last part that writes the metadata again without it (cleared).
// cloudbase-init, unlike cloud-init, leaves guestinfo as it found it, and
// the password would stay in the VM's configuration in vCenter. It keeps
// the metadata it loaded for the rest of its run, so the password is set
// after the part clears it.
func cloudbaseUserData(image, own string, cleared []byte) (string, error) {
	var parts []string
	if isCloudConfig(image) && isCloudConfig(own) {
		merged, err := mergeUserData(image, own)
		if err != nil {
			return "", err
		}
		parts = append(parts, merged)
	} else {
		for _, d := range []string{image, own} {
			if d != "" {
				parts = append(parts, d)
			}
		}
	}
	if cleared != nil {
		parts = append(parts, fmt.Sprintf("#ps1_sysnative\n& \"%s\" \"info-set guestinfo.metadata %s\"\n", rpctool,
			base64.StdEncoding.EncodeToString(cleared)))
	}
	if len(parts) < 2 {
		return strings.Join(parts, ""), nil
	}
	return mimeParts(parts, true)
}
