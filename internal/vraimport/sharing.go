package vraimport

import (
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/vra"
)

// vRA's project roles a content sharing policy names, as rvav's.
var sharingRoles = map[string]string{
	"administrator": "project-admin", "project_administrator": "project-admin",
	"member": "member", "project_member": "member",
	"viewer": "viewer", "project_viewer": "viewer",
	"supervisor": "supervisor", "project_supervisor": "supervisor",
}

// audienceOf is who a content sharing policy's principals are in an rvav
// project: users by email, groups by name, roles mapped; what doesn't map
// is in the item's notes. False when nothing maps.
func audienceOf(it *ItemImport, r Rvav, proj *RvavProject, principals []vra.SharePrincipal) (ShareAudience, bool) {
	a := ShareAudience{ProjectID: proj.ID, UserIDs: []string{}, Groups: []string{}, Roles: []string{}}
	var noUser, noRole []string
	for _, p := range principals {
		ref := strings.TrimSpace(p.ReferenceID)
		switch strings.ToUpper(p.Type) {
		case "USER":
			if id, ok := r.Users[strings.ToLower(ref)]; ok {
				a.UserIDs = append(a.UserIDs, id)
			} else {
				noUser = append(noUser, ref)
			}
		case "GROUP":
			if ref != "" {
				a.Groups = append(a.Groups, ref)
			}
		case "ROLE":
			if k, ok := sharingRoles[strings.ToLower(strings.TrimPrefix(ref, "ROLE:"))]; ok {
				a.Roles = append(a.Roles, k)
			} else {
				noRole = append(noRole, ref)
			}
		default:
			noRole = append(noRole, p.Type+" "+ref)
		}
	}
	tidy := func(l []string) []string {
		return slices.Compact(append([]string{}, slices.Sorted(slices.Values(l))...))
	}
	a.UserIDs, a.Groups, a.Roles = tidy(a.UserIDs), tidy(a.Groups), tidy(a.Roles)
	in := " in " + proj.Name
	if len(noUser) > 0 {
		it.Notes = append(it.Notes, Note{Unmapped, "sharing", "vRA shares it" + in + " with " + strings.Join(tidy(noUser), ", ") +
			", who has no rvav account yet: share it with them once they have signed in."})
	}
	if len(noRole) > 0 {
		it.Notes = append(it.Notes, Note{Unmapped, "sharing", "vRA shares it" + in + " with " + strings.Join(tidy(noRole), ", ") +
			", which has no rvav equivalent."})
	}
	if len(a.Groups) > 0 {
		it.Notes = append(it.Notes, Note{Warning, "sharing", "Shared" + in + " with the groups " + strings.Join(a.Groups, ", ") +
			" as vRA names them: check the identity provider's token names them the same."})
	}
	return a, len(a.UserIDs)+len(a.Groups)+len(a.Roles) > 0
}
