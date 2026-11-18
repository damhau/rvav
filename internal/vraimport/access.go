package vraimport

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/damhau/rvav/internal/blueprint"
)

// mapRemoteAccess maps a machine's remoteAccess to its guest's
// remote_access (ADR-0039): a public key given, a key pair made for each
// deployment, or a user name and password. What rvav can't take leaves
// remote access out, and is said.
func mapRemoteAccess(props, fields map[string]any, windows bool, where string, c map[string]any, note func(level, where, text string)) {
	raw, ok := props["remoteAccess"]
	if !ok {
		return
	}
	where += ".remoteAccess"
	ra, ok := raw.(map[string]any)
	if !ok {
		note(Unmapped, where, "Not a map of authentication, username and the key or password. Left out.")
		return
	}
	auth, _ := ra["authentication"].(string)
	kind := map[string]string{"publicPrivateKey": blueprint.AccessSSHKey, "generatedPublicPrivateKey": blueprint.AccessGeneratedKey,
		"usernamePassword": blueprint.AccessPassword}[auth]
	if kind == "" {
		note(Unmapped, where+".authentication", fmt.Sprintf("Authentication %v: rvav gives a user a public key, a key pair it makes "+
			"for each deployment, or a password. Left out.", ra["authentication"]))
		return
	}
	osFamily := "linux"
	if windows {
		osFamily = "windows"
	}
	user, _ := ra["username"].(string)
	switch msg := blueprint.CheckUsername(osFamily, user); {
	case user == "":
		note(Unmapped, where+".username", "No user name: rvav's remote access names the user people sign in as. Left out.")
		return
	case strings.Contains(user, "${"):
		note(Unmapped, where+".username", "The user name comes from "+user+": rvav's is written in the blueprint. Left out: set guest.remote_access by hand.")
		return
	case msg != "":
		note(Unmapped, where+".username", fmt.Sprintf("User %s: %s Left out.", user, msg))
		return
	}
	out := map[string]any{"kind": kind, "username": user}
	switch kind {
	case blueprint.AccessSSHKey:
		key, _ := ra["sshKey"].(string)
		if name := inputRef(key); name != "" && fields[name] != nil {
			out["public_key"] = "${inputs." + name + "}"
		} else if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key)); err == nil && !strings.Contains(key, "${") {
			out["public_key"] = strings.TrimSpace(key)
		} else {
			note(Unmapped, where+".sshKey", "rvav takes the public key itself, or an input that holds it. Left out.")
			return
		}
	case blueprint.AccessPassword:
		pw, _ := ra["password"].(string)
		name := inputRef(pw)
		if f, _ := fields[name].(map[string]any); name == "" || f["secret"] != true {
			note(Unmapped, where+".password", "rvav reads the password from a secret input (encrypted in vRA) or a stored secret, "+
				"never from text kept with the request. Left out: store it as a secret, and name it in guest.remote_access.secret.")
			return
		}
		out["password"] = "${inputs." + name + "}"
	}
	g, _ := c["guest"].(map[string]any)
	if spec, _ := g["customization_spec"].(string); spec != "" &&
		(!windows || kind != blueprint.AccessPassword || !strings.EqualFold(user, blueprint.WindowsAdministrator)) {
		note(Unmapped, where, "The customization specification "+spec+" is kept, and rvav gives this user through cloud-init "+
			"(cloudbase-init on Windows), which doesn't use one. Left out.")
		return
	}
	if g == nil {
		g = map[string]any{}
	}
	g["remote_access"] = out
	c["guest"] = g
	note(Info, where, fmt.Sprintf("People sign in as %s with %s (ADR-0039).", user,
		map[string]string{blueprint.AccessSSHKey: "the public key given", blueprint.AccessGeneratedKey: "a key pair rvav makes for each deployment",
			blueprint.AccessPassword: "the password given"}[kind]))
	if windows && (kind != blueprint.AccessPassword || !strings.EqualFold(user, blueprint.WindowsAdministrator)) {
		note(Warning, where, "On Windows, a key or a user other than Administrator needs a template with cloudbase-init "+
			"(Administration › Infrastructure › Templates).")
	}
}

// mapAddressing maps how a machine's adapters get their addresses
// (ADR-0039): DHCP (assignment: dynamic) and an IPv6 address
// (assignIPv6Address) on the first. vRA's other adapter settings are left
// out, and said.
func mapAddressing(props map[string]any, segments map[string]string, where string, c map[string]any, note func(level, where, text string)) {
	nets, _ := props["networks"].([]any)
	for i, x := range nets {
		n, _ := x.(map[string]any)
		at := fmt.Sprintf("%s.networks[%d]", where, i)
		_, onSegment := segments[resourceRef(fmt.Sprint(n["network"]))]
		for _, k := range slices.Sorted(maps.Keys(n)) {
			v := n[k]
			switch k {
			case "network", "securityGroups", "deviceIndex", "assignIPv6Address":
			case "assignment":
				switch {
				case v == "static":
				case v != "dynamic":
					note(Unmapped, at+".assignment", fmt.Sprintf("Assignment %v: rvav takes static or dynamic. Left out: a static address from EfficientIP.", v))
				case onSegment:
					note(Unmapped, at+".assignment", "A segment made for the deployment has no DHCP: its VMs get their addresses from EfficientIP.")
				case i == 0:
					c["ipam"] = blueprint.IPAMDHCP
					note(Info, at+".assignment", "DHCP: the first adapter takes its address from the network's DHCP server; "+
						"rvav reserves no address and makes no DNS records for it (ADR-0039).")
				default:
					note(Info, at+".assignment", fmt.Sprintf("Adapter %d takes its address from DHCP: give it ipam: dhcp when you add it to nics.", i+1))
				}
			case "address":
				note(Unmapped, at+".address", fmt.Sprintf("A fixed address, %v: rvav reserves each address in EfficientIP. Left out.", v))
			default:
				note(Unmapped, at+"."+k, k+" has no rvav equivalent. Left out.")
			}
		}
		v, ok := n["assignIPv6Address"]
		switch {
		case !ok || v == false:
		case v != true:
			note(Unmapped, at+".assignIPv6Address", fmt.Sprintf("assignIPv6Address %v: rvav takes true or false. Left out.", v))
		case i > 0:
			note(Unmapped, at+".assignIPv6Address", "rvav gives an IPv6 address to the first adapter only. Left out.")
		case onSegment:
			note(Unmapped, at+".assignIPv6Address", "A segment made for the deployment has IPv4 only. Left out.")
		case c["ipam"] == blueprint.IPAMDHCP:
			note(Unmapped, at+".assignIPv6Address", "rvav reserves the IPv6 address in EfficientIP beside the IPv4 one, not with DHCP. Left out.")
		default:
			c["ipv6"] = true
			note(Info, at+".assignIPv6Address", "An IPv6 address beside the IPv4 one, with an AAAA record: the network needs an IPv6 subnet "+
				"(Administration › Infrastructure › Networks) (ADR-0039).")
		}
	}
}
