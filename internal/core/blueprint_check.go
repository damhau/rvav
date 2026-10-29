package core

import (
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/naming"
	"github.com/damhau/rvav/internal/policy"
)

// VM blueprints with components, disks, adapters, tags and guests (ADR-0024).

var componentKey = regexp.MustCompile(`^[a-z][a-z0-9]{0,15}$`)

// checkVMBlueprint checks a vm blueprint on its own; the secrets it names
// are checked when it is published (checkBlueprintRefs).
func checkVMBlueprint(b blueprint.Blueprint, ptr string, publish bool) []api.FieldError {
	var errs []api.FieldError
	add := func(at, code, msg string) { errs = append(errs, fieldErr(at, code, msg)) }
	cel := func(at, expr, what string) {
		if err := policy.Check(expr); err != nil {
			add(at, "invalid", what+" is not valid CEL: "+err.Error())
		}
	}
	bindings := func(at string, m map[string]string) {
		for _, k := range sortedKeys(m) {
			cel(at+"/"+k, m[k], "This binding")
		}
	}
	bindings(ptr+"/bindings", b.Bindings)
	for _, k := range sortedKeys(b.AWXTemplates) {
		if b.AWXTemplates[k].JobTemplate == "" && publish {
			add(ptr+"/awx_templates/"+k+"/job_template", "required", "Name the AWX template.")
		}
	}
	checkNaming := func(at, raw string) {
		if raw == "" {
			return
		}
		if _, err := naming.Parse(raw); err != nil {
			add(at, "invalid", "A naming template ends with {seq} or {seq:03}, for example {project}-{environment}-{component}{seq:02}.")
		}
	}
	checkNaming(ptr+"/naming", b.Naming)
	if b.DeploymentName != "" {
		if !b.Multi() {
			add(ptr+"/deployment_name", "invalid", "Only a blueprint with components names its deployment: a single VM's is its name.")
		} else if _, err := naming.ParseLabel(b.DeploymentName); err != nil {
			add(ptr+"/deployment_name", "invalid", "A deployment name has no {seq}, for example {project}-{environment}-{inputs.app}.")
		}
	}
	errs = append(errs, checkParts(ptr, b.Disks, b.NICs, b.Tags)...)

	keys := map[string]bool{}
	total := 0
	resolved := b.ComponentList()
	for i, c := range b.Components {
		at := fmt.Sprintf("%s/components/%d", ptr, i)
		switch {
		case !componentKey.MatchString(c.Key):
			add(at+"/key", "pattern", "A key of up to 16 lower-case letters and digits, starting with a letter: it goes into VM names.")
		case keys[c.Key]:
			add(at+"/key", "duplicate", "Another component has the key "+c.Key+".")
		}
		keys[c.Key] = true
		literal := -1
		switch n := c.Count.(type) {
		case nil:
			literal = 1
		case float64:
			if n != float64(int(n)) || n < 0 || n > blueprint.MaxVMs {
				add(at+"/count", "out_of_range", fmt.Sprintf("A count from 0 to %d.", blueprint.MaxVMs))
			} else {
				literal = int(n)
			}
		case string:
			cel(at+"/count", n, "The count")
		default:
			add(at+"/count", "invalid", "A number, or CEL over the inputs (inputs.web_nodes).")
		}
		if literal >= 0 {
			total += literal
		}
		if c.AntiAffinity && literal >= 0 && literal < 2 {
			add(at+"/anti_affinity", "invalid", "Anti-affinity keeps several VMs apart: give the component a count of two or more.")
		}
		checkNaming(at+"/naming", c.Naming)
		bindings(at+"/bindings", c.Bindings)
		errs = append(errs, checkParts(at, c.Disks, c.NICs, c.Tags)...)
	}
	if total > blueprint.MaxVMs {
		add(ptr+"/components", "out_of_range", fmt.Sprintf("A request builds at most %d VMs; these components have %d.", blueprint.MaxVMs, total))
	}

	for i, c := range resolved {
		at := ptr
		if b.Multi() {
			at = fmt.Sprintf("%s/components/%d", ptr, i)
		}
		if publish && c.Template == "" && c.Bindings["template"] == "" {
			what := "the vCenter template its VMs are cloned from"
			if c.Library != "" {
				what = "the item of content library " + c.Library + " its VMs are deployed from"
			}
			add(at+"/template", "required", "Name "+what+", or take it from an input (bindings.template, for example inputs.template with rvav.templates).")
		}
		errs = append(errs, checkGuest(at+"/guest", *c.Guest, publish)...)
	}
	return errs
}

// checkParts checks added disks and adapters, and fixed tags.
func checkParts(ptr string, disks []blueprint.Disk, nics []blueprint.NIC, tags []string) []api.FieldError {
	var errs []api.FieldError
	add := func(at, code, msg string) { errs = append(errs, fieldErr(at, code, msg)) }
	for i, d := range disks {
		at := fmt.Sprintf("%s/disks/%d", ptr, i)
		switch n := d.SizeGB.(type) {
		case float64:
			if n != float64(int(n)) || n < 1 || n > 65536 {
				add(at+"/size_gb", "out_of_range", "A size from 1 to 65536 GB.")
			}
		case string:
			if err := policy.Check(n); err != nil {
				add(at+"/size_gb", "invalid", "The size is not valid CEL: "+err.Error())
			}
		default:
			add(at+"/size_gb", "required", "Give the disk's size in GB, or CEL over the inputs.")
		}
		if d.Datastore != "" {
			if err := policy.Check(d.Datastore); err != nil {
				add(at+"/datastore", "invalid", "The datastore is not valid CEL: "+err.Error()+` (a name is quoted: '"vsan-01"').`)
			}
		}
	}
	keys := map[string]bool{}
	for i, n := range nics {
		at := fmt.Sprintf("%s/nics/%d", ptr, i)
		key := blueprint.NICKey(n, i)
		switch {
		case !componentKey.MatchString(key):
			add(at+"/key", "pattern", "A key of up to 16 lower-case letters and digits, starting with a letter: it goes into DNS names.")
		case keys[key]:
			add(at+"/key", "duplicate", "Another adapter has the key "+key+".")
		}
		keys[key] = true
		if strings.TrimSpace(n.Network) == "" {
			add(at+"/network", "required", "Give the adapter's network, as CEL (inputs.backup_network, or a quoted name).")
		} else if err := policy.Check(n.Network); err != nil {
			add(at+"/network", "invalid", "The network is not valid CEL: "+err.Error()+` (a name is quoted: '"dvpg-backup-vl900"').`)
		}
		switch n.IPAM {
		case "", blueprint.IPAMEfficientIP, blueprint.IPAMDHCP:
		default:
			add(at+"/ipam", "enum", "efficientip (an address and DNS records) or dhcp.")
		}
	}
	for i, t := range tags {
		if cat, tag, ok := strings.Cut(t, "/"); !ok || strings.TrimSpace(cat) == "" || strings.TrimSpace(tag) == "" || strings.Contains(tag, "/") {
			add(fmt.Sprintf("%s/tags/%d", ptr, i), "invalid", "A vSphere tag as category/tag, for example backup/daily.")
		}
	}
	return errs
}

// checkGuest checks a component's guest, as it inherits it.
func checkGuest(ptr string, g blueprint.Guest, publish bool) []api.FieldError {
	var errs []api.FieldError
	add := func(at, code, msg string) { errs = append(errs, fieldErr(at, code, msg)) }
	switch g.OSFamily {
	case "linux", "windows":
	case "":
		if publish {
			add(ptr+"/os_family", "required", "linux or windows.")
		}
	default:
		add(ptr+"/os_family", "enum", "linux or windows.")
	}
	windows := g.OSFamily == "windows"
	if j := g.DomainJoin; j != nil {
		if !windows {
			add(ptr+"/domain_join", "invalid", "Only Windows VMs join a domain here; Linux ones do it in post-configuration.")
		}
		if strings.TrimSpace(j.Domain) == "" {
			add(ptr+"/domain_join/domain", "required", "Name the domain to join, for example corp.example.")
		}
		if strings.TrimSpace(j.Credential) == "" {
			add(ptr+"/domain_join/credential", "required", "Name the basic secret with the account that joins VMs to the domain.")
		}
	}
	if g.AdminPassword != "" && !windows {
		add(ptr+"/admin_password", "invalid", "Only Windows VMs get a local administrator password here.")
	}
	if ci := g.CloudInit; ci != nil {
		if windows {
			add(ptr+"/cloud_init", "invalid", "cloud-init is for Linux VMs.")
		}
		if strings.TrimSpace(ci.UserData) == "" {
			add(ptr+"/cloud_init/user_data", "required", "Give the user data, for example a #cloud-config document.")
		} else if _, err := template.New("user_data").Option("missingkey=error").Parse(ci.UserData); err != nil {
			add(ptr+"/cloud_init/user_data", "invalid", "The user data is not a valid template: "+err.Error())
		}
		if g.CustomizationSpec != "" {
			add(ptr+"/customization_spec", "invalid", "A VM configured by cloud-init doesn't use a customization specification.")
		}
	}
	return errs
}

// blueprintSecrets lists the secrets a vm blueprint names, by where, and
// the kind each must be.
func blueprintSecrets(b blueprint.Blueprint, ptr string) map[string][2]string {
	out := map[string][2]string{}
	for i, c := range b.ComponentList() {
		at := ptr + "/guest"
		if b.Multi() {
			at = fmt.Sprintf("%s/components/%d/guest", ptr, i)
		}
		if c.Guest.AdminPassword != "" {
			out[at+"/admin_password"] = [2]string{c.Guest.AdminPassword, ""}
		}
		if j := c.Guest.DomainJoin; j != nil && j.Credential != "" {
			out[at+"/domain_join/credential"] = [2]string{j.Credential, "basic"}
		}
	}
	return out
}
