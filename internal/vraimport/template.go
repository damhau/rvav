package vraimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/blueprint"
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
	// Templates are the catalogue templates it binds, by key (ADR-0037).
	Templates []string `json:"-"`
	// Secrets are the stored secrets its latest version's cloud-init
	// opens, as rvav names them.
	Secrets []string `json:"-"`
}

// tmpl is a cloud template's YAML: its inputs and resources.
type tmpl struct {
	Inputs    map[string]map[string]any `yaml:"inputs"`
	Resources map[string]struct {
		Type       string         `yaml:"type"`
		DependsOn  []string       `yaml:"dependsOn"`
		Properties map[string]any `yaml:"properties"`
		// vRA's resource flags (preventDelete, ignoreChanges...).
		Flags map[string]any `yaml:",inline"`
	} `yaml:"resources"`
}

// Resource types rvav builds from; anything else is reported.
var (
	machineTypes = []string{vra.MachineType, "Cloud.Machine"}
	networkTypes = []string{"Cloud.vSphere.Network", "Cloud.Network", "Cloud.NSX.Network"}
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
	p := ItemPlan{BlueprintID: bp.ID, Name: bp.Name, Slug: TemplateSlugs(e)[bp.ID]}
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
	taken := map[string]bool{}
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
		// rvav numbers versions major.minor: vRA's 1.0.1 and 1.0.2 take the
		// next free minor.
		if n := freeNumber(vd.Number, taken); n != vd.Number {
			note(Info, "versions", fmt.Sprintf("vRA's version %s is %s in rvav, which numbers versions major.minor.", v.Version, n))
			vd.Number = n
		}
		taken[vd.Number] = true
		doc.Versions = append(doc.Versions, vd)
	}
	p.Status, p.Doc = worst, doc
	p.Templates = boundTemplates(doc.Versions[len(doc.Versions)-1].Blueprint)
	p.Secrets = templateSecrets(released[len(released)-1].Content)
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
	// An input that is a property group: the item names the group, whose
	// inputs are the form's (ADR-0038).
	gs := newGroupScope(e, bp.ProjectID)
	order = slices.DeleteFunc(order, func(name string) bool {
		ref, _ := t.Inputs[name]["$ref"].(string)
		if !strings.HasPrefix(ref, groupRefPrefix) {
			return false
		}
		gname := strings.TrimPrefix(ref, groupRefPrefix)
		g := e.PropertyGroupFor(gname, bp.ProjectID)
		if g == nil {
			note(Unmapped, "inputs."+name, fmt.Sprintf("Property group %s isn't in what was read from vRA. Left out.", gname))
			return true
		}
		key := gs.keys[g.ID]
		gs.inputs[name], gs.used[key] = key, true
		note(Info, "inputs."+name, fmt.Sprintf("Property group %s is rvav's %s: the item names it, and its inputs are in the form, "+
			"in their own section. ${input.%s.x} is inputs.x.", gname, key, name))
		return true
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
			// Those made for each deployment are NSX segments (mapNSX).
			if nt, _ := r.Properties["networkType"].(string); nt != "" && nt != "existing" && nt != "public" && !slices.Contains(onDemand, nt) {
				note(Unmapped, "resources."+name, fmt.Sprintf("A %s network (networkType %s): rvav has no such network.", r.Type, nt))
			}
		case r.Type == "Cloud.SecurityGroup":
			// mapNSX
		default:
			unmapped = append(unmapped, name)
			note(Unmapped, "resources."+name, fmt.Sprintf("%s has no equivalent in rvav: it is left out. An AWX job after the VMs, in a composite item, can do what it did.", r.Type))
		}
	}
	if len(machines) == 0 {
		note(Unmapped, "resources", "No vSphere machine: rvav builds VMs, and this template builds none.")
		return VersionDoc{}, false
	}

	nx := mapNSX(t, note)
	machineRefs, keys := map[string]machineRef{}, map[string]bool{}
	for _, name := range machines {
		_, many := t.Resources[name].Properties["count"]
		machineRefs[name] = machineRef{key: resourceKey(name, "vm", keys), many: many}
	}
	// Constraint tags are rvav's constraints (ADR-0035): an existing
	// network's are those of the machines whose first adapter is on it.
	cs := scope{machines: machineRefs, environment: true, groups: gs}
	netConstraints := map[string][]any{}
	for _, name := range slices.Sorted(maps.Keys(t.Resources)) {
		r := t.Resources[name]
		if !slices.Contains(networkTypes, r.Type) || r.Properties["constraints"] == nil {
			continue
		}
		if _, onSegment := nx.segments[name]; onSegment {
			note(Info, "resources."+name+".constraints", "Its constraint tags chose vRA's network profile: rvav's placement rule says where the segment is made.")
			continue
		}
		netConstraints[name] = cs.constraintsOf(r.Properties["constraints"], "resources."+name+".constraints", note)
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
		sc := scope{machines: machineRefs, self: name, groups: gs}
		c := map[string]any{"key": machineRefs[name].key, "title": name}
		bindings := map[string]any{}
		if img, _ := r.Properties["image"].(string); img != "" {
			if strings.Contains(img, "${") {
				note(Unmapped, where+".image", "The image comes from an input ("+img+"): an rvav item builds from one template per component. Make an item per image, or a size-like choice in a later version.")
			} else if needsCatalogue(e, img) {
				key := ImageKey(img)
				bindings["template"] = strconv.Quote(key)
				note(Info, where+".image", fmt.Sprintf("Image %s is catalogue template %s, made of vRA's image mappings: a template per vCenter, with their cloud-init and constraints (ADR-0037).", img, key))
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
			ud, problems := sc.userData(ci)
			g, _ := c["guest"].(map[string]any)
			if g == nil {
				g = map[string]any{}
			}
			g["cloud_init"] = map[string]any{"user_data": ud}
			c["guest"] = g
			for _, pr := range problems {
				note(Warning, where+".cloudConfig", pr)
			}
			for _, c := range gs.inlined {
				note(Info, where+".cloudConfig", fmt.Sprintf("${%s} is the constant's value in vRA, printed in the user data: "+
					"rvav's cloud-init doesn't read property groups, so change it here too when the group changes.", c))
			}
			gs.inlined = nil
			for _, name := range secretsIn(ci) {
				note(Warning, where+".cloudConfig", fmt.Sprintf("cloud-init opens secret %s: store it in rvav under that name "+
					"(Catalog items › Secrets) before importing, since vRA never gives a secret's value.", name))
			}
		}
		mapFlags(r.Flags, where, c, note)
		mapFolderAndProperties(sc, r.Properties, where, c, note)
		var cDisks []map[string]any
		if ad, ok := r.Properties["attachedDisks"].([]any); ok {
			for _, a := range ad {
				src, _ := a.(map[string]any)["source"].(string)
				ref := resourceRef(src)
				if d, ok := disks[ref]; ok {
					disk := map[string]any{"size_gb": d["capacityGb"]}
					if l := cs.constraintsOf(d["constraints"], "resources."+ref+".constraints", note); len(l) > 0 {
						disk["constraints"] = l
					}
					if pt, ok := d["provisioningType"]; ok {
						if p := diskProvisioning(pt); p != "" {
							disk["provisioning"] = p
						} else {
							note(Unmapped, "resources."+ref+".provisioningType", fmt.Sprintf(
								"Provisioning %v isn't thin, thick or eagerZeroedThick: left out, the disk is provisioned as the template's first disk.", pt))
						}
					}
					cDisks = append(cDisks, disk)
					delete(disks, ref)
				} else {
					note(Unmapped, where+".attachedDisks", "Disk "+src+" isn't a disk of this template: left out.")
				}
			}
		}
		if len(cDisks) > 0 {
			c["disks"] = cDisks
		}
		for _, k := range placementFields {
			bindings[k] = "inputs." + k
		}
		nx.machine(r.Properties, where, c, bindings, note)
		placeByConstraints(cs, r.Properties, netConstraints, where, c, bindings, note)
		sc.tags(r.Properties["tags"], where+".tags", c, bindings, note)
		c["bindings"] = bindings
		if count, ok := r.Properties["count"]; ok {
			if n, err := sc.count(count); err != nil {
				note(Unmapped, where+".count", fmt.Sprintf("The count %v: %v. It is left out: one VM.", count, err))
			} else {
				c["count"] = n
			}
		}
		mapDependsOn(t, name, machineRefs, unmapped, c, note)
		if len(machines) == 1 && c["count"] == nil {
			single = c
		}
		comps = append(comps, c)
	}
	for name := range disks {
		note(Unmapped, "resources."+name, "A disk attached to no machine: left out.")
	}

	// The placement inputs rvav's VM items ask for, those a VM reads: no
	// network when every VM is on a segment made for the deployment, none
	// when constraint tags place them all.
	var placed []string
	for _, f := range placementFields {
		if slices.ContainsFunc(comps, func(c map[string]any) bool { return c["bindings"].(map[string]any)[f] == "inputs."+f }) {
			placed = append(placed, f)
		}
	}
	placementUI := placementUI()
	for _, f := range placed {
		props[f] = map[string]any{"type": "string", "minLength": 1}
		required = append(required, f)
		fields[f] = placementUI[f]
	}

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
			c["naming"] = "{project}-{environment}-" + c["key"].(string) + "{seq:02}"
		}
		blueprint["components"] = comps
		blueprint["deployment_name"] = "{project}-{environment}-" + slug(bp.Name)
		note(Info, "resources", fmt.Sprintf("%d machines: a multi-VM item, one component each (ADR-0024).", len(comps)))
	}
	nx.apply(blueprint)
	if refs := gs.refs(); len(refs) > 0 {
		blueprint["property_groups"] = refs
	}
	if osFamily == "windows" {
		note(Warning, "guest", "A Windows guest: set its domain join (domain, OU and the secret of the joining account) in the blueprint.")
	}

	order = append(order, placed...)
	vd.FormSchema = map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object",
		"additionalProperties": false, "required": required, "properties": props}
	sections := []map[string]any{{"key": "inputs", "title": "Request", "columns": 2,
		"fields": slices.DeleteFunc(slices.Clone(order), func(f string) bool { return slices.Contains(placed, f) || fields[f] == nil })}}
	if len(placed) > 0 {
		sections = append(sections, map[string]any{"key": "placement", "title": "Placement and network", "columns": 2, "fields": placed})
	}
	ui := map[string]any{"fields": fields, "layout": map[string]any{"kind": "page", "sections": sections}}
	if withForm && form != nil {
		applyForm(form, props, fields, &required, initial, ui, note)
		vd.FormSchema["required"] = required
	}
	vd.UISchema, vd.InitialValues, vd.Blueprint = ui, initial, blueprint
	return vd, true
}

// placeByConstraints maps a machine's constraint tags, its storage's and
// its first network's. With them, rvav places its VMs within its placement
// rules and the form asks for no cluster, datastore or network; unless the
// first network is an existing one without constraints, which rvav can't
// choose (ADR-0035).
func placeByConstraints(cs scope, props map[string]any, netConstraints map[string][]any, where string, c, bindings map[string]any,
	note func(level, where, text string)) {
	cons := map[string]any{}
	if l := cs.constraintsOf(props["constraints"], where+".constraints", note); len(l) > 0 {
		cons["compute"] = l
	}
	if st, ok := props["storage"].(map[string]any); ok {
		if l := cs.constraintsOf(st["constraints"], where+".storage.constraints", note); len(l) > 0 {
			cons["storage"] = l
		}
	}
	if nets, _ := props["networks"].([]any); len(nets) > 0 {
		n, _ := nets[0].(map[string]any)
		if l := netConstraints[resourceRef(fmt.Sprint(n["network"]))]; len(l) > 0 {
			cons["network"] = l
		}
	}
	disks, _ := c["disks"].([]map[string]any)
	ownDisks := slices.ContainsFunc(disks, func(d map[string]any) bool { return d["constraints"] != nil })
	if len(cons) == 0 && !ownDisks {
		return
	}
	if len(cons) > 0 {
		c["constraints"] = cons
	}
	_, byNetwork := cons["network"]
	if !byNetwork && !strings.HasPrefix(fmt.Sprint(bindings["network"]), "segments.") {
		note(Warning, where+".constraints", "Its network has no constraint tags, and rvav can't choose one without: the form asks for cluster, datastore and network, checked against the constraints.")
		return
	}
	delete(bindings, "cluster")
	delete(bindings, "datastore")
	if byNetwork {
		delete(bindings, "network")
	}
	note(Info, where+".constraints", "Placed by its constraint tags, within what rvav's placement rules allow: the form asks for no cluster, datastore or network.")
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
		copyKeys(in, prop, "pattern", "minLength", "maxLength")
		switch in["format"] {
		case "date":
			field["widget"], prop["format"] = "date", "date"
		case "date-time":
			field["widget"], prop["format"] = "datetime", "date-time"
		}
		if enc, _ := in["encrypted"].(bool); enc {
			field["widget"], field["secret"] = "secret", true
			note(Info, where, "Encrypted in vRA: a secret field in rvav, sealed and given only to the automation that needs it.")
		}
	case "integer", "number":
		prop["type"] = typ
		copyKeys(in, prop, "minimum", "maximum")
		field["widget"] = "number"
	case "boolean":
		prop["type"] = "boolean"
		field["widget"] = "toggle"
	case "array":
		if !mapList(where, in, prop, field, note) {
			return nil, nil, false, nil
		}
	default:
		note(Unmapped, where, fmt.Sprintf("An input of type %s: left out of the form.", typ))
		return nil, nil, false, nil
	}
	if vals, opts := choices(in); vals != nil && typ != "array" {
		prop["enum"], field["widget"], field["options"] = vals, "select", opts
	}
	def = in["default"]
	// vRA asks for an input without a default; one with a default can be
	// left as it is.
	required = def == nil
	if field["secret"] == true && def != nil {
		def, required = nil, true
		note(Warning, where, secretDefault)
	}
	return prop, field, required, def
}

// secretDefault is the note on a secret's default, which the import drops.
const secretDefault = "Its default isn't kept, since anyone who reads the item would see it: the requester types the secret."

// choices are an input's values and options: its oneOf (with titles), or
// its enum.
func choices(in map[string]any) (vals []any, opts []map[string]any) {
	if one, ok := in["oneOf"].([]any); ok {
		for _, o := range one {
			m, _ := o.(map[string]any)
			label, _ := m["title"].(string)
			if label == "" {
				label = fmt.Sprint(m["const"])
			}
			vals = append(vals, m["const"])
			opts = append(opts, map[string]any{"value": fmt.Sprint(m["const"]), "label": label})
		}
		return vals, opts
	}
	e, _ := in["enum"].([]any)
	for _, v := range e {
		vals = append(vals, v)
		opts = append(opts, map[string]any{"value": fmt.Sprint(v), "label": fmt.Sprint(v)})
	}
	return vals, opts
}

// mapList types a list input: a multi-select when its items are text with
// choices, tags when they are text, a table when they are objects.
func mapList(where string, in, prop, field map[string]any, note func(level, where, text string)) bool {
	items, _ := in["items"].(map[string]any)
	typ, _ := items["type"].(string)
	prop["type"] = "array"
	copyKeys(in, prop, "minItems", "maxItems", "uniqueItems")
	switch typ {
	case "string", "":
		it := map[string]any{"type": "string"}
		prop["items"] = it
		if vals, opts := choices(items); vals != nil {
			it["enum"], field["widget"], field["options"] = vals, "multiselect", opts
		} else {
			field["widget"] = "tags"
		}
		return true
	case "object":
		cols, rows, why := mapRows(items)
		if why != "" {
			note(Unmapped, where, why)
			return false
		}
		prop["items"], field["widget"], field["columns"] = rows, "datagrid", cols
		return true
	}
	note(Unmapped, where, fmt.Sprintf("A list of %s values: rvav's lists hold text (tags or a multi-select) or rows (a table). Left out of the form.", typ))
	return false
}

// mapRows are a table's columns and its rows' schema, from a list's object
// items: a column per property of text, a number or a boolean. why says
// what rvav can't show.
func mapRows(items map[string]any) (cols []map[string]any, rows map[string]any, why string) {
	cells, _ := items["properties"].(map[string]any)
	if len(cells) == 0 {
		return nil, nil, "A list of objects without properties: rvav's table needs a column per property. Left out of the form."
	}
	props := map[string]any{}
	for _, k := range slices.Sorted(maps.Keys(cells)) {
		c, _ := cells[k].(map[string]any)
		label, _ := c["title"].(string)
		if label == "" {
			label = humanize(k)
		}
		col, cell := map[string]any{"key": k, "label": label}, map[string]any{"type": c["type"]}
		switch c["type"] {
		case "string", nil:
			cell["type"], col["widget"] = "string", "text"
			copyKeys(c, cell, "pattern", "minLength", "maxLength")
			if vals, opts := choices(c); vals != nil {
				cell["enum"], col["widget"], col["options"] = vals, "select", opts
			}
		case "integer", "number":
			col["widget"] = "number"
			copyKeys(c, cell, "minimum", "maximum")
		case "boolean":
			col["widget"] = "toggle"
		default:
			return nil, nil, fmt.Sprintf("Its rows' %s is of type %v: rvav's table columns hold text, numbers, booleans or choices. Left out of the form.", k, c["type"])
		}
		props[k], cols = cell, append(cols, col)
	}
	rows = map[string]any{"type": "object", "properties": props}
	if r, ok := items["required"]; ok {
		rows["required"] = r
	}
	return cols, rows, ""
}

// copyKeys copies those of keys that from has to to.
func copyKeys(from, to map[string]any, keys ...string) {
	for _, k := range keys {
		if v, ok := from[k]; ok {
			to[k] = v
		}
	}
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
	src, from := "", "the project's naming template"
	if i := slices.IndexFunc(e.Projects, func(p vra.Project) bool { return p.ID == bp.ProjectID }); i >= 0 {
		src = e.Projects[i].MachineNamingTemplate
	}
	// Custom naming, when it has a pattern for machines, wins over the
	// project's own template, as in vRA.
	if cn, t := e.MachineNaming(bp.ProjectID); t != nil && t.Pattern != "" {
		src, from = t.Pattern, "custom naming "+cn.Name
		if t.StartCounter != 1 || t.IncrementStep != 1 {
			note(Unmapped, "naming", fmt.Sprintf("Custom naming %s counts from %d by %d; an item's naming counts from 1 by 1. "+
				"To keep vRA's numbers, make a naming policy for the project (Policies › Naming): the item's naming as its template, Start at %d and Increment %d.",
				cn.Name, t.StartCounter, t.IncrementStep, t.StartCounter, t.IncrementStep))
		}
	}
	if src == "" {
		note(Info, "naming", "Named "+def+", or by the naming policy rvav offers.")
		return def
	}
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
		note(Warning, "naming", strings.ToUpper(from[:1])+from[1:]+" "+src+" can't be translated whole: named "+def+" instead.")
		return def
	}
	note(Info, "naming", "Named "+out+", from "+from+" "+src+".")
	return out
}

// mapDependsOn maps a machine's dependsOn: the machines it names are
// components it is built after. rvav makes networks, disks and security
// groups before any VM.
func mapDependsOn(t tmpl, name string, machines map[string]machineRef, unmapped []string, c map[string]any,
	note func(level, where, text string)) {
	var deps []string
	where := "resources." + name + ".dependsOn"
	for _, d := range t.Resources[name].DependsOn {
		r, ok := t.Resources[d]
		switch {
		case machines[d].key != "":
			deps = append(deps, machines[d].key)
		case !ok:
			note(Unmapped, where, "It depends on "+d+", which the template doesn't have: left out.")
		case slices.Contains(unmapped, d):
			note(Unmapped, where, "It depends on "+d+" ("+r.Type+"), which is left out: an AWX job before it, in a composite item, can do what it did.")
		}
	}
	if len(deps) > 0 {
		c["depends_on"] = deps
		note(Info, where, "Built after "+strings.Join(deps, ", ")+" (depends_on).")
	}
}

// fitWidget is the rvav widget for a vRA form's, if it can edit the input
// as rvav typed it: vRA's date and time picker is a date picker on a date
// input, and gives any other text input the date-time format. Only list
// widgets edit a list.
func fitWidget(w string, prop map[string]any) (string, bool) {
	if prop == nil {
		return w, true
	}
	items, _ := prop["items"].(map[string]any)
	switch w {
	case "datetime":
		if prop["type"] != "string" {
			return w, false
		}
		if prop["format"] == "date" {
			return "date", true
		}
		prop["format"] = "date-time"
	case "number":
		return w, prop["type"] == "integer" || prop["type"] == "number"
	case "toggle":
		return w, prop["type"] == "boolean"
	case "secret":
		return w, prop["type"] == "string"
	case "multiselect", "tags":
		if prop["type"] != "array" {
			return w, false
		}
		if items == nil {
			prop["items"] = map[string]any{"type": "string"}
			return w, true
		}
		return w, items["type"] == "string"
	case "datagrid":
		return w, prop["type"] == "array" && items["type"] == "object"
	}
	return w, prop["type"] != "array"
}

// formColumns orders and labels a table's columns as the form's datagrid
// does.
func formColumns(field, sch map[string]any) {
	typ, _ := sch["type"].(map[string]any)
	defs, _ := typ["fields"].([]any)
	cols, _ := field["columns"].([]map[string]any)
	var out []map[string]any
	for _, d := range defs {
		m, _ := d.(map[string]any)
		i := slices.IndexFunc(cols, func(c map[string]any) bool { return c["key"] == m["id"] })
		if i < 0 {
			continue
		}
		if l, _ := m["label"].(string); l != "" {
			cols[i]["label"] = l
		}
		out, cols = append(out, cols[i]), slices.Delete(cols, i, i+1)
	}
	field["columns"] = append(out, cols...)
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
		"multiSelect": "multiselect", "dualList": "multiselect", "tagField": "tags", "datagrid": "datagrid"}
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
				prop, _ := props[fd.ID].(map[string]any)
				// A secret's default would be readable in the item.
				noDefault := func() {
					delete(initial, fd.ID)
					if !slices.Contains(*required, fd.ID) {
						*required = append(*required, fd.ID)
						note(Warning, where, secretDefault)
					}
				}
				w, ok := widgets[fd.Display]
				if fit, fits := fitWidget(w, prop); ok && fits {
					field["widget"] = fit
					if fit == "secret" {
						field["secret"] = true
						if _, ok := initial[fd.ID]; ok {
							noDefault()
						}
					}
				} else if ok {
					note(Warning, where, fmt.Sprintf("Shown as %s in vRA, which doesn't fit an input of type %v in rvav: shown as %v.",
						fd.Display, prop["type"], field["widget"]))
				} else if fd.Display != "" {
					note(Unmapped, where, "Shown as "+fd.Display+" in vRA, which rvav has no widget for: shown as "+fmt.Sprint(field["widget"])+".")
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
				if d, ok := sch["default"]; ok && field["secret"] == true {
					noDefault()
				} else if ok {
					initial[fd.ID] = d
				}
				if field["widget"] == "datagrid" {
					formColumns(field, sch)
				}
				switch vl := sch["valueList"].(type) {
				case []any:
					if field["widget"] == "datagrid" {
						break
					}
					var vals []any
					var opts []map[string]any
					for _, o := range vl {
						m, _ := o.(map[string]any)
						vals = append(vals, m["value"])
						opts = append(opts, map[string]any{"value": fmt.Sprint(m["value"]), "label": fmt.Sprint(m["label"])})
					}
					field["options"] = opts
					switch {
					case prop == nil || field["widget"] == "tags":
						// A tag field's choices are suggestions.
					case field["widget"] == "multiselect":
						if items, ok := prop["items"].(map[string]any); ok {
							items["enum"] = vals
						}
					default:
						prop["enum"] = vals
					}
				case map[string]any:
					text := fmt.Sprintf("Its choices come from %s %v in vRA: give the field an rvav value source (REST or CEL), or an AWX inventory.",
						vl["type"], vl["id"])
					if field["widget"] == "multiselect" && field["options"] == nil {
						field["widget"] = "tags"
						text += " Until then, it is tags, which take any value typed: make it a multi-select again once it has a source."
					}
					note(Unmapped, where, text)
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
	var placement map[string]any
	for _, s := range ui["layout"].(map[string]any)["sections"].([]map[string]any) {
		if s["key"] == "placement" {
			placement = s
			continue
		}
		for _, f := range s["fields"].([]string) {
			if !seen[f] {
				rest = append(rest, f)
			}
		}
	}
	if len(rest) > 0 {
		sections = append(sections, map[string]any{"key": "more", "title": "More", "columns": 2, "fields": rest})
	}
	if placement != nil {
		sections = append(sections, placement)
	}
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

// boundTemplates are the catalogue templates a blueprint's template
// bindings name, the components' included.
// mapFlags maps a machine's resource flags: preventDelete keeps its VMs
// from deletion (ADR-0038); rvav has none of the others.
func mapFlags(flags map[string]any, where string, c map[string]any, note func(level, where, text string)) {
	for _, k := range slices.Sorted(maps.Keys(flags)) {
		v := flags[k]
		switch {
		case k == "preventDelete" && v == true:
			c["prevent_delete"] = true
			note(Info, where+"."+k, "Its VMs are kept from deletion: an administrator runs the deployment's Allow deletion first.")
		case k == "preventDelete" && v == false:
		case k == "preventDelete":
			note(Unmapped, where+"."+k, fmt.Sprintf("preventDelete %v: rvav takes true or false, not an expression. Left out.", v))
		case k == "metadata":
			// The canvas layout of vRA's designer.
		case strings.HasSuffix(k, "Timeout"):
			note(Info, where+"."+k, fmt.Sprintf("%s %v left out: rvav's own timeouts apply.", k, v))
		default:
			note(Unmapped, where+"."+k, fmt.Sprintf("Resource flag %s has no rvav equivalent: left out.", k))
		}
	}
}

// machineProps are the properties of vRA's vSphere machine schema; any
// other property of a machine is a custom property.
var machineProps = []string{"name", "description", "image", "imageRef", "flavor", "flavorRef", "cpuCount", "coreCount",
	"totalMemoryMB", "cloudConfig", "cloudConfigSettings", "customizationSpec", "customizeGuestOs", "networks",
	"attachedDisks", "storage", "constraints", "tags", "count", "folderName", "resourceGroupName", "remoteAccess",
	"snapshotLimit", "ovfProperties", "imageDiskConstraints", "cloneFromSnapshot", "linkedClone", "enableDiskUuid",
	"cpuLimit", "cpuReservation", "cpuShares", "cpuSharesLevel", "memoryLimitMb", "memoryReservationMb", "memoryShares",
	"memorySharesLevel", "deploymentName", "hostName", "provisioningType", "storagePolicy"}

// mapFolderAndProperties maps a machine's folderName to its folder and
// its custom properties to rvav's, written to the VMs as vSphere custom
// attributes (ADR-0038).
func mapFolderAndProperties(sc scope, props map[string]any, where string, c map[string]any, note func(level, where, text string)) {
	sc.environment = true
	if raw, ok := props["folderName"]; ok {
		folder, err := textOf(sc, raw)
		if err == nil {
			if msg := blueprint.CheckFolder(folder); msg != "" {
				err = errors.New(msg)
			}
		}
		if err != nil {
			note(Unmapped, where+".folderName", fmt.Sprintf("Folder %v: %v. Left out: the placement rule's folder.", raw, err))
		} else {
			c["folder"] = folder
			note(Info, where+".folderName", "The VMs' folder, "+folder+", below the VM folder of the placement rule folder's datacenter.")
		}
	}
	custom := map[string]string{}
	for _, k := range slices.Sorted(maps.Keys(props)) {
		if slices.Contains(machineProps, k) {
			continue
		}
		at := where + "." + k
		v, err := textOf(sc, props[k])
		if err == nil {
			if msg := blueprint.CheckProperty(k, v); msg != "" {
				err = errors.New(msg)
			}
		}
		switch {
		case err != nil:
			note(Unmapped, at, fmt.Sprintf("Custom property %s: %v. Left out.", k, err))
		case len(custom) == blueprint.MaxProperties:
			note(Unmapped, at, fmt.Sprintf("Custom property %s: rvav keeps at most %d. Left out.", k, blueprint.MaxProperties))
		default:
			custom[k] = v
		}
	}
	if len(custom) > 0 {
		c["properties"] = custom
		note(Info, where+".properties", "Custom properties "+strings.Join(slices.Sorted(maps.Keys(custom)), ", ")+
			": each VM gets them as vSphere custom attributes. Remove any that was a vSphere setting rvav doesn't have.")
	}
}

// textOf is a scalar property as rvav's text with ${CEL}.
func textOf(sc scope, v any) (string, error) {
	switch x := v.(type) {
	case string:
		return sc.interp(x)
	case bool, int, int64, float64:
		return fmt.Sprint(x), nil
	}
	return "", errors.New("a list or a map, where rvav takes a text")
}

// templateSecrets are the secrets a template's machines' cloud-init opens.
func templateSecrets(content string) []string {
	var t tmpl
	_ = yaml.Unmarshal([]byte(content), &t)
	var out []string
	for _, name := range slices.Sorted(maps.Keys(t.Resources)) {
		if ci, _ := t.Resources[name].Properties["cloudConfig"].(string); slices.Contains(machineTypes, t.Resources[name].Type) {
			for _, s := range secretsIn(ci) {
				if !slices.Contains(out, s) {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

func boundTemplates(b map[string]any) []string {
	var out []string
	add := func(m map[string]any) {
		bindings, _ := m["bindings"].(map[string]any)
		raw, _ := bindings["template"].(string)
		if key, err := strconv.Unquote(raw); err == nil && !slices.Contains(out, key) {
			out = append(out, key)
		}
	}
	add(b)
	comps, _ := b["components"].([]map[string]any)
	for _, c := range comps {
		add(c)
	}
	return out
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
		if f.SourceID == blueprintID && f.SourceType == "com.vmw.blueprint" && strings.EqualFold(f.Type, "requestForm") &&
			!strings.EqualFold(f.Status, "off") {
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

// versionNumber is vRA's version as rvav's major.minor.
// freeNumber is n, or the next minor version not taken.
func freeNumber(n string, taken map[string]bool) string {
	major, minor, _ := strings.Cut(n, ".")
	m, err := strconv.Atoi(minor)
	for err == nil && taken[n] {
		m++
		n = major + "." + strconv.Itoa(m)
	}
	return n
}

// TemplateSlugs are the templates' item slugs: from their names, with the
// project's name too where names repeat, then the template's ID.
func TemplateSlugs(e *vra.Estate) map[string]string {
	out := map[string]string{}
	count := map[string]int{}
	for _, bp := range e.Blueprints {
		count[trimSlug(slug(bp.Name))]++
	}
	used := map[string]bool{}
	for _, bp := range e.Blueprints {
		s := trimSlug(slug(bp.Name))
		if count[s] > 1 {
			s = trimSlug(s + "-" + slug(projectName(e, bp.ProjectID)))
			if used[s] {
				s = trimSlug(s + "-" + slug(bp.ID))
			}
		}
		used[s] = true
		out[bp.ID] = s
	}
	return out
}

func trimSlug(s string) string {
	if len(s) > 63 {
		s = s[:63]
	}
	return strings.TrimRight(s, "-")
}

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
