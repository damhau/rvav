package vraimport

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/vra"
)

// vRA's image mappings, by image name across the regions' image profiles,
// as catalogue templates with a source per vCenter (ADR-0037).

// TemplateImport is the catalogue template an image name becomes.
type TemplateImport struct {
	Image    string `json:"image"`
	Key      string `json:"key"`
	Label    string `json:"label"`
	OSFamily string `json:"os_family"`
	// Action: create; unchanged (rvav's template of that key is what the
	// mappings give); differs (rvav has a template of that key, left as it
	// is); skip (no mapping in a vCenter rvav manages).
	Action      string           `json:"action"`
	Sources     []TemplateSource `json:"sources"`
	UserData    string           `json:"user_data"`
	Constraints []string         `json:"constraints"`
	Notes       []Note           `json:"notes"`
}

// TemplateSource is an image's template in a vCenter rvav manages, by its
// name in GET /admin/vcenters.
type TemplateSource struct {
	VCenter  string `json:"vcenter"`
	Template string `json:"template"`
}

// RvavVCenter is a vCenter rvav manages: its name, and its URL's host.
type RvavVCenter struct {
	Name, Host string
}

// RvavTemplate is what rvav has of a catalogue template.
type RvavTemplate struct {
	Sources     []TemplateSource
	UserData    string
	Constraints []string
}

const ActionDiffers = "differs"

var keyChars = regexp.MustCompile(`[^a-z0-9]+`)

// ImageKey is the catalogue template key of an image name: lower case,
// other characters as dashes, starting with a letter.
func ImageKey(image string) string {
	k := strings.Trim(keyChars.ReplaceAllString(strings.ToLower(image), "-"), "-")
	if k == "" || k[0] < 'a' || k[0] > 'z' {
		k = "img-" + k
	}
	if len(k) > 41 {
		k = strings.TrimRight(k[:41], "-")
	}
	if len(k) < 2 {
		k += "-img"
	}
	return k
}

// ImageKeys are the catalogue template keys of the image names vRA's
// image profiles map, by name: ImageKey's, with -2, -3 and on for a name
// whose key an earlier one, in name order, has.
func ImageKeys(e *vra.Estate) map[string]string {
	names := map[string]bool{}
	for _, p := range e.Images {
		for name := range p.ImageMappings.Mapping {
			names[name] = true
		}
	}
	out, taken := map[string]string{}, map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		base := ImageKey(name)
		key := base
		for n := 2; taken[key]; n++ {
			suffix := fmt.Sprintf("-%d", n)
			key = strings.TrimRight(base[:min(len(base), 41-len(suffix))], "-") + suffix
		}
		taken[key], out[name] = true, key
	}
	return out
}

// regionMapping is an image's mapping in one image profile.
type regionMapping struct {
	profile vra.ImageProfile
	mapping vra.ImageMapping
}

func imageMappings(e *vra.Estate, image string) []regionMapping {
	var out []regionMapping
	for _, p := range e.Images {
		if m, ok := p.ImageMappings.Mapping[image]; ok && m.Name != "" {
			out = append(out, regionMapping{p, m})
		}
	}
	slices.SortStableFunc(out, func(a, b regionMapping) int {
		return strings.Compare(e.RegionName(a.profile.RegionID)+a.profile.ID, e.RegionName(b.profile.RegionID)+b.profile.ID)
	})
	return out
}

// needsCatalogue says whether an image is more than one vCenter template's
// name: another name in some region, or cloud-init or constraints of its
// own. Such an image is a catalogue template the item binds; any other,
// the vCenter template the item names, as rvav places it wherever vCenter
// has it.
func needsCatalogue(e *vra.Estate, image string) bool {
	ms := imageMappings(e, image)
	for _, m := range ms {
		if m.mapping.Name != ms[0].mapping.Name || strings.TrimSpace(m.mapping.CloudConfig) != "" || len(m.mapping.Constraints) > 0 {
			return true
		}
	}
	return false
}

// imageConstraints maps an image mapping's constraints: rvav's compute
// constraints, and a problem for each it can't write.
func imageConstraints(list []vra.ImageConstraint) ([]string, []string) {
	var out, problems []string
	for _, c := range list {
		expr := strings.TrimSpace(c.Expression)
		lower := strings.ToLower(expr)
		if !c.Mandatory && !strings.HasSuffix(lower, ":soft") && !strings.HasSuffix(lower, ":hard") {
			expr += ":soft"
		}
		tag, err := scope{}.constraint(expr)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("Constraint %q: %v. Left out.", c.Expression, err))
		case strings.Contains(tag, "${"):
			problems = append(problems, fmt.Sprintf("Constraint %q depends on the request: a template's constraints are tags as they are. Left out.", c.Expression))
		default:
			out = append(out, tag)
		}
	}
	return out, problems
}

// rvavVCenter is the vCenter rvav manages of a cloud account's host name:
// by its name, else its URL's host.
func rvavVCenter(r Rvav, host string) (string, bool) {
	for _, v := range r.VCenters {
		if strings.EqualFold(v.Name, host) || v.Host != "" && strings.EqualFold(v.Host, host) {
			return v.Name, true
		}
	}
	return "", false
}

// URLHost is a vCenter URL's host name.
func URLHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// PlanTemplates is the catalogue template each image name of vRA's image
// profiles becomes: a source in each region's vCenter rvav manages, the
// first region's cloud-init and constraints. The import makes those rvav
// hasn't; it never changes one rvav has.
func PlanTemplates(e *vra.Estate, r Rvav) []TemplateImport {
	keys := ImageKeys(e)
	owner := map[string]string{}
	for name, key := range keys {
		owner[key] = name
	}
	out := []TemplateImport{}
	for _, image := range slices.Sorted(maps.Keys(keys)) {
		t := TemplateImport{Image: image, Key: keys[image], Label: image, OSFamily: "linux", Sources: []TemplateSource{},
			Constraints: []string{}, Notes: []Note{}}
		note := func(level, where, text string) { t.Notes = append(t.Notes, Note{level, where, text}) }
		if base := ImageKey(image); t.Key != base {
			note(Warning, "", fmt.Sprintf("Images %s and %s would both be template %s: this one is %s, and items using it bind %s.",
				owner[base], image, base, t.Key, t.Key))
		}
		if windowsImage(image) {
			t.OSFamily = "windows"
		}
		ms := imageMappings(e, image)
		var first *regionMapping
		for i, m := range ms {
			where := "image_profiles." + m.profile.ID
			region := e.RegionName(m.profile.RegionID)
			host := e.VCenterOf(m.profile.RegionID)
			if host == "" {
				note(Unmapped, where, fmt.Sprintf("Region %s isn't in a vSphere cloud account read: its template %s is left out.", region, m.mapping.Name))
				continue
			}
			vc, ok := rvavVCenter(r, host)
			if !ok {
				note(Unmapped, where, fmt.Sprintf("Region %s is in vCenter %s, which rvav doesn't manage: its template %s is left out. Add the vCenter in Administration › vCenters, then import again.",
					region, host, m.mapping.Name))
				continue
			}
			if j := slices.IndexFunc(t.Sources, func(s TemplateSource) bool { return s.VCenter == vc }); j >= 0 {
				if t.Sources[j].Template != m.mapping.Name {
					note(Warning, where, fmt.Sprintf("Region %s is in vCenter %s too, with template %s: rvav has one template per vCenter, %s.",
						region, host, m.mapping.Name, t.Sources[j].Template))
				}
				continue
			}
			t.Sources = append(t.Sources, TemplateSource{VCenter: vc, Template: m.mapping.Name})
			if first == nil {
				first = &ms[i]
				continue
			}
			if strings.TrimSpace(m.mapping.CloudConfig) != strings.TrimSpace(first.mapping.CloudConfig) {
				note(Warning, where+".cloudConfig", fmt.Sprintf("Region %s's cloud-init differs from region %s's: the template has one, %s's.",
					region, e.RegionName(first.profile.RegionID), e.RegionName(first.profile.RegionID)))
			}
			if !slices.Equal(m.mapping.Constraints, first.mapping.Constraints) {
				note(Warning, where+".constraints", fmt.Sprintf("Region %s's constraints differ from region %s's: the template has one set, %s's.",
					region, e.RegionName(first.profile.RegionID), e.RegionName(first.profile.RegionID)))
			}
		}
		if first != nil {
			where := "image_profiles." + first.profile.ID
			if ci := strings.TrimSpace(first.mapping.CloudConfig); ci != "" {
				switch {
				case t.OSFamily == "windows":
					note(Unmapped, where+".cloudConfig", "cloud-init (cloudbase-init) on a Windows image: left out, since rvav customizes a Windows "+
						"template by a specification unless it is marked as using cloudbase-init. If it does, mark it, and give it this user data.")
				case !strings.HasPrefix(ci, "#cloud-config") && !strings.HasPrefix(ci, "#!"):
					note(Unmapped, where+".cloudConfig", "The cloud-init doesn't start with #cloud-config or #!. Left out.")
				default:
					ud, problems := scope{}.userData(ci)
					t.UserData = ud
					for _, pr := range problems {
						note(Warning, where+".cloudConfig", pr)
					}
				}
			}
			cons, problems := imageConstraints(first.mapping.Constraints)
			t.Constraints = append(t.Constraints, cons...)
			for _, pr := range problems {
				note(Unmapped, where+".constraints", pr)
			}
		}
		var missing []string
		if first != nil && t.UserData != "" {
			missing = slices.DeleteFunc(secretsIn(first.mapping.CloudConfig), func(s string) bool { return r.Secrets[s] })
		}
		have, exists := r.Templates[t.Key]
		switch {
		case len(t.Sources) == 0:
			t.Action = ActionSkip
		case len(missing) > 0 && !exists:
			t.Action = ActionSkip
			note(Unmapped, "image_profiles."+first.profile.ID+".cloudConfig", "Its cloud-init opens secrets rvav doesn't have: store "+
				strings.Join(missing, ", ")+" in Catalog items › Secrets, then import again. vRA never gives a secret's value.")
		case !exists:
			t.Action = ActionCreate
		case sameTemplate(have, t):
			t.Action = ActionUnchanged
		default:
			t.Action = ActionDiffers
			note(Warning, "", fmt.Sprintf("rvav has a template %s that isn't what vRA's mappings give: the import leaves it as it is.", t.Key))
		}
		out = append(out, t)
	}
	return out
}

func sameTemplate(have RvavTemplate, t TemplateImport) bool {
	cmpSource := func(a, b TemplateSource) int { return strings.Compare(a.VCenter, b.VCenter) }
	a, b := slices.SortedFunc(slices.Values(have.Sources), cmpSource), slices.SortedFunc(slices.Values(t.Sources), cmpSource)
	return slices.Equal(a, b) && strings.TrimSpace(have.UserData) == strings.TrimSpace(t.UserData) &&
		slices.Equal(have.Constraints, t.Constraints)
}
