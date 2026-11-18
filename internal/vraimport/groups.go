package vraimport

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/damhau/rvav/internal/vra"
)

// GroupImport is what the import does with a vRA property group: the
// rvav property group it becomes (ADR-0038).
type GroupImport struct {
	VRAID string `json:"vra_id"`
	// Name is vRA's, as templates name it.
	Name  string `json:"name"`
	Key   string `json:"key"`
	Label string `json:"label"`
	// Type is input or constant.
	Type   string `json:"type"`
	Action string `json:"action"`
	// Version is the one the items name: the one the import makes, or
	// rvav's latest.
	Version   int            `json:"version"`
	Inputs    []GroupInput   `json:"inputs"`
	Constants map[string]any `json:"constants"`
	Notes     []Note         `json:"notes"`
	// Description is what the import gives the group made.
	Description string `json:"-"`
}

// GroupInput is a property group's input as rvav's API has it.
type GroupInput struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Secret   bool     `json:"secret,omitempty"`
	Default  any      `json:"default,omitempty"`
	Options  []string `json:"options,omitempty"`
	Help     string   `json:"help,omitempty"`
}

// RvavGroup is the latest version of one of rvav's property groups.
type RvavGroup struct {
	Version   int
	Inputs    []GroupInput
	Constants map[string]any
}

const (
	groupRefPrefix = "/ref/property-groups/"
	maxGroupInputs = 50
)

var (
	groupKeyChars = regexp.MustCompile(`[^A-Za-z0-9_]+`)
	groupPropName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,62}$`)
)

// GroupKey is a vRA property group's name as rvav's keys are: letters,
// digits and underscores, starting with a letter.
func GroupKey(name string) string {
	k := strings.Trim(groupKeyChars.ReplaceAllString(name, "_"), "_")
	switch {
	case k == "" || !(k[0] >= 'A' && k[0] <= 'Z' || k[0] >= 'a' && k[0] <= 'z'):
		k = "g_" + k
	case len(k) == 1:
		k += "_group"
	}
	if len(k) > 63 {
		k = strings.TrimRight(k[:63], "_")
	}
	return k
}

// GroupKeys are the rvav keys of the estate's property groups, by vRA ID:
// two of the same name (in two projects) get a number.
func GroupKeys(e *vra.Estate) map[string]string {
	groups := slices.Clone(e.PropertyGroups)
	slices.SortFunc(groups, func(a, b vra.PropertyGroup) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.ProjectID, b.ProjectID), strings.Compare(a.ID, b.ID))
	})
	out, taken := map[string]string{}, map[string]bool{}
	for _, g := range groups {
		base := GroupKey(g.Name)
		key := base
		for n := 2; taken[key]; n++ {
			suffix := fmt.Sprintf("_%d", n)
			key = base[:min(len(base), 63-len(suffix))] + suffix
		}
		taken[key], out[g.ID] = true, key
	}
	return out
}

// PlanPropertyGroups is what the import makes of vRA's property groups:
// a group is made when rvav has none of its key, and never changed.
func PlanPropertyGroups(e *vra.Estate, r Rvav) []GroupImport {
	keys := GroupKeys(e)
	out := []GroupImport{}
	for _, g := range e.PropertyGroups {
		gi := mapGroup(g, keys[g.ID])
		have, exists := r.PropertyGroups[gi.Key]
		switch {
		case !exists:
			gi.Action, gi.Version = ActionCreate, 1
		case sameGroup(have, gi):
			gi.Action, gi.Version = ActionUnchanged, have.Version
		default:
			gi.Action, gi.Version = ActionDiffers, have.Version
			gi.Notes = append(gi.Notes, Note{Warning, "", fmt.Sprintf("rvav has a property group %s that isn't what vRA's gives: "+
				"the import leaves it as it is, and items name its version %d. An item imported while the group had an older "+
				"version gets a new draft, even when its template hasn't changed.", gi.Key, have.Version)})
		}
		out = append(out, gi)
	}
	slices.SortFunc(out, func(a, b GroupImport) int { return strings.Compare(a.Key, b.Key) })
	return out
}

func mapGroup(g vra.PropertyGroup, key string) GroupImport {
	constant := strings.EqualFold(g.Type, "CONSTANT")
	gi := GroupImport{VRAID: g.ID, Name: g.Name, Key: key, Label: clip(cmp.Or(g.DisplayName, g.Name), 100), Type: "input",
		Inputs: []GroupInput{}, Constants: map[string]any{}, Notes: []Note{},
		Description: clip(cmp.Or(g.Description, "vRA's property group "+g.Name+"."), 500)}
	if constant {
		gi.Type = "constant"
	}
	note := func(level, where, text string) { gi.Notes = append(gi.Notes, Note{level, where, text}) }
	if g.ProjectID != "" {
		note(Info, "projectId", "A project's property group in vRA: rvav's are for every item.")
	}
	for _, name := range slices.Sorted(maps.Keys(g.Properties)) {
		p, where := g.Properties[name], "properties."+name
		if !groupPropName.MatchString(name) {
			note(Unmapped, where, "rvav names a group's properties with letters, digits and underscores, starting with a letter. Left out.")
			continue
		}
		if constant {
			mapConstant(name, p, gi.Constants, note)
			continue
		}
		if in, ok := mapGroupInput(name, p, slices.Contains(g.Required, name), note); ok {
			if len(gi.Inputs) == maxGroupInputs {
				note(Unmapped, where, fmt.Sprintf("rvav's property groups have at most %d inputs. Left out.", maxGroupInputs))
				continue
			}
			gi.Inputs = append(gi.Inputs, in)
		}
	}
	// What rvav stores is JSON: compared with rvav's group as it.
	b, _ := json.Marshal(gi.Inputs)
	_ = json.Unmarshal(b, &gi.Inputs)
	b, _ = json.Marshal(gi.Constants)
	_ = json.Unmarshal(b, &gi.Constants)
	return gi
}

func mapConstant(name string, p map[string]any, out map[string]any, note func(level, where, text string)) {
	where := "properties." + name
	if enc, _ := p["encrypted"].(bool); enc {
		note(Unmapped, where, "An encrypted constant, whose value vRA doesn't give: store it as a secret in rvav, and open it in cloud-init with {{ secret \"name\" }}.")
		return
	}
	v, ok := p["const"]
	if !ok {
		v, ok = p["default"]
	}
	switch v.(type) {
	case string, bool, int, int64, float64:
		out[name] = v
	case nil:
		if ok {
			note(Unmapped, where, "A constant without a value. Left out.")
		} else {
			note(Unmapped, where, "vRA gives it no value (const). Left out.")
		}
	default:
		note(Unmapped, where, "A list or a map: rvav's constants are text, numbers or booleans. Left out.")
	}
}

func mapGroupInput(name string, p map[string]any, required bool, note func(level, where, text string)) (GroupInput, bool) {
	where := "properties." + name
	title, _ := p["title"].(string)
	in := GroupInput{Name: name, Label: clip(cmp.Or(title, humanize(name)), 100), Required: required}
	in.Type, _ = p["type"].(string)
	switch in.Type {
	case "":
		in.Type = "string"
	case "string", "integer", "number", "boolean":
	default:
		note(Unmapped, where, fmt.Sprintf("An input of type %s: rvav's group inputs are text, numbers or booleans. Left out.", in.Type))
		return in, false
	}
	if d, _ := p["description"].(string); d != "" {
		in.Help = clip(d, 300)
	}
	var choices []any
	if l, ok := p["enum"].([]any); ok {
		choices = l
	}
	if l, ok := p["oneOf"].([]any); ok {
		for _, c := range l {
			if m, ok := c.(map[string]any); ok {
				choices = append(choices, m["const"])
			}
		}
		note(Info, where+".oneOf", "Its choices' titles aren't kept: the form shows their values.")
	}
	if len(choices) > 0 {
		for _, c := range choices {
			if s, ok := c.(string); ok && in.Type == "string" {
				in.Options = append(in.Options, s)
			}
		}
		if len(in.Options) != len(choices) {
			note(Unmapped, where+".enum", "Only a string input is chosen from options, each a text. Its choices are left out.")
			in.Options = nil
		}
	}
	if enc, _ := p["encrypted"].(bool); enc {
		if in.Type == "string" && len(in.Options) == 0 {
			in.Secret = true
			note(Info, where, "Encrypted in vRA: a secret input in rvav.")
		} else {
			note(Unmapped, where+".encrypted", "Only a string typed in can be secret in rvav: it isn't.")
		}
	}
	if def, ok := p["default"]; ok && def != nil {
		switch {
		case in.Secret:
			note(Info, where+".default", "A secret input has no default in rvav: the group would hold its value.")
		case !groupValueOf(def, in.Type) || len(in.Options) > 0 && !slices.Contains(in.Options, fmt.Sprint(def)):
			note(Unmapped, where+".default", fmt.Sprintf("Default %v isn't a %s of its choices. Left out.", def, in.Type))
		default:
			in.Default = def
		}
	}
	for _, k := range []string{"pattern", "minLength", "maxLength", "minimum", "maximum", "format", "readOnly"} {
		if _, ok := p[k]; ok {
			note(Unmapped, where+"."+k, k+": rvav's group inputs have no such check. Left out.")
		}
	}
	return in, true
}

// clip is s cut to n characters, as the API's limits count them.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func groupValueOf(v any, typ string) bool {
	switch x := v.(type) {
	case string:
		return typ == "string"
	case bool:
		return typ == "boolean"
	case int, int64:
		return typ == "integer" || typ == "number"
	case float64:
		return typ == "number" || typ == "integer" && x == math.Trunc(x)
	}
	return false
}

func sameGroup(have RvavGroup, gi GroupImport) bool {
	text := func(inputs []GroupInput, constants map[string]any) string {
		if inputs == nil {
			inputs = []GroupInput{}
		}
		if constants == nil {
			constants = map[string]any{}
		}
		b, _ := json.Marshal([]any{inputs, constants})
		return string(b)
	}
	return text(have.Inputs, have.Constants) == text(gi.Inputs, gi.Constants)
}

// groupScope is what a template's expressions read of property groups:
// those its inputs are, and those whose constants it reads.
type groupScope struct {
	estate    *vra.Estate
	projectID string
	keys      map[string]string
	// inputs are the template's inputs that are property groups, by
	// their names, to the groups' keys.
	inputs map[string]string
	// used are the groups the item names, by key.
	used map[string]bool
	// inlined are the constants printed in cloud-init.
	inlined []string
}

func newGroupScope(e *vra.Estate, projectID string) *groupScope {
	return &groupScope{estate: e, projectID: projectID, keys: GroupKeys(e), inputs: map[string]string{}, used: map[string]bool{}}
}

// constant is a constant group's value, as a ref.
func (gs *groupScope) constant(path string) (ref, error) {
	name, prop, _ := strings.Cut(strings.TrimPrefix(path, "propgroup."), ".")
	g := gs.estate.PropertyGroupFor(name, gs.projectID)
	if g == nil {
		return ref{}, fmt.Errorf("%s: there is no property group %s in what was read", path, name)
	}
	if !strings.EqualFold(g.Type, "CONSTANT") {
		return ref{}, fmt.Errorf("%s: property group %s holds inputs, not constants", path, name)
	}
	consts := map[string]any{}
	mapConstant(prop, g.Properties[prop], consts, func(string, string, string) {})
	v, ok := consts[prop]
	if !ok || !groupPropName.MatchString(prop) {
		return ref{}, fmt.Errorf("%s: property group %s has no constant %s rvav keeps", path, name, prop)
	}
	key := gs.keys[g.ID]
	gs.used[key] = true
	return ref{what: "propgroup", name: key + "." + prop, value: v}, nil
}

// refs are the property groups the item names, each at version 1 until
// the plan says which (withGroupVersions).
func (gs *groupScope) refs() []any {
	var out []any
	for _, key := range slices.Sorted(maps.Keys(gs.used)) {
		out = append(out, map[string]any{"key": key, "version": 1})
	}
	return out
}

// withGroupVersions makes an item's versions name the version of each
// property group the import makes or finds, and says when it finds one
// that differs from vRA's.
// namesGroup says whether the item's latest version names the group.
func namesGroup(doc *ItemDoc, key string) bool {
	if doc == nil || len(doc.Versions) == 0 {
		return false
	}
	refs, _ := doc.Versions[len(doc.Versions)-1].Blueprint["property_groups"].([]any)
	return slices.ContainsFunc(refs, func(r any) bool {
		m, _ := r.(map[string]any)
		return m["key"] == key
	})
}

func withGroupVersions(doc *ItemDoc, groups map[string]GroupImport, note func(level, where, text string)) {
	warned := map[string]bool{}
	for _, v := range doc.Versions {
		refs, _ := v.Blueprint["property_groups"].([]any)
		for _, r := range refs {
			m, _ := r.(map[string]any)
			key, _ := m["key"].(string)
			g, ok := groups[key]
			if !ok {
				continue
			}
			m["version"] = g.Version
			if g.Action == ActionDiffers && !warned[key] {
				warned[key] = true
				note(Warning, "property_groups."+key, fmt.Sprintf("Property group %s differs in rvav from vRA's %s: the item names its version %d. "+
					"Check it has the inputs and constants the item reads.", key, g.Name, g.Version))
			}
		}
	}
}
