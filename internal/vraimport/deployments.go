package vraimport

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// Deployment plan statuses.
const (
	Adopt   = "adopt"   // rvav will adopt it
	Adopted = "adopted" // rvav manages its VMs already
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

// Rvav is what rvav has, for planning: its projects, the vRA projects
// mapped to them, its users by email, the VMs of its inventory by name,
// its catalog items by slug.
type Rvav struct {
	Projects   []RvavProject
	ProjectMap map[string]string // vRA project ID → rvav project ID
	Users      map[string]string // lower-case email → user ID
	VMs        map[string]VMRef  // name → VM
	Items      map[string]string // slug → item ID
	// TemplateItems are the items imported from vRA's templates: template
	// ID → item ID (vra_links).
	TemplateItems map[string]string
	Now           time.Time
}

// DeploymentPlan is what becomes of a vRA deployment: adopted into an rvav
// deployment of its name, or left in vRA with the reason.
type DeploymentPlan struct {
	VRAID         string   `json:"vra_id"`
	Name          string   `json:"name"`
	VRAProject    string   `json:"vra_project"`
	Owner         string   `json:"owner"`
	Status        string   `json:"status"`
	Notes         []Note   `json:"notes"`
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
	var out []DeploymentPlan
	for _, d := range e.Deployments {
		out = append(out, planDeployment(e, r, d))
	}
	return out
}

func planDeployment(e *vra.Estate, r Rvav, d vra.Deployment) DeploymentPlan {
	p := DeploymentPlan{VRAID: d.ID, Name: d.Name, VRAProject: projectName(e, d.ProjectID), Status: Adopt}
	note := func(level, where, text string) { p.Notes = append(p.Notes, Note{level, where, text}) }
	skip := func(where, text string) DeploymentPlan {
		p.Status = Skipped
		note(Unmapped, where, text)
		return p
	}

	// Its VMs, in rvav's inventory.
	var missing, ambiguous []string
	managed := 0
	for _, res := range d.Resources {
		switch {
		case res.Type == vra.MachineType:
			name, _ := res.Properties["resourceName"].(string)
			if name == "" {
				name = res.Name
			}
			vm, ok := r.VMs[name]
			switch {
			case !ok:
				missing = append(missing, name)
			case vm.Ambiguous:
				ambiguous = append(ambiguous, name)
			case vm.Managed:
				managed++
			default:
				p.VMs = append(p.VMs, vm)
			}
		case strings.HasPrefix(res.Type, "Cloud.vSphere.Disk") || strings.HasPrefix(res.Type, "Cloud.vSphere.Network"):
			// A machine's disk or network: adopted with it.
		default:
			note(Warning, "resources."+res.Name, res.Type+" stays in vRA's records only: rvav adopts VMs.")
		}
	}
	if managed > 0 {
		p.Status, p.VMs = Adopted, nil
		note(Info, "vms", "rvav manages its VMs already.")
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

	// Its project.
	proj, how := mapProject(r, d.ProjectID, p.VRAProject)
	if proj == nil {
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
	owner := d.OwnedBy
	if owner == "" {
		owner = d.CreatedBy
	}
	p.Owner = owner
	uid, ok := r.Users[strings.ToLower(owner)]
	switch {
	case !ok:
		return skip("owner", "Its owner "+owner+" has never signed in to rvav: they can be its owner once they have, or someone can take it over in vRA first.")
	case !slices.Contains(proj.Members, uid):
		return skip("owner", "Its owner "+owner+" isn't a member of "+proj.Name+" (or only a viewer).")
	}
	p.OwnerID = uid

	// The item it was requested from, as imported.
	bpID := d.BlueprintID
	if i := slices.IndexFunc(e.CatalogItems, func(c vra.CatalogItem) bool { return c.ID == d.CatalogItemID }); i >= 0 && e.CatalogItems[i].SourceID != "" {
		bpID = e.CatalogItems[i].SourceID
	}
	slugName := ""
	if i := slices.IndexFunc(e.CatalogItems, func(c vra.CatalogItem) bool { return c.ID == d.CatalogItemID }); i >= 0 {
		if b := slices.IndexFunc(e.Blueprints, func(b vra.Blueprint) bool { return b.ID == e.CatalogItems[i].SourceID }); b >= 0 {
			slugName = slug(e.Blueprints[b].Name)
		}
	} else if b := slices.IndexFunc(e.Blueprints, func(b vra.Blueprint) bool { return b.ID == d.BlueprintID }); b >= 0 {
		slugName = slug(e.Blueprints[b].Name)
	}
	itemID, ok := r.TemplateItems[bpID]
	if !ok {
		itemID, ok = r.Items[slugName]
	}
	if !ok {
		return skip("item", "The item it was requested from isn't imported into rvav: import its cloud template first.")
	}
	p.CatalogItemID = itemID

	// Its lease: what remains of vRA's.
	if d.LeaseExpireAt != nil {
		left := int(math.Ceil(d.LeaseExpireAt.Sub(r.Now).Hours() / 24))
		if left < 1 {
			note(Warning, "lease", "Its lease ended in vRA on "+d.LeaseExpireAt.Format("2 Jan 2006")+": adopted with a day left, for its owner to extend or let go.")
			left = 1
		}
		p.LeaseDays = &left
	} else {
		note(Info, "lease", "No lease in vRA: "+proj.Name+"'s lease policy for "+p.Environment+" applies.")
	}
	return p
}

// How a vRA project maps to an rvav project.
const (
	HowMapped   = "mapped"    // an administrator said so
	HowSameName = "same_name" // the rvav project of its name
	HowNone     = "none"
)

func mapProject(r Rvav, vraID, vraName string) (*RvavProject, string) {
	if id, ok := r.ProjectMap[vraID]; ok {
		if i := slices.IndexFunc(r.Projects, func(p RvavProject) bool { return p.ID == id }); i >= 0 {
			return &r.Projects[i], HowMapped
		}
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
