package vraimport

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/vra"
)

// Protection is whether vRA keeps a deployment's machine from deletion, and
// Doubt says why rvav isn't sure when it can't work the flag out.
type Protection struct {
	On    bool
	Doubt string
}

var (
	instanceIndex = regexp.MustCompile(`\[\d+\]$`)
	inputFlag     = regexp.MustCompile(`^\$\{\s*input\.([A-Za-z_][A-Za-z0-9_]*)\s*\}$`)
)

// Protected says whether vRA keeps a deployment's machine from deletion.
//
// preventDelete is a resource flag of the cloud template, written outside
// the resource's properties ("Automation Assembler resource flags for
// requests",
// https://techdocs.broadcom.com/us/en/vmware-cis/aria/aria-automation/all/resource-flags-for-requests.html).
// vRA's API reference gives no field for it on a deployment's resources
// (/deployment/api/deployments?expand=resources) nor on the IaaS API's
// machines (/iaas/api/machines). So rvav reads, in order:
//   - the resource's own preventDelete, beside its dependsOn, or in its
//     properties, should vRA give one;
//   - the flag on the machine's resource (its name without [n]) in the
//     template version the deployment was requested from, or the
//     template's current content when vRA names no version.
//
// A flag that reads an input takes the deployment's input. Any other
// expression keeps the machine from deletion, the safer side, with a doubt.
func Protected(e *vra.Estate, d vra.Deployment, res vra.Resource) Protection {
	if res.PreventDelete != nil {
		return Protection{On: *res.PreventDelete}
	}
	if v, ok := res.Properties["preventDelete"]; ok {
		return flagValue(v, d.Inputs)
	}
	content := templateOf(e, d)
	if content == "" {
		return Protection{}
	}
	var t tmpl
	if yaml.Unmarshal([]byte(content), &t) != nil {
		return Protection{}
	}
	r, ok := t.Resources[instanceIndex.ReplaceAllString(res.Name, "")]
	if !ok {
		return Protection{}
	}
	return flagValue(r.Flags["preventDelete"], d.Inputs)
}

// templateOf is the content of the template version a deployment was made
// from, else the template's own; "" when the estate hasn't the template.
func templateOf(e *vra.Estate, d vra.Deployment) string {
	i := slices.IndexFunc(e.Blueprints, func(b vra.Blueprint) bool { return b.ID == d.BlueprintID })
	if i < 0 {
		return ""
	}
	bp := e.Blueprints[i]
	if d.BlueprintVersion != "" {
		if j := slices.IndexFunc(bp.Versions, func(v vra.BlueprintVersion) bool { return v.Version == d.BlueprintVersion }); j >= 0 {
			return bp.Versions[j].Content
		}
	}
	return bp.Content
}

func flagValue(v any, inputs map[string]any) Protection {
	switch x := v.(type) {
	case nil:
		return Protection{}
	case bool:
		return Protection{On: x}
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true":
			return Protection{On: true}
		case "false", "":
			return Protection{}
		}
		if m := inputFlag.FindStringSubmatch(x); m != nil {
			if in, given := inputs[m[1]]; given {
				if p := flagValue(in, nil); p.Doubt == "" {
					return p
				}
			}
		}
	}
	return Protection{On: true, Doubt: fmt.Sprintf("preventDelete is %v, which rvav can't work out", v)}
}

// protectionNotes say which of a plan's VMs are kept from deletion, by vRA
// and by rvav.
func protectionNotes(p *DeploymentPlan, vraKeeps map[string]Protection, note func(level, where, text string)) {
	var kept, lost []string
	for _, vm := range p.VMs {
		pr := vraKeeps[vm.Name]
		if p.Status == Adopted && !vm.Managed {
			continue
		}
		if pr.Doubt != "" {
			note(Warning, "vms."+vm.Name, pr.Doubt+": kept from deletion, the safer side. An administrator can allow its deletion.")
		}
		switch {
		case !pr.On:
		case vm.PreventDelete:
			kept = append(kept, vm.Name)
		default:
			lost = append(lost, vm.Name)
		}
	}
	switch {
	case len(kept) > 0 && p.Status == Adopted:
		note(Info, "vms", "Kept from deletion, as in vRA: "+strings.Join(kept, ", ")+". An administrator runs the deployment's Allow deletion first.")
	case len(kept) > 0:
		note(Info, "vms", "vRA keeps "+strings.Join(kept, ", ")+" from deletion (preventDelete): rvav does too once adopted, until an "+
			"administrator runs the deployment's Allow deletion.")
	}
	if len(lost) > 0 {
		note(Warning, "vms", "vRA keeps "+strings.Join(lost, ", ")+" from deletion, rvav doesn't: an administrator allowed it, or rvav "+
			"adopted it before it kept vRA's protection.")
	}
}

// keepThere asks to keep from deletion those of the VMs adopted from
// Onboarding that vRA keeps.
func keepThere(names []string, vraKeeps map[string]Protection) string {
	var kept []string
	for _, n := range names {
		if vraKeeps[n].On {
			kept = append(kept, n)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return " vRA keeps " + strings.Join(kept, ", ") + " from deletion: choose Keep from deletion there."
}
