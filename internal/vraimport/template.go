package vraimport

import (
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/vra"
)

// ItemPlan is what a cloud template becomes: a catalog item (as the YAML
// import takes it) and the notes on what didn't carry over.
type ItemPlan struct {
	BlueprintID string   `json:"blueprint_id"`
	Name        string   `json:"name"`
	Slug        string   `json:"slug"`
	Status      string   `json:"status"`
	Notes       []Note   `json:"notes"`
	Doc         *ItemDoc `json:"doc,omitempty"`
}

// tmpl is a cloud template's YAML: its inputs and resources.
type tmpl struct {
	Inputs    map[string]map[string]any `yaml:"inputs"`
	Resources map[string]struct {
		Type       string         `yaml:"type"`
		Properties map[string]any `yaml:"properties"`
	} `yaml:"resources"`
}

// Resource types rvav builds from; anything else is reported.
var (
	machineTypes = []string{vra.MachineType, "Cloud.Machine"}
	networkTypes = []string{"Cloud.vSphere.Network", "Cloud.Network"}
	diskTypes    = []string{"Cloud.vSphere.Disk", "Cloud.Volume"}
)

// The placement fields an imported item asks for, as rvav's own VM items
// do: vRA chose them by constraint tags, rvav by the requester's choice
// within what placement allows.
var placementFields = []string{"cluster", "datastore", "network"}

// MapTemplate turns a cloud template's released versions, and its custom
// form, into a catalog item. A template with no released version, or with
// no vSphere machine, is skipped.
func MapTemplate(e *vra.Estate, bp vra.Blueprint) ItemPlan {
	p := ItemPlan{BlueprintID: bp.ID, Name: bp.Name, Slug: slug(bp.Name)}
	note := func(level, where, text string) { p.Notes = append(p.Notes, Note{level, where, text}) }
	var released []vra.BlueprintVersion
	for _, v := range bp.Versions {
		if v.Status == "RELEASED" {
			released = append(released, v)
		}
	}
	if len(released) == 0 {
		note(Unmapped, "versions", "No released version: nothing is offered in vRA's catalog.")
		p.Status = Skipped
		return p
	}
	sort.SliceStable(released, func(i, j int) bool { return versionLess(released[i].Version, released[j].Version) })
	form := formFor(e, bp.ID)
	doc := &ItemDoc{APIVersion: "rvav.dev/v1", Kind: "CatalogItem", Metadata: ItemMeta{Slug: p.Slug, Name: bp.Name,
		Summary: firstLine(bp.Description, bp.Name), Description: bp.Description, Category: "compute", Type: "vm_blueprint",
		Icon: "server", Tags: []string{"vra"}}}
	worst := Complete
	for i, v := range released {
		latest := i == len(released)-1
		var vnotes []Note
		vnote := func(level, where, text string) {
			vnotes = append(vnotes, Note{level, where, text})
		}
		vd, ok := mapVersion(e, bp, v, form, latest, vnote)
		// The latest version's notes are the item's; earlier ones only say
		// whether they could be built.
		if latest {
			p.Notes = append(p.Notes, vnotes...)
		}
		if !ok {
			if latest {
				p.Status = Skipped
				return p
			}
			note(Warning, "versions", fmt.Sprintf("Version %s left out: it has no vSphere machine rvav can build.", v.Version))
			continue
		}
		if slices.ContainsFunc(vnotes, func(n Note) bool { return n.Level == Unmapped }) && latest {
			worst = Partial
		}
		doc.Versions = append(doc.Versions, vd)
	}
	p.Status, p.Doc = worst, doc
	if strings.Contains(strings.ToLower(doc.Versions[len(doc.Versions)-1].Blueprint["guest"].(map[string]any)["os_family"].(string)), "windows") {
		doc.Metadata.Icon = "monitor"
	}
	return p
}

// mapVersion maps one version; false when it has nothing rvav can build.
func mapVersion(e *vra.Estate, bp vra.Blueprint, v vra.BlueprintVersion, form *vra.Form, withForm bool,
	note func(level, where, text string)) (VersionDoc, bool) {
	var t tmpl
	if err := yaml.Unmarshal([]byte(v.Content), &t); err != nil {
		note(Unmapped, "content", "The template's YAML can't be read: "+err.Error())
		return VersionDoc{}, false
	}
	vd := VersionDoc{Number: versionNumber(v.Version), Changelog: changelog(bp, v)}
	props, fields, required, initial := map[string]any{}, map[string]any{}, []string{}, map[string]any{}
	order := slices.Sorted(maps.Keys(t.Inputs))
	// rvav asks every request for its environment: an input for it isn't a
	// field (its references are rvav's environment).
	order = slices.DeleteFunc(order, func(name string) bool {
		if environmentInput(name) {
			note(Info, "inputs."+name, "The environment: rvav asks for it with every request, so it isn't a field.")
			return true
		}
		return false
	})
	for _, name := range order {
		prop, field, req, def := mapInput(name, t.Inputs[name], note)
		if prop == nil {
			continue
		}
		props[name], fields[name] = prop, field
		if req {
			required = append(required, name)
		}
		if def != nil {
			initial[name] = def
		}
	}

	var machines, unmapped []string
	disks := map[string]map[string]any{}
	for _, name := range slices.Sorted(maps.Keys(t.Resources)) {
		r := t.Resources[name]
		switch {
		case slices.Contains(machineTypes, r.Type):
			machines = append(machines, name)
		case slices.Contains(diskTypes, r.Type):
			disks[name] = r.Properties
		case slices.Contains(networkTypes, r.Type):
			if nt, _ := r.Properties["networkType"].(string); nt != "" && nt != "existing" {
				note(Unmapped, "resources."+name, fmt.Sprintf("A %s network (networkType %s): rvav attaches VMs to existing networks only.", r.Type, nt))
			}
		default:
			unmapped = append(unmapped, name)
			note(Unmapped, "resources."+name, fmt.Sprintf("%s has no equivalent in rvav: it is left out. An AWX job after the VMs, in a composite item, can do what it did.", r.Type))
		}
	}
	if len(machines) == 0 {
		note(Unmapped, "resources", "No vSphere machine: rvav builds VMs, and this template builds none.")
		return VersionDoc{}, false
	}

	// Each machine is a component; one machine without a count is the
	// item's VM itself.
	sizes := map[string]any{}
	var comps []map[string]any
	var single map[string]any
	osFamily := "linux"
	for _, name := range machines {
		r := t.Resources[name]
		where := "resources." + name
		c := map[string]any{"key": slug(name), "title": name}
		bindings := map[string]any{}
		if img, _ := r.Properties["image"].(string); img != "" {
			if strings.Contains(img, "${") {
				note(Unmapped, where+".image", "The image comes from an input ("+img+"): an rvav item builds from one template per component. Make an item per image, or a size-like choice in a later version.")
			} else if tpl := imageTemplate(e, img); tpl != "" {
				c["template"] = tpl
				note(Info, where+".image", fmt.Sprintf("Image %s is vCenter template %s (vRA's image profile).", img, tpl))
			} else {
				c["template"] = img
				note(Warning, where+".image", "Image "+img+" isn't in any image profile read: set the vCenter template.")
			}
			if strings.Contains(strings.ToLower(img), "win") {
				osFamily = "windows"
			}
		} else {
			note(Unmapped, where+".image", "No image (imageRef or a cloned VM?): set the vCenter template.")
		}
		mapFlavor(e, r.Properties, t.Inputs, where, sizes, bindings, note)
		if spec, _ := r.Properties["customizationSpec"].(string); spec != "" {
			c["guest"] = map[string]any{"customization_spec": spec}
			note(Info, where+".customizationSpec", "vCenter customization specification "+spec+" kept.")
		}
		if ci, _ := r.Properties["cloudConfig"].(string); ci != "" {
			ud, problems := userData(ci)
			g, _ := c["guest"].(map[string]any)
			if g == nil {
				g = map[string]any{}
			}
			g["cloud_init"] = map[string]any{"user_data": ud}
			c["guest"] = g
			for _, pr := range problems {
				note(Warning, where+".cloudConfig", pr)
			}
		}
		var cDisks []map[string]any
		if ad, ok := r.Properties["attachedDisks"].([]any); ok {
			for _, a := range ad {
				src, _ := a.(map[string]any)["source"].(string)
				ref := resourceRef(src)
				if d, ok := disks[ref]; ok {
					cDisks = append(cDisks, map[string]any{"size_gb": d["capacityGb"]})
					delete(disks, ref)
				} else {
					note(Unmapped, where+".attachedDisks", "Disk "+src+" isn't a disk of this template: left out.")
				}
			}
		}
		if len(cDisks) > 0 {
			c["disks"] = cDisks
		}
		if cons, ok := r.Properties["constraints"].([]any); ok && len(cons) > 0 {
			var tags []string
			for _, x := range cons {
				if m, ok := x.(map[string]any); ok {
					tags = append(tags, fmt.Sprint(m["tag"]))
				}
			}
			note(Warning, where+".constraints", "vRA placed it by constraint tags ("+strings.Join(tags, ", ")+"): the item asks for cluster, datastore and network, within what rvav's placement rules allow. Write a placement rule for those tags.")
		}
		for _, k := range placementFields {
			bindings[k] = "inputs." + k
		}
		c["bindings"] = bindings
		if count, ok := r.Properties["count"]; ok {
			c["count"] = countExpr(count)
		}
		if len(machines) == 1 && c["count"] == nil {
			single = c
		}
		comps = append(comps, c)
	}
	for name := range disks {
		note(Unmapped, "resources."+name, "A disk attached to no machine: left out.")
	}

	// The placement inputs rvav's VM items ask for.
	for _, f := range placementFields {
		props[f] = map[string]any{"type": "string", "minLength": 1}
		required = append(required, f)
	}
	maps.Copy(fields, placementUI())

	blueprint := map[string]any{"kind": "vm", "naming": naming(e, bp, order, note),
		"guest": map[string]any{"os_family": osFamily, "domain": ""}}
	if len(sizes) > 0 {
		blueprint["sizes"] = sizes
	}
	if single != nil {
		for k, v := range single {
			switch k {
			case "key", "title":
			case "guest":
				g := blueprint["guest"].(map[string]any)
				maps.Copy(g, v.(map[string]any))
			default:
				blueprint[k] = v
			}
		}
	} else {
		for _, c := range comps {
			c["naming"] = "{project}-{environment}-" + strings.ReplaceAll(c["key"].(string), "-", "") + "{seq:02}"
		}
		blueprint["components"] = comps
		blueprint["deployment_name"] = "{project}-{environment}-" + slug(bp.Name)
		note(Info, "resources", fmt.Sprintf("%d machines: a multi-VM item, one component each (ADR-0024).", len(comps)))
	}
	if osFamily == "windows" {
		note(Warning, "guest", "A Windows guest: set its domain join (domain, OU and the secret of the joining account) in the blueprint.")
	}

	order = append(order, placementFields...)
	vd.FormSchema = map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object",
		"additionalProperties": false, "required": required, "properties": props}
	ui := map[string]any{"fields": fields, "layout": map[string]any{"kind": "page", "sections": []map[string]any{
		{"key": "inputs", "title": "Request", "columns": 2, "fields": slices.DeleteFunc(slices.Clone(order), func(f string) bool {
			return slices.Contains(placementFields, f)
		})},
		{"key": "placement", "title": "Placement and network", "columns": 2, "fields": placementFields},
	}}}
	if withForm && form != nil {
		applyForm(form, props, fields, &required, initial, ui, note)
		vd.FormSchema["required"] = required
	}
	vd.UISchema, vd.InitialValues, vd.Blueprint = ui, initial, blueprint
	return vd, true
}

// mapInput turns a template input into a form property and field; nil
// when it has no rvav equivalent.
func mapInput(name string, in map[string]any, note func(level, where, text string)) (prop, field map[string]any, required bool, def any) {
	where := "inputs." + name
	label, _ := in["title"].(string)
	if label == "" {
		label = humanize(name)
	}
	field = map[string]any{"label": label, "widget": "text"}
	if d, _ := in["description"].(string); d != "" {
		field["help"] = d
	}
	prop = map[string]any{}
	typ, _ := in["type"].(string)
	switch typ {
	case "string", "":
		prop["type"] = "string"
		for _, k := range []string{"pattern", "minLength", "maxLength"} {
			if v, ok := in[k]; ok {
				prop[k] = v
			}
		}
		switch in["format"] {
		case "date":
			field["widget"] = "date"
		case "date-time":
			field["widget"] = "datetime"
		}
		if enc, _ := in["encrypted"].(bool); enc {
			field["widget"], field["secret"] = "secret", true
			note(Info, where, "Encrypted in vRA: a secret field in rvav, sealed and given only to the automation that needs it.")
		}
	case "integer", "number":
		prop["type"] = typ
		for _, k := range []string{"minimum", "maximum"} {
			if v, ok := in[k]; ok {
				prop[k] = v
			}
		}
		field["widget"] = "number"
	case "boolean":
		prop["type"] = "boolean"
		field["widget"] = "toggle"
	default:
		note(Unmapped, where, fmt.Sprintf("An input of type %s: left out of the form.", typ))
		return nil, nil, false, nil
	}
	if e, ok := in["enum"].([]any); ok {
		var vals []any
		var opts []map[string]any
		for _, v := range e {
			vals = append(vals, v)
			opts = append(opts, map[string]any{"value": fmt.Sprint(v), "label": fmt.Sprint(v)})
		}
		prop["enum"], field["widget"], field["options"] = vals, "select", opts
	}
	if one, ok := in["oneOf"].([]any); ok {
		var vals []any
		var opts []map[string]any
		for _, o := range one {
			m, _ := o.(map[string]any)
			vals = append(vals, m["const"])
			opts = append(opts, map[string]any{"value": fmt.Sprint(m["const"]), "label": fmt.Sprint(m["title"])})
		}
		prop["enum"], field["widget"], field["options"] = vals, "select", opts
	}
	def = in["default"]
	// vRA asks for an input without a default; one with a default can be
	// left as it is.
	required = def == nil
	return prop, field, required, def
}

// mapFlavor sets the machine's CPU and memory: from a flavor (fixed, or
// chosen by an input whose choices are flavors) or from cpuCount and
// totalMemoryMB.
func mapFlavor(e *vra.Estate, props map[string]any, inputs map[string]map[string]any, where string, sizes, bindings map[string]any,
	note func(level, where, text string)) {
	if cpu, ok := props["cpuCount"]; ok {
		bindings["vcpu"] = expr(cpu)
		if mem, ok := props["totalMemoryMB"]; ok {
			if s := expr(mem); strings.HasPrefix(s, "inputs.") {
				bindings["memory_gb"] = s + " / 1024"
			} else if n, err := strconv.Atoi(s); err == nil {
				bindings["memory_gb"] = strconv.Itoa((n + 1023) / 1024)
			}
		}
		return
	}
	flavor, _ := props["flavor"].(string)
	if flavor == "" {
		note(Unmapped, where+".flavor", "No flavor, cpuCount or totalMemoryMB: set the VM's size.")
		return
	}
	if in := inputRef(flavor); in != "" {
		var names []string
		if def, ok := inputs[in]; ok {
			if en, ok := def["enum"].([]any); ok {
				for _, v := range en {
					names = append(names, fmt.Sprint(v))
				}
			}
		}
		if len(names) == 0 {
			note(Unmapped, where+".flavor", "The flavor comes from input "+in+", which lists no flavors: set the sizes.")
			return
		}
		for _, n := range names {
			if f, ok := flavorSize(e, n); ok {
				sizes[n] = map[string]any{"label": humanize(n), "vcpu": f.CPUCount, "memory_gb": (f.MemoryInMB + 1023) / 1024}
			} else {
				note(Warning, where+".flavor", "Flavor "+n+" isn't in any flavor profile read: set its size.")
			}
		}
		bindings["vcpu"] = "sizes[inputs." + in + "].vcpu"
		bindings["memory_gb"] = "sizes[inputs." + in + "].memory_gb"
		return
	}
	if f, ok := flavorSize(e, flavor); ok {
		bindings["vcpu"] = strconv.Itoa(f.CPUCount)
		bindings["memory_gb"] = strconv.Itoa((f.MemoryInMB + 1023) / 1024)
		return
	}
	note(Unmapped, where+".flavor", "Flavor "+flavor+" isn't in any flavor profile read: set the VM's size.")
}

// naming translates the project's machine naming template where it can.
func naming(e *vra.Estate, bp vra.Blueprint, inputs []string, note func(level, where, text string)) string {
	def := "{project}-{environment}-vm{seq:03}"
	if slices.Contains(inputs, "role") {
		def = "{project}-{environment}-{inputs.role}{seq:03}"
	}
	i := slices.IndexFunc(e.Projects, func(p vra.Project) bool { return p.ID == bp.ProjectID })
	if i < 0 || e.Projects[i].MachineNamingTemplate == "" {
		note(Info, "naming", "Named "+def+", or by the naming policy rvav offers.")
		return def
	}
	src := e.Projects[i].MachineNamingTemplate
	out := regexp.MustCompile(`\$\{(#+)\}`).ReplaceAllStringFunc(src, func(m string) string {
		return fmt.Sprintf("{seq:%02d}", strings.Count(m, "#"))
	})
	missing := false
	out = regexp.MustCompile(`\$\{input\.([A-Za-z_][A-Za-z0-9_]*)\}`).ReplaceAllStringFunc(out, func(m string) string {
		name := m[len("${input.") : len(m)-1]
		switch {
		case environmentInput(name):
			return "{environment}"
		case !slices.Contains(inputs, name):
			missing = true
		}
		return "{inputs." + name + "}"
	})
	if missing || strings.Contains(out, "${") || !strings.Contains(out, "{seq:") {
		note(Warning, "naming", "The project's naming template "+src+" can't be translated whole: named "+def+" instead.")
		return def
	}
	note(Info, "naming", "Named "+out+", from the project's naming template "+src+".")
	return out
}

var refPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

// userData translates cloud-init's vRA expressions into the Go template
// rvav renders, and lists those it can't.
func userData(src string) (string, []string) {
	var problems []string
	out := refPattern.ReplaceAllStringFunc(src, func(m string) string {
		ref := m[2 : len(m)-1]
		switch {
		case ref == "self.resourceName" || ref == "self.name":
			return "{{ .vm.name }}"
		case ref == "self.address":
			return "{{ .vm.ip }}"
		case strings.HasPrefix(ref, "input.") && environmentInput(strings.TrimPrefix(ref, "input.")):
			return "{{ .environment }}"
		case strings.HasPrefix(ref, "input."):
			return "{{ .inputs." + strings.TrimPrefix(ref, "input.") + " }}"
		}
		problems = append(problems, "cloud-init uses "+m+", which rvav doesn't give: it is left as it was.")
		return m
	})
	return out, problems
}

// applyForm takes a custom form's labels, help, choices, defaults,
// requirements and layout. Its dynamic parts (vRO actions, external
// sources, conditions) are reported.
func applyForm(f *vra.Form, props, fields map[string]any, required *[]string, initial, ui map[string]any,
	note func(level, where, text string)) {
	var def struct {
		Layout struct {
			Pages []struct {
				Title    string `json:"title"`
				Sections []struct {
					ID     string `json:"id"`
					Fields []struct {
						ID      string         `json:"id"`
						Display string         `json:"display"`
						State   map[string]any `json:"state"`
					} `json:"fields"`
				} `json:"sections"`
			} `json:"pages"`
		} `json:"layout"`
		Schema map[string]map[string]any `json:"schema"`
	}
	if err := json.Unmarshal([]byte(f.Form), &def); err != nil {
		note(Unmapped, "form", "The custom form can't be read: "+err.Error())
		return
	}
	widgets := map[string]string{"textField": "text", "textArea": "textarea", "dropDown": "select", "radio": "radio_cards",
		"checkbox": "toggle", "integerField": "number", "decimalField": "number", "passwordField": "secret", "datetime": "datetime",
		"multiSelect": "multiselect", "dualList": "multiselect", "tagField": "tags"}
	var sections []map[string]any
	seen := map[string]bool{}
	for pi, page := range def.Layout.Pages {
		for si, s := range page.Sections {
			var names []string
			for _, fd := range s.Fields {
				where := "form." + fd.ID
				field, ok := fields[fd.ID].(map[string]any)
				if !ok {
					if !environmentInput(fd.ID) {
						note(Info, where, "A field of the form that is no input of the template: left out.")
					}
					continue
				}
				names, seen[fd.ID] = append(names, fd.ID), true
				if w, ok := widgets[fd.Display]; ok {
					field["widget"] = w
					if w == "secret" {
						field["secret"] = true
					}
				} else if fd.Display != "" {
					note(Warning, where, "Shown as "+fd.Display+" in vRA, which rvav has no widget for: shown as "+fmt.Sprint(field["widget"])+".")
				}
				for k, v := range fd.State {
					if b, ok := v.(bool); ok {
						if k == "visible" && !b {
							note(Warning, where, "Hidden in vRA's form: shown in rvav's. Remove it from the form if nothing needs it.")
						}
						if k == "read-only" && b {
							note(Warning, where, "Read-only in vRA's form: editable in rvav's.")
						}
						continue
					}
					note(Unmapped, where, fmt.Sprintf("Its %s depends on other fields in vRA (a condition): write an rvav rule (CEL) for it.", k))
				}
				sch := def.Schema[fd.ID]
				if l, _ := sch["label"].(string); l != "" {
					field["label"] = l
				}
				if p, _ := sch["placeholder"].(string); p != "" {
					field["placeholder"] = p
				}
				if h, _ := sch["signpost"].(string); h != "" {
					field["help"] = h
				}
				if d, ok := sch["default"]; ok {
					initial[fd.ID] = d
				}
				prop, _ := props[fd.ID].(map[string]any)
				switch vl := sch["valueList"].(type) {
				case []any:
					var vals []any
					var opts []map[string]any
					for _, o := range vl {
						m, _ := o.(map[string]any)
						vals = append(vals, m["value"])
						opts = append(opts, map[string]any{"value": fmt.Sprint(m["value"]), "label": fmt.Sprint(m["label"])})
					}
					field["options"] = opts
					if prop != nil {
						prop["enum"] = vals
					}
				case map[string]any:
					note(Unmapped, where, fmt.Sprintf("Its choices come from %s %v in vRA: give the field an rvav value source (REST or CEL), or an AWX inventory.",
						vl["type"], vl["id"]))
				}
				if c, ok := sch["constraints"].(map[string]any); ok {
					if req, _ := c["required"].(bool); req && !slices.Contains(*required, fd.ID) {
						*required = append(*required, fd.ID)
					}
					if req, ok := c["required"].(bool); ok && !req {
						*required = slices.DeleteFunc(*required, func(r string) bool { return r == fd.ID })
					}
					if prop != nil {
						for from, to := range map[string]string{"pattern": "pattern", "max-length": "maxLength", "min-length": "minLength",
							"min-value": "minimum", "max-value": "maximum"} {
							if v, ok := c[from]; ok {
								prop[to] = v
							}
						}
					}
				}
				if _, ok := sch["valueList"]; !ok {
					if _, ok := sch["$data"]; ok {
						note(Unmapped, where, "Its value is computed in vRA ($data): give the field an rvav value source.")
					}
				}
			}
			if len(names) == 0 {
				continue
			}
			title := page.Title
			if len(page.Sections) > 1 {
				title = fmt.Sprint(fields[names[0]].(map[string]any)["label"])
			}
			sections = append(sections, map[string]any{"key": fmt.Sprintf("p%ds%d", pi+1, si+1), "title": title, "columns": 2, "fields": names})
		}
	}
	// Inputs the form doesn't show come after its sections.
	var rest []string
	for _, s := range ui["layout"].(map[string]any)["sections"].([]map[string]any) {
		for _, f := range s["fields"].([]string) {
			if !seen[f] && !slices.Contains(placementFields, f) {
				rest = append(rest, f)
			}
		}
	}
	if len(rest) > 0 {
		sections = append(sections, map[string]any{"key": "more", "title": "More", "columns": 2, "fields": rest})
	}
	sections = append(sections, map[string]any{"key": "placement", "title": "Placement and network", "columns": 2, "fields": placementFields})
	kind := "page"
	if len(def.Layout.Pages) > 1 {
		kind = "wizard"
	}
	ui["layout"] = map[string]any{"kind": kind, "sections": sections}
	note(Info, "form", "The custom form's labels, help, choices, defaults and order are the item's.")
}

// placementUI is rvav's own placement fields, as its VM items have them.
func placementUI() map[string]any {
	return map[string]any{
		"cluster": map[string]any{"widget": "vsphere_cluster", "label": "Cluster",
			"value_source": map[string]any{"kind": "builtin", "name": "vsphere.clusters"}, "depends_on": []string{"$environment"}},
		"datastore": map[string]any{"widget": "vsphere_datastore", "label": "Datastore",
			"value_source": map[string]any{"kind": "builtin", "name": "vsphere.datastores", "params": map[string]any{"cluster": "${inputs.cluster}"}},
			"depends_on":   []string{"$environment", "cluster"}},
		"network": map[string]any{"widget": "vsphere_network", "label": "Network",
			"value_source": map[string]any{"kind": "builtin", "name": "vsphere.networks", "params": map[string]any{"cluster": "${inputs.cluster}"}},
			"depends_on":   []string{"$environment", "cluster"}},
	}
}

// environmentInput: the name vRA templates give the environment.
func environmentInput(name string) bool {
	return name == "environment" || name == "env"
}

func imageTemplate(e *vra.Estate, image string) string {
	for _, p := range e.Images {
		if m, ok := p.ImageMappings.Mapping[image]; ok && m.Name != "" {
			return m.Name
		}
	}
	return ""
}

func flavorSize(e *vra.Estate, name string) (vra.FlavorMapping, bool) {
	for _, p := range e.Flavors {
		if m, ok := p.FlavorMappings.Mapping[name]; ok && m.CPUCount > 0 {
			return m, true
		}
	}
	return vra.FlavorMapping{}, false
}

func formFor(e *vra.Estate, blueprintID string) *vra.Form {
	for i, f := range e.Forms {
		if f.SourceID == blueprintID && f.SourceType == "com.vmw.blueprint" && f.Type == "requestForm" && f.Status != "OFF" {
			return &e.Forms[i]
		}
	}
	return nil
}

// inputRef is the input a ${input.x} expression names, or "".
func inputRef(s string) string {
	if m := regexp.MustCompile(`^\$\{input\.([A-Za-z_][A-Za-z0-9_]*)\}$`).FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// resourceRef is the resource a ${resource.x.id} expression names, or "".
func resourceRef(s string) string {
	if m := regexp.MustCompile(`^\$\{resource\.([A-Za-z0-9_]+)\.id\}$`).FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

// expr is a property as an rvav binding: a number, or the input it names.
func expr(v any) string {
	if s, ok := v.(string); ok {
		if in := inputRef(s); in != "" {
			return "inputs." + in
		}
		return s
	}
	return fmt.Sprint(v)
}

func countExpr(v any) any {
	if s, ok := v.(string); ok {
		if in := inputRef(s); in != "" {
			return "inputs." + in
		}
	}
	return v
}

// versionNumber is vRA's version as rvav's major.minor.
func versionNumber(v string) string {
	if regexp.MustCompile(`^\d+$`).MatchString(v) {
		return v + ".0"
	}
	if m := regexp.MustCompile(`^(\d+)\.(\d+)`).FindStringSubmatch(v); m != nil {
		return m[1] + "." + m[2]
	}
	return v
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(versionNumber(a), "."), strings.Split(versionNumber(b), ".")
	for i := range min(len(pa), len(pb)) {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

func changelog(bp vra.Blueprint, v vra.BlueprintVersion) string {
	s := "Imported from vRA cloud template " + bp.Name + " version " + v.Version
	if v.VersionDescription != "" {
		s += ": " + v.VersionDescription
	}
	return s + "."
}

func firstLine(s, def string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if s == "" {
		return def + " (from vRA)."
	}
	return s
}

func humanize(s string) string {
	s = regexp.MustCompile(`([a-z])([A-Z])`).ReplaceAllString(s, "$1 $2")
	s = strings.NewReplacer("_", " ", "-", " ").Replace(s)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + strings.ToLower(s[1:])
}
