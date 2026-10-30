package vraimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/vra"
)

// The import's dry run (P2.5): what importing an estate would create,
// change or leave in rvav, from what earlier imports made (vra_links).

// Item import actions.
const (
	ActionCreate    = "create"    // a new item, its versions as drafts
	ActionUpdate    = "update"    // a new draft of the item made before
	ActionUnchanged = "unchanged" // what the template maps to is what was imported
	ActionConflict  = "conflict"  // something in rvav is in the way (Reason)
	ActionSkip      = "skip"      // nothing maps
)

// ItemState is what the import needs of an rvav catalog item.
type ItemState struct {
	ID, Slug    string
	HasDraft    bool
	Published   bool
	AllProjects bool
	Projects    []string
}

// Link is what an earlier import made of a template.
type Link struct {
	RvavID, Version, Hash string
}

// ImportState is what rvav has from earlier imports.
type ImportState struct {
	Links map[string]Link      // template ID → its item
	Items map[string]ItemState // item ID → state
	Slugs map[string]string    // slug → item ID
	// Cutover: the vRA projects marked moved.
	Cutover map[string]Moved
}

// ItemImport is what the import does with a cloud template.
type ItemImport struct {
	BlueprintID   string   `json:"blueprint_id"`
	Name          string   `json:"name"`
	Slug          string   `json:"slug"`
	Status        string   `json:"status"`
	Action        string   `json:"action"`
	Reason        string   `json:"reason,omitempty"`
	Version       string   `json:"version,omitempty"`
	RvavItemID    string   `json:"rvav_item_id,omitempty"`
	Notes         []Note   `json:"notes"`
	ShareAdd      []string `json:"share_add"`
	ShareUnmapped []string `json:"share_unmapped"`

	// Doc and Hash are what the import sends and records.
	Doc  *ItemDoc `json:"-"`
	Hash string   `json:"-"`
}

// ProjectPlan is how a vRA project maps.
type ProjectPlan struct {
	VRAID           string `json:"vra_id"`
	Name            string `json:"name"`
	RvavProjectID   string `json:"rvav_project_id,omitempty"`
	RvavProjectName string `json:"rvav_project_name,omitempty"`
	How             string `json:"how"`
	Items           int    `json:"items"`
	Deployments     int    `json:"deployments"`
}

// Summary counts what the import would do.
type Summary struct {
	ItemsCreate        int `json:"items_create"`
	ItemsUpdate        int `json:"items_update"`
	ItemsUnchanged     int `json:"items_unchanged"`
	ItemsBlocked       int `json:"items_blocked"`
	SharesAdd          int `json:"shares_add"`
	DeploymentsAdopt   int `json:"deployments_adopt"`
	DeploymentsAdopted int `json:"deployments_adopted"`
	DeploymentsSkipped int `json:"deployments_skipped"`
}

// ImportPlan is the dry run.
type ImportPlan struct {
	Summary     Summary          `json:"summary"`
	Projects    []ProjectPlan    `json:"projects"`
	Items       []ItemImport     `json:"items"`
	Deployments []DeploymentPlan `json:"deployments"`
	Policies    []PolicyPlan     `json:"policies"`
	Triage      Triage           `json:"triage"`
	Cutover     Cutover          `json:"cutover"`
}

// PlanImport is what importing the estate would do, nothing written.
func PlanImport(e *vra.Estate, r Rvav, st ImportState) ImportPlan {
	p := ImportPlan{Projects: []ProjectPlan{}, Items: []ItemImport{}, Policies: PlanPolicies(e), Triage: TriageVRO(e)}
	for _, vp := range e.Projects {
		pp := ProjectPlan{VRAID: vp.ID, Name: vp.Name}
		proj, how := mapProject(r, vp.ID, vp.Name)
		pp.How = how
		if proj != nil {
			pp.RvavProjectID, pp.RvavProjectName = proj.ID, proj.Name
		}
		for _, en := range e.Entitlements {
			if en.ProjectID == vp.ID && en.Definition.Type == "CatalogItemIdentifier" {
				pp.Items++
			}
		}
		for _, d := range e.Deployments {
			if d.ProjectID == vp.ID {
				pp.Deployments++
			}
		}
		p.Projects = append(p.Projects, pp)
	}

	if r.TemplateItems == nil {
		r.TemplateItems = map[string]string{}
	}
	for _, bp := range e.Blueprints {
		it := planItem(e, r, st, bp)
		p.Items = append(p.Items, it)
		// Deployments of a template about to be imported find its item
		// once it is.
		if it.RvavItemID != "" && it.Action != ActionConflict {
			r.TemplateItems[bp.ID] = it.RvavItemID
		}
		switch it.Action {
		case ActionCreate:
			p.Summary.ItemsCreate++
		case ActionUpdate:
			p.Summary.ItemsUpdate++
		case ActionUnchanged:
			p.Summary.ItemsUnchanged++
		default:
			p.Summary.ItemsBlocked++
		}
		p.Summary.SharesAdd += len(it.ShareAdd)
	}
	p.Deployments = PlanDeployments(e, r)
	p.Cutover = planCutover(e, r, st, p.Items, p.Deployments)
	for _, d := range p.Deployments {
		switch d.Status {
		case Adopt:
			p.Summary.DeploymentsAdopt++
		case Adopted:
			p.Summary.DeploymentsAdopted++
		default:
			p.Summary.DeploymentsSkipped++
		}
	}
	return p
}

func planItem(e *vra.Estate, r Rvav, st ImportState, bp vra.Blueprint) ItemImport {
	m := MapTemplate(e, bp)
	it := ItemImport{BlueprintID: bp.ID, Name: m.Name, Slug: m.Slug, Status: m.Status, Notes: m.Notes, ShareAdd: []string{},
		ShareUnmapped: []string{}, Doc: m.Doc}
	if it.Notes == nil {
		it.Notes = []Note{}
	}
	if m.Status == Skipped || m.Doc == nil || len(m.Doc.Versions) == 0 {
		it.Action = ActionSkip
		return it
	}
	last := m.Doc.Versions[len(m.Doc.Versions)-1]
	it.Version = last.Number
	b, _ := json.Marshal(last)
	sum := sha256.Sum256(b)
	it.Hash = hex.EncodeToString(sum[:])

	var item *ItemState
	if l, ok := st.Links[bp.ID]; ok {
		if s, ok := st.Items[l.RvavID]; ok {
			item = &s
			it.RvavItemID = s.ID
			switch {
			case l.Hash == it.Hash:
				it.Action = ActionUnchanged
			case s.HasDraft:
				it.Action = ActionConflict
				it.Reason = s.Slug + " has a draft in rvav: publish or discard it, then import again for the template's changes."
			default:
				it.Action = ActionUpdate
			}
		}
	}
	if item == nil {
		if id, taken := st.Slugs[it.Slug]; taken {
			it.Action = ActionConflict
			it.Reason = "rvav has an item " + it.Slug + " not imported from this template: rename one of them."
			it.RvavItemID = ""
			_ = id
			return it
		}
		it.Action = ActionCreate
	}

	// Sharing: the rvav projects of the vRA projects it is shared with.
	shared := map[string]bool{}
	for _, ci := range e.CatalogItems {
		if ci.Type.ID != "com.vmw.blueprint" || ci.SourceID != bp.ID {
			continue
		}
		for _, en := range e.Entitlements {
			if en.Definition.Type == "CatalogItemIdentifier" && en.Definition.ID == ci.ID {
				shared[en.ProjectID] = true
			}
		}
	}
	for _, vp := range e.Projects {
		if !shared[vp.ID] {
			continue
		}
		proj, _ := mapProject(r, vp.ID, vp.Name)
		switch {
		case proj == nil:
			it.ShareUnmapped = append(it.ShareUnmapped, vp.Name)
		case item != nil && (item.AllProjects || slices.Contains(item.Projects, proj.ID)):
		case !slices.Contains(it.ShareAdd, proj.ID):
			it.ShareAdd = append(it.ShareAdd, proj.ID)
		}
	}
	slices.Sort(it.ShareAdd)
	if len(it.ShareUnmapped) > 0 {
		it.Notes = append(it.Notes, Note{Warning, "sharing", "Shared in vRA with " + strings.Join(it.ShareUnmapped, ", ") +
			", which map to no rvav project: map them to share it there too."})
	}
	return it
}
