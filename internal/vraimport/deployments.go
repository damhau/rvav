package vraimport

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// Deployment plan statuses.
const (
	Adopt   = "adopt"   // rvav will adopt it
	Adopted = "adopted" // rvav manages it already
	Failed  = "failed"  // adopting it was refused
)

// VMRef is a VM of rvav's inventory.
type VMRef struct {
	VCenter string `json:"vcenter"`
	Moref   string `json:"moref"`
	Name    string `json:"name"`
	// Managed: an rvav deployment has it already.
	Managed bool `json:"managed,omitempty"`
	// Ambiguous: more than one vCenter has a VM of this name, and which
	// vRA built isn't known by name alone.
	Ambiguous bool `json:"ambiguous,omitempty"`
}

// RvavProject is what planning needs of an rvav project.
type RvavProject struct {
	ID           string
	Name         string
	Environments []string
	// Members are the users who may own deployments (not viewers).
	Members []string
}

// Lease is an rvav lease policy's limits for a project's environment.
type Lease struct {
	Default, Max *int
}

// Rvav is what rvav has, for planning: its projects, the vRA projects
// mapped to them, its users by email, the VMs of its inventory by name,
// its catalog items by slug.
type Rvav struct {
	Projects []RvavProject
	// ProjectMap: vRA project ID → rvav project ID, "" for one an
	// administrator keeps in vRA.
	ProjectMap map[string]string
	Users      map[string]string // lower-case email → user ID
	VMs        map[string]VMRef  // name → VM
	Items      map[string]string // slug → item ID
	// TemplateItems are the items imported from vRA's templates: template
	// ID → item ID (vra_links).
	TemplateItems map[string]string
	// ItemStates are the items' states, as the import leaves them; nil when
	// unknown.
	ItemStates map[string]ItemState
	// Leases: "projectID/environment" → its lease policy's limits.
	Leases map[string]Lease
	// Adopted: vRA deployment ID → the rvav deployments it became
	// (vra_links), not deleted.
	Adopted map[string][]string
	Now     time.Time
}

// DeploymentPlan is what becomes of a vRA deployment: adopted into an rvav
// deployment of its name, or left in vRA with the reason.
type DeploymentPlan struct {
	VRAID      string `json:"vra_id"`
	Name       string `json:"name"`
	VRAProject string `json:"vra_project"`
	Owner      string `json:"owner"`
	Status     string `json:"status"`
	Notes      []Note `json:"notes"`
	// RvavName is its name in rvav: vRA's, made valid for rvav.
	RvavName      string   `json:"rvav_name,omitempty"`
	ProjectID     string   `json:"project_id,omitempty"`
	Environment   string   `json:"environment,omitempty"`
	OwnerID       string   `json:"owner_id,omitempty"`
	CatalogItemID string   `json:"catalog_item_id,omitempty"`
	LeaseDays     *int     `json:"lease_days,omitempty"`
	VMs           []VMRef  `json:"vms,omitempty"`
	Deployments   []string `json:"deployments,omitempty"` // once adopted
}

// PlanDeployments says, for each vRA deployment, whether and how rvav
// adopts it.
func PlanDeployments(e *vra.Estate, r Rvav) []DeploymentPlan {
	out := []DeploymentPlan{}
	slugs := TemplateSlugs(e)
	for _, d := range e.Deployments {
		out = append(out, planDeployment(e, r, d, slugs))
	}
	return out
}

func ownerOf(d vra.Deployment) string {
	if d.OwnedBy != "" {
		return d.OwnedBy
	}
	return d.CreatedBy
}

// machines are a deployment's vSphere machines by name.
func machines(d vra.Deployment) []string {
	var out []string
	for _, res := range d.Resources {
		if res.Type != vra.MachineType {
			continue
		}
		name, _ := res.Properties["resourceName"].(string)
		if name == "" {
			name = res.Name
		}
		out = append(out, name)
	}
	return out
}

func planDeployment(e *vra.Estate, r Rvav, d vra.Deployment, slugs map[string]string) DeploymentPlan {
	p := DeploymentPlan{VRAID: d.ID, Name: d.Name, VRAProject: projectName(e, d.ProjectID), Owner: ownerOf(d), Status: Adopt,
		Notes: []Note{}, RvavName: RvavName(d.Name)}
	note := func(level, where, text string) { p.Notes = append(p.Notes, Note{level, where, text}) }
	skip := func(where, text string) DeploymentPlan {
		p.Status = Skipped
		note(Unmapped, where, text)
		return p
	}

	// Its VMs, in rvav's inventory.
	var missing, ambiguous, unmanaged []string
	managed := 0
	for _, name := range machines(d) {
		vm, ok := r.VMs[name]
		switch {
		case !ok:
			missing = append(missing, name)
		case vm.Managed:
			managed++
			p.VMs = append(p.VMs, vm)
		case vm.Ambiguous:
			ambiguous = append(ambiguous, name)
		default:
			unmanaged = append(unmanaged, name)
			p.VMs = append(p.VMs, vm)
		}
	}
	for _, res := range d.Resources {
		if res.Type != vra.MachineType && !strings.HasPrefix(res.Type, "Cloud.vSphere.Disk") && !strings.HasPrefix(res.Type, "Cloud.vSphere.Network") {
			note(Warning, "resources."+res.Name, res.Type+" stays in vRA's records only: rvav adopts VMs.")
		}
	}

	// Adopted already: by the import (its link), by hand, or its VMs are
	// rvav's.
	if deps := r.Adopted[d.ID]; len(deps) > 0 || managed > 0 {
		p.Status, p.Deployments = Adopted, deps
		note(Info, "vms", "rvav manages it already.")
		if len(unmanaged) > 0 {
			note(Warning, "vms", "Not adopted with it: "+strings.Join(unmanaged, ", ")+". Adopt them from Onboarding into its deployment.")
		}
		return p
	}
	if len(ambiguous) > 0 {
		return skip("vms", "More than one vCenter has a VM named "+strings.Join(ambiguous, ", ")+": adopt them from Onboarding, where each is chosen by its vCenter.")
	}
	if len(p.VMs) == 0 {
		if len(missing) > 0 {
			return skip("vms", "Its VMs ("+strings.Join(missing, ", ")+") aren't in the vCenters rvav reads: deleted, or in a vCenter rvav doesn't have.")
		}
		return skip("vms", "No vSphere machine: nothing for rvav to adopt.")
	}
	if len(missing) > 0 {
		note(Warning, "vms", "Not in the vCenters rvav reads: "+strings.Join(missing, ", ")+". The others are adopted.")
	}

	// Its name, as rvav takes it.
	switch {
	case p.RvavName == "":
		return skip("name", "Its name has nothing rvav can name a deployment with: adopt it from Onboarding, with a name.")
	case p.RvavName != d.Name:
		note(Info, "name", "Named "+p.RvavName+" in rvav: lower-case letters, digits, dots and dashes.")
	}

	// Its project.
	proj, how := mapProject(r, d.ProjectID, p.VRAProject)
	switch {
	case how == HowStays:
		return skip("project", "vRA project "+p.VRAProject+" stays in vRA, as chosen.")
	case proj == nil:
		return skip("project", "vRA project "+p.VRAProject+" has no rvav project: map it to one, or create it.")
	}
	p.ProjectID = proj.ID
	note(Info, "project", fmt.Sprintf("In %s (%s).", proj.Name, map[string]string{HowMapped: "mapped", HowSameName: "the same name"}[how]))

	// Its environment, from its inputs.
	for _, k := range []string{"environment", "env"} {
		if v, ok := d.Inputs[k].(string); ok && v != "" {
			if !slices.Contains(proj.Environments, v) {
				return skip("inputs."+k, fmt.Sprintf("Environment %s isn't one of %s's (%s).", v, proj.Name, strings.Join(proj.Environments, ", ")))
			}
			p.Environment = v
			break
		}
	}
	if p.Environment == "" {
		return skip("inputs", "Its inputs name no environment: rvav can't tell which of "+proj.Name+"'s it belongs to.")
	}

	// Its owner, a member of the project.
	uid, ok := r.Users[strings.ToLower(p.Owner)]
	switch {
	case !ok:
		return skip("owner", "Its owner "+p.Owner+" has never signed in to rvav: they can be its owner once they have, or someone can take it over in vRA first.")
	case !slices.Contains(proj.Members, uid):
		return skip("owner", "Its owner "+p.Owner+" isn't a member of "+proj.Name+" (or only a viewer).")
	}
	p.OwnerID = uid

	// The item it was requested from, as imported: published, and offered
	// to the project.
	bpID := d.BlueprintID
	if bpID == "" {
		if i := slices.IndexFunc(e.CatalogItems, func(c vra.CatalogItem) bool { return c.ID == d.CatalogItemID }); i >= 0 {
			bpID = e.SourceOf(e.CatalogItems[i])
		}
	}
	itemID, ok := r.TemplateItems[bpID]
	if !ok && slugs[bpID] != "" {
		itemID, ok = r.Items[slugs[bpID]]
	}
	if !ok {
		return skip("item", "The item it was requested from isn't imported into rvav: import its cloud template first.")
	}
	if st, known := r.ItemStates[itemID]; known {
		switch {
		case !st.Published:
			return skip("item", st.Slug+" isn't published in rvav: publish it, then adopt.")
		case !st.AllProjects && !slices.Contains(st.Projects, proj.ID):
			return skip("item", st.Slug+" isn't offered to "+proj.Name+" in rvav: share it there.")
		}
	}
	p.CatalogItemID = itemID

	// Its lease: what remains of vRA's, within rvav's policy.
	policy := r.Leases[proj.ID+"/"+p.Environment]
	if d.LeaseExpireAt != nil {
		left := int(math.Ceil(d.LeaseExpireAt.Sub(r.Now).Hours() / 24))
		if left < 1 {
			note(Warning, "lease", "Its lease ended in vRA on "+d.LeaseExpireAt.Format("2 Jan 2006")+": adopted with a day left, for its owner to extend or let go.")
			left = 1
		}
		if policy.Max != nil && left > *policy.Max {
			note(Warning, "lease", fmt.Sprintf("%d days were left in vRA; rvav's lease policy for %s allows at most %d: adopted with %d.", left,
				p.Environment, *policy.Max, *policy.Max))
			left = *policy.Max
		}
		p.LeaseDays = &left
	} else if policy.Default != nil {
		note(Warning, "lease", fmt.Sprintf("No lease in vRA, but rvav's lease policy for %s gives it %d days, after which its VMs are powered off "+
			"and deleted. Change the policy before adopting it if it should live on.", p.Environment, *policy.Default))
	} else {
		note(Info, "lease", "No lease, in vRA or in rvav's policy for "+p.Environment+".")
	}
	return p
}

var nameRun = regexp.MustCompile(`[^a-z0-9._-]+`)

// RvavName is a vRA deployment's name as rvav names deployments: lower
// case, other characters as dashes, at most 63; "" when nothing is left.
func RvavName(name string) string {
	s := nameRun.ReplaceAllString(strings.ToLower(name), "-")
	s = strings.TrimLeft(s, "._-")
	if len(s) > 63 {
		s = s[:63]
	}
	return strings.TrimRight(s, "-")
}

// How a vRA project maps to an rvav project.
const (
	HowMapped   = "mapped"    // an administrator said so
	HowSameName = "same_name" // the rvav project of its name
	HowStays    = "stays"     // an administrator keeps it in vRA
	HowNone     = "none"
)

func mapProject(r Rvav, vraID, vraName string) (*RvavProject, string) {
	if id, ok := r.ProjectMap[vraID]; ok {
		if id == "" {
			return nil, HowStays
		}
		if i := slices.IndexFunc(r.Projects, func(p RvavProject) bool { return p.ID == id }); i >= 0 {
			return &r.Projects[i], HowMapped
		}
		// Mapped to a project rvav no longer has (or archived): not to
		// another by its name.
		return nil, HowNone
	}
	if i := slices.IndexFunc(r.Projects, func(p RvavProject) bool { return strings.EqualFold(p.Name, vraName) }); i >= 0 {
		return &r.Projects[i], HowSameName
	}
	return nil, HowNone
}

func projectName(e *vra.Estate, id string) string {
	if i := slices.IndexFunc(e.Projects, func(p vra.Project) bool { return p.ID == id }); i >= 0 {
		return e.Projects[i].Name
	}
	return id
}
