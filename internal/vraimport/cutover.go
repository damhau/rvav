package vraimport

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/vra"
)

// The cutover (P2.6): project by project, what rvav can check before and
// after a vRA project moves, and whether vRA can be switched off.

// Moved is a vRA project an administrator marked moved.
type Moved struct {
	At   time.Time
	By   string
	Note string
}

// Check states.
const (
	CheckOK     = "ok"     // done
	CheckWarn   = "warn"   // needs doing, or something went wrong
	CheckTodo   = "todo"   // not yet
	CheckManual = "manual" // people check it; rvav can't
)

// Check is one step of a project's cutover.
type Check struct {
	Key    string `json:"key"`
	Label  string `json:"label"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// CutoverProject is a vRA project's cutover.
type CutoverProject struct {
	VRAID           string     `json:"vra_id"`
	Name            string     `json:"name"`
	RvavProjectID   string     `json:"rvav_project_id,omitempty"`
	RvavProjectName string     `json:"rvav_project_name,omitempty"`
	MovedAt         *time.Time `json:"moved_at,omitempty"`
	MovedBy         string     `json:"moved_by,omitempty"`
	Note            string     `json:"note,omitempty"`
	// Ready: mapped, its items published in rvav and shared with it.
	Ready  bool    `json:"ready"`
	Checks []Check `json:"checks"`
}

// Cutover is every project's, and whether vRA can go.
type Cutover struct {
	Projects []CutoverProject `json:"projects"`
	Moved    int              `json:"moved"`
	Total    int              `json:"total"`
	// DeploymentsLeft are vRA's deployments not adopted.
	DeploymentsLeft []string `json:"deployments_left"`
	// SwitchOff: every project moved, every deployment adopted.
	SwitchOff bool `json:"switch_off"`
}

// leaseSoon: an adopted deployment whose vRA lease ends within this many
// days is a warning.
const leaseSoon = 90

func planCutover(e *vra.Estate, r Rvav, st ImportState, items []ItemImport, deps []DeploymentPlan) Cutover {
	c := Cutover{Projects: []CutoverProject{}, DeploymentsLeft: []string{}, Total: len(e.Projects)}
	itemOf := map[string]ItemImport{} // template ID → its plan
	for _, it := range items {
		itemOf[it.BlueprintID] = it
	}
	depOf := map[string]DeploymentPlan{}
	for _, d := range deps {
		depOf[d.VRAID] = d
		if d.Status != Adopted {
			c.DeploymentsLeft = append(c.DeploymentsLeft, d.Name)
		}
	}
	for _, vp := range e.Projects {
		cp := CutoverProject{VRAID: vp.ID, Name: vp.Name, Checks: []Check{}}
		check := func(key, label, state, detail string) {
			cp.Checks = append(cp.Checks, Check{key, label, state, detail})
		}
		m, moved := st.Cutover[vp.ID]
		if moved {
			at := m.At
			cp.MovedAt, cp.MovedBy, cp.Note = &at, m.By, m.Note
			c.Moved++
		}

		proj, how := mapProject(r, vp.ID, vp.Name)
		switch {
		case how == HowStays:
			check("project", "rvav project", CheckTodo, "It stays in vRA, as chosen: map it to an rvav project to move it.")
		case proj == nil:
			check("project", "rvav project", CheckTodo, "Map it to an rvav project, or create one.")
		default:
			cp.RvavProjectID, cp.RvavProjectName = proj.ID, proj.Name
			check("project", "rvav project", CheckOK, proj.Name+map[string]string{HowMapped: ", mapped.", HowSameName: ", by name."}[how])
		}

		// The items vRA offers it (on their own or by content source), in
		// rvav.
		var templates []vra.CatalogItem
		var others []string
		for _, ci := range e.ItemsFor(vp.ID) {
			if ci.Type.ID == "com.vmw.blueprint" {
				templates = append(templates, ci)
			} else {
				others = append(others, ci.Name)
			}
		}
		var published int
		var pending, unshared []string
		for _, ci := range templates {
			it, ok := itemOf[e.SourceOf(ci)]
			s, imported := st.Items[it.RvavItemID]
			switch {
			case !ok || it.Action == ActionSkip:
				pending = append(pending, ci.Name+" (doesn't map)")
			case it.RvavItemID == "" || !imported:
				pending = append(pending, ci.Name+" (not imported)")
			case !s.Published:
				pending = append(pending, ci.Name+" (a draft)")
			default:
				published++
			}
			if imported && proj != nil && !s.AllProjects && !slices.Contains(s.Projects, proj.ID) {
				unshared = append(unshared, ci.Name)
			}
		}
		itemsOK := len(pending) == 0
		switch {
		case len(templates) == 0:
			check("items", "Catalog items", CheckOK, "vRA offers it no cloud template.")
		case itemsOK:
			check("items", "Catalog items", CheckOK, fmt.Sprintf("All %d published in rvav.", len(templates)))
		default:
			check("items", "Catalog items", CheckWarn, fmt.Sprintf("%d of %d published in rvav. Not yet: %s.", published, len(templates),
				strings.Join(pending, ", ")))
		}
		sharedOK := proj != nil && len(unshared) == 0
		switch {
		case proj == nil:
			check("sharing", "Shared in rvav", CheckTodo, "Once it has an rvav project.")
		case sharedOK:
			check("sharing", "Shared in rvav", CheckOK, "Its items are offered to "+proj.Name+" in rvav.")
		default:
			check("sharing", "Shared in rvav", CheckTodo, "Not yet offered to "+proj.Name+": "+strings.Join(unshared, ", ")+". The import shares them.")
		}
		if len(others) > 0 {
			check("extensibility", "vRO and ABX items", CheckManual, "Replace "+strings.Join(others, ", ")+" as the vRO triage says.")
		}
		// What vRA runs on its deployments' events: rvav's subscriptions
		// must do it once they are rvav's.
		var subs []string
		for _, s := range e.Subscriptions {
			if !s.Disabled && (len(s.Constraints.ProjectID) == 0 || slices.Contains(s.Constraints.ProjectID, vp.ID)) {
				subs = append(subs, s.Name+" ("+s.EventTopicID+")")
			}
		}
		if len(subs) > 0 {
			check("subscriptions", "vRA subscriptions", CheckManual, "vRA runs these on its deployments' events: "+strings.Join(subs, ", ")+
				". rvav subscriptions must do what they did, as the vRO triage says, before its deployments are rvav's.")
		}

		// Its deployments.
		var mine, left, leased, leasedLater []string
		var since []string
		for _, d := range e.Deployments {
			if d.ProjectID != vp.ID {
				continue
			}
			mine = append(mine, d.Name)
			pl := depOf[d.ID]
			if pl.Status == Adopted {
				switch {
				case d.LeaseExpireAt == nil:
				case d.LeaseExpireAt.Before(r.Now.AddDate(0, 0, leaseSoon)):
					leased = append(leased, d.Name+" ("+d.LeaseExpireAt.Format("2 Jan 2006")+")")
				default:
					leasedLater = append(leasedLater, d.Name+" ("+d.LeaseExpireAt.Format("2 Jan 2006")+")")
				}
			} else {
				why := ""
				if n := len(pl.Notes); n > 0 && pl.Notes[n-1].Level == Unmapped {
					why = ": " + strings.TrimSuffix(pl.Notes[n-1].Text, ".")
				}
				left = append(left, d.Name+why)
			}
			if moved && d.CreatedAt.After(m.At) && pl.Status != Adopted {
				since = append(since, d.Name)
			}
		}
		switch {
		case len(mine) == 0:
			check("deployments", "Deployments adopted", CheckOK, "It has none in vRA.")
		case len(left) == 0:
			check("deployments", "Deployments adopted", CheckOK, fmt.Sprintf("All %d adopted.", len(mine)))
		default:
			check("deployments", "Deployments adopted", CheckWarn, fmt.Sprintf("%d of %d adopted. Still in vRA: %s.", len(mine)-len(left), len(mine),
				strings.Join(left, "; ")))
		}
		switch {
		case len(leased) > 0:
			check("vra_lease", "Leases in vRA", CheckWarn, "vRA deletes these when their lease there ends, adopted or not, within "+
				fmt.Sprint(leaseSoon)+" days: "+strings.Join(slices.Concat(leased, leasedLater), ", ")+
				". Remove their lease in vRA, or extend it past the day vRA is switched off.")
		case len(leasedLater) > 0:
			check("vra_lease", "Leases in vRA", CheckManual, "vRA deletes these when their lease there ends: "+strings.Join(leasedLater, ", ")+
				". Check vRA is switched off before then, or remove their lease in vRA.")
		}

		// Its people.
		var users, missing, groups []string
		for _, pr := range slices.Concat(vp.Administrators, vp.Members) {
			switch {
			case pr.Type == "group":
				groups = append(groups, pr.Email)
			case !slices.Contains(users, pr.Email):
				users = append(users, pr.Email)
				if _, ok := r.Users[strings.ToLower(pr.Email)]; !ok {
					missing = append(missing, pr.Email)
				}
			}
		}
		detail := ""
		state := CheckOK
		switch {
		case len(users) == 0:
			detail = "vRA names no one in it."
		case len(missing) == 0:
			detail = fmt.Sprintf("All %d have signed in to rvav.", len(users))
		default:
			state = CheckWarn
			detail = fmt.Sprintf("%d of %d have signed in to rvav. Not yet: %s.", len(users)-len(missing), len(users), strings.Join(missing, ", "))
		}
		if len(groups) > 0 {
			detail += " Groups (" + strings.Join(groups, ", ") + "): give their identity-provider groups to the rvav project."
		}
		check("people", "People", state, detail)

		var pols []string
		for _, p := range e.Policies {
			if p.ProjectID == "" || p.ProjectID == vp.ID {
				pols = append(pols, p.Name)
			}
		}
		if len(pols) > 0 {
			check("policies", "Policies", CheckManual, "Set by hand from the plan: "+strings.Join(pols, ", ")+".")
		}

		// Once moved: vRA offers it nothing, and nothing new is made there.
		if moved {
			offered := 0
			for _, en := range e.Entitlements {
				if en.ProjectID == vp.ID {
					offered++
				}
			}
			if offered > 0 {
				check("vra_offer", "vRA offers it nothing", CheckWarn, fmt.Sprintf("vRA still offers it %d items: vRA's administrators remove them.", offered))
			} else {
				check("vra_offer", "vRA offers it nothing", CheckOK, "vRA offers it no item.")
			}
			if len(since) > 0 {
				check("vra_new", "Nothing new in vRA", CheckWarn, "Made in vRA since it moved, not adopted: "+strings.Join(since, ", ")+". Adopt them.")
			} else {
				check("vra_new", "Nothing new in vRA", CheckOK, "No deployment made in vRA since it moved.")
			}
		}

		cp.Ready = proj != nil && itemsOK && sharedOK
		c.Projects = append(c.Projects, cp)
	}
	c.SwitchOff = c.Total > 0 && c.Moved == c.Total && len(c.DeploymentsLeft) == 0
	return c
}
