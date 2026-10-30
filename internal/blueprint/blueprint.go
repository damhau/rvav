// Package blueprint describes what a catalog item version builds and turns
// request inputs into a concrete plan. The blueprint is stored as JSON on the
// item version; bindings are CEL expressions over the inputs.
package blueprint

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/damhau/rvav/internal/policy"
)

const (
	KindVM  = "vm"
	KindAWX = "awx"
	// KindWorkflow runs a registered custom workflow (ADR-0022).
	KindWorkflow = "workflow"
	// KindComposite chains other items, AWX templates and custom workflows.
	KindComposite = "composite"
)

type Blueprint struct {
	Kind string `json:"kind"`

	// VM blueprints. Without Components, the fields below are the one VM
	// it builds; with them, what components don't set (ADR-0024).
	Naming       string                 `json:"naming,omitempty"`
	Template     string                 `json:"template,omitempty"`
	Library      string                 `json:"library,omitempty"`
	Guest        Guest                  `json:"guest,omitempty"`
	Sizes        map[string]Size        `json:"sizes,omitempty"`
	Bindings     map[string]string      `json:"bindings,omitempty"`
	AWXTemplates map[string]AWXTemplate `json:"awx_templates,omitempty"`
	Disks        []Disk                 `json:"disks,omitempty"`
	NICs         []NIC                  `json:"nics,omitempty"`
	Tags         []string               `json:"tags,omitempty"`
	// StoragePolicy is the VMs' storage policy, over the placement rule's.
	StoragePolicy string `json:"storage_policy,omitempty"`
	// Components are the groups of VMs of a multi-VM blueprint, built as
	// one deployment named by DeploymentName.
	Components []Component `json:"components,omitempty"`
	// Segments are NSX segments made for each deployment (ADR-0028); a
	// VM's network binding names one as segments.<key>.
	Segments       []Segment `json:"segments,omitempty"`
	DeploymentName string    `json:"deployment_name,omitempty"`
	// SecurityGroups are NSX groups made for each deployment, with the
	// distributed firewall rules into them (ADR-0028); Groups are those its
	// VMs are in: keys of SecurityGroups, or existing NSX groups as
	// nsx:<group ID>. Components may name their own.
	SecurityGroups []SecurityGroup `json:"security_groups,omitempty"`
	Groups         []string        `json:"groups,omitempty"`

	// AWX items: the job template to launch with the inputs as extra_vars.
	AWX *AWXTemplate `json:"awx,omitempty"`

	// Workflow items: the key of the custom workflow they start, with the
	// inputs.
	Workflow string `json:"workflow,omitempty"`

	// Composite items: their steps, in order; a step with Parallel is a
	// group run at the same time.
	Steps []CompositeStep `json:"steps,omitempty"`

	// RollbackOnFailure deletes what was created when a step fails. When
	// false, resources are kept so someone can look at the failure.
	RollbackOnFailure bool `json:"rollback_on_failure,omitempty"`
}

// Segment is an NSX segment made for a deployment, on the Tier-1 its
// placement rule names, its subnet carved from the rule's block.
type Segment struct {
	Key   string `json:"key"`
	Title string `json:"title,omitempty"`
}

// SecurityGroup is an NSX group made for a deployment: the VMs naming it
// in their groups, and the firewall rules into it.
type SecurityGroup struct {
	Key   string         `json:"key"`
	Title string         `json:"title,omitempty"`
	Rules []FirewallRule `json:"rules,omitempty"`
}

// FirewallRule lets traffic into its group, or stops it: from any, from
// another of the deployment's groups (its key), or from an existing NSX
// group (nsx:<group ID>); on NSX services by ID (HTTPS, SSH), or any.
type FirewallRule struct {
	From     []string `json:"from"`
	Services []string `json:"services"`
	Action   string   `json:"action,omitempty"` // allow (the default), drop, reject
}

// ExistingGroup is the NSX group a group name refers to, if it is an
// existing one (nsx:<group ID>).
func ExistingGroup(name string) (string, bool) { return strings.CutPrefix(name, "nsx:") }

// SegmentRef is what segments.<key> evaluates to in a binding: a
// reference the request's planning turns into the segment's name.
const SegmentRef = "segment:"

// SegmentKey is the segment a network binding's value names, if any.
func SegmentKey(network string) (string, bool) {
	return strings.CutPrefix(network, SegmentRef)
}

type Guest struct {
	OSFamily string `json:"os_family"` // linux or windows
	Domain   string `json:"domain"`
	// TimeZone is a tz name for Linux, a Windows time zone index for
	// Windows (110, W. Europe, when empty).
	TimeZone string `json:"time_zone,omitempty"`
	// CustomizationSpec names a vCenter customization specification used
	// as the base of the guest customization.
	CustomizationSpec string `json:"customization_spec,omitempty"`
	// AdminPassword names the secret holding a Windows VM's local
	// administrator password.
	AdminPassword string      `json:"admin_password,omitempty"`
	DomainJoin    *DomainJoin `json:"domain_join,omitempty"`
	CloudInit     *CloudInit  `json:"cloud_init,omitempty"`
}

// DomainJoin joins a Windows VM to an Active Directory domain with the
// credentials of a basic secret.
type DomainJoin struct {
	Domain     string `json:"domain"`
	OU         string `json:"ou,omitempty"`
	Credential string `json:"credential"`
}

// CloudInit configures a Linux VM with cloud-init through guestinfo
// instead of vCenter's Linux preparation. UserData is a Go template.
type CloudInit struct {
	UserData string `json:"user_data"`
}

// merge returns g with the fields o sets.
func (g Guest) merge(o *Guest) Guest {
	if o == nil {
		return g
	}
	for _, f := range []struct{ dst, src *string }{{&g.OSFamily, &o.OSFamily}, {&g.Domain, &o.Domain}, {&g.TimeZone, &o.TimeZone},
		{&g.CustomizationSpec, &o.CustomizationSpec}, {&g.AdminPassword, &o.AdminPassword}} {
		if *f.src != "" {
			*f.dst = *f.src
		}
	}
	if o.DomainJoin != nil {
		g.DomainJoin = o.DomainJoin
	}
	if o.CloudInit != nil {
		g.CloudInit = o.CloudInit
	}
	return g
}

// Component is a group of identical VMs (ADR-0024). What it doesn't set
// comes from the blueprint.
type Component struct {
	Key   string `json:"key"`
	Title string `json:"title,omitempty"`
	// Count is a number or CEL over the inputs; 1 when absent.
	Count    any               `json:"count,omitempty"`
	Naming   string            `json:"naming,omitempty"`
	Template string            `json:"template,omitempty"`
	Library  string            `json:"library,omitempty"`
	Guest    *Guest            `json:"guest,omitempty"`
	Bindings map[string]string `json:"bindings,omitempty"`
	Disks    []Disk            `json:"disks,omitempty"`
	NICs     []NIC             `json:"nics,omitempty"`
	Tags     []string          `json:"tags,omitempty"`
	// StoragePolicy is its VMs' storage policy.
	StoragePolicy string `json:"storage_policy,omitempty"`
	// AntiAffinity keeps its VMs on separate hosts.
	AntiAffinity bool `json:"anti_affinity,omitempty"`
	// Groups are the NSX security groups its VMs are in (ADR-0028).
	Groups []string `json:"groups,omitempty"`
}

// Disk is a disk added after the template's: SizeGB is a number or CEL,
// Datastore CEL (the VM's when empty).
type Disk struct {
	SizeGB        any    `json:"size_gb"`
	Datastore     string `json:"datastore,omitempty"`
	StoragePolicy string `json:"storage_policy,omitempty"`
}

// NIC is a network adapter added after the template's: Network is CEL.
type NIC struct {
	Key     string `json:"key,omitempty"`
	Network string `json:"network"`
	// IPAM is efficientip (the default: an address and DNS records) or dhcp.
	IPAM string `json:"ipam,omitempty"`
}

const (
	IPAMEfficientIP = "efficientip"
	IPAMDHCP        = "dhcp"
	// SingleKey is the component of a blueprint without components.
	SingleKey = "vm"
	// MaxVMs is the most VMs a component, and a request, builds.
	MaxVMs = 20
)

// Multi says whether the blueprint lists components: one deployment for
// all its VMs, rather than one per VM.
func (b Blueprint) Multi() bool { return len(b.Components) > 0 }

// ComponentList returns the components with what they inherit from the
// blueprint; a blueprint without components is one, SingleKey.
func (b Blueprint) ComponentList() []Component {
	if !b.Multi() {
		return []Component{{Key: SingleKey, Naming: b.Naming, Template: b.Template, Library: b.Library, Guest: &b.Guest,
			Bindings: b.Bindings, Disks: b.Disks, NICs: b.NICs, Tags: b.Tags, StoragePolicy: b.StoragePolicy, Groups: b.Groups}}
	}
	out := make([]Component, len(b.Components))
	for i, c := range b.Components {
		r := c
		if r.Naming == "" {
			r.Naming = b.Naming
		}
		// A template and its library go together.
		if r.Template == "" && r.Library == "" {
			r.Template, r.Library = b.Template, b.Library
		}
		g := b.Guest.merge(c.Guest)
		r.Guest = &g
		r.Bindings = map[string]string{}
		for k, v := range b.Bindings {
			r.Bindings[k] = v
		}
		for k, v := range c.Bindings {
			r.Bindings[k] = v
		}
		if r.Disks == nil {
			r.Disks = b.Disks
		}
		if r.Groups == nil {
			r.Groups = b.Groups
		}
		if r.NICs == nil {
			r.NICs = b.NICs
		}
		if r.Tags == nil {
			r.Tags = b.Tags
		}
		if r.StoragePolicy == "" {
			r.StoragePolicy = b.StoragePolicy
		}
		out[i] = r
	}
	return out
}

// NICKey is the key of the i-th added adapter (the template's is first).
func NICKey(n NIC, i int) string {
	if n.Key != "" {
		return n.Key
	}
	return fmt.Sprintf("nic%d", i+2)
}

type Size struct {
	Label    string `json:"label"`
	VCPU     int    `json:"vcpu"`
	MemoryGB int    `json:"memory_gb"`
	DiskGB   int    `json:"disk_gb"`
}

type AWXTemplate struct {
	Title       string `json:"title"`
	JobTemplate string `json:"job_template"`
	// TemplateID is the template's AWX ID, set on items made from a
	// template: it is launched by ID, whatever it is called since.
	TemplateID int  `json:"template_id,omitempty"`
	Workflow   bool `json:"workflow,omitempty"`
	// Inventory and Credentials name inputs (picked from awx.inventories and
	// awx.credentials) that are the launch's inventory and credentials
	// rather than extra variables. The template must prompt for them.
	Inventory   string   `json:"inventory,omitempty"`
	Credentials []string `json:"credentials,omitempty"`
}

// VMPlan is one VM to build, after evaluating the bindings.
type VMPlan struct {
	VCPU       int      `json:"vcpu"`
	MemoryGB   int      `json:"memory_gb"`
	DiskGB     int      `json:"disk_gb"`
	Cluster    string   `json:"cluster"`
	Datastore  string   `json:"datastore"`
	Network    string   `json:"network"`
	Template   string   `json:"template"`
	Library    string   `json:"library,omitempty"`
	PostConfig []string `json:"post_config"`
	// StoragePolicy is the VM's (its home and disks without their own).
	StoragePolicy string `json:"storage_policy,omitempty"`
	// Disks and NICs are those added after the template's first ones.
	Disks []DiskPlan `json:"disks,omitempty"`
	NICs  []NICPlan  `json:"nics,omitempty"`
	Tags  []string   `json:"tags,omitempty"`
}

// StorageGB is the VM's storage: its first disk and those added.
func (p VMPlan) StorageGB() int {
	n := p.DiskGB
	for _, d := range p.Disks {
		n += d.SizeGB
	}
	return n
}

type DiskPlan struct {
	SizeGB        int    `json:"size_gb"`
	Datastore     string `json:"datastore,omitempty"`
	StoragePolicy string `json:"storage_policy,omitempty"`
}

type NICPlan struct {
	Key     string `json:"key"`
	Network string `json:"network"`
	IPAM    string `json:"ipam"`
}

func Parse(raw json.RawMessage) (Blueprint, error) {
	var b Blueprint
	if len(raw) == 0 {
		return b, fmt.Errorf("empty blueprint")
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, fmt.Errorf("parse blueprint: %w", err)
	}
	return b, nil
}

// Vars returns the CEL variables for bindings: inputs and the size presets.
func (b Blueprint) Vars(inputs map[string]any) policy.Vars {
	sizes := map[string]any{}
	for k, s := range b.Sizes {
		sizes[k] = map[string]any{"label": s.Label, "vcpu": s.VCPU, "memory_gb": s.MemoryGB, "disk_gb": s.DiskGB}
	}
	segments := map[string]any{}
	for _, s := range b.Segments {
		segments[s.Key] = SegmentRef + s.Key
	}
	return policy.Vars{"inputs": inputs, "sizes": sizes, "segments": segments}
}

// EvaluateVM evaluates the bindings of a blueprint without components.
func (b Blueprint) EvaluateVM(inputs map[string]any) (plan VMPlan, missing []string) {
	plan, _, missing = b.EvaluateComponent(b.ComponentList()[0], inputs)
	return plan, missing
}

// number turns a CEL or JSON number into an int.
func number(v any) (int, bool) {
	switch n := v.(type) {
	case int64:
		return int(n), true
	case int:
		return n, true
	case float64:
		return int(math.Round(n)), true
	}
	return 0, false
}

// numberOrCEL is a number as it is, or a string evaluated as CEL.
func numberOrCEL(v any, vars policy.Vars) (int, bool) {
	if s, ok := v.(string); ok {
		r, err := policy.Eval(s, vars)
		if err != nil {
			return 0, false
		}
		return number(r)
	}
	return number(v)
}

// celStrings evaluates CEL giving a string or a list of them.
func celStrings(expr string, vars policy.Vars) ([]string, bool) {
	v, err := policy.Eval(expr, vars)
	if err != nil {
		return nil, false
	}
	switch x := v.(type) {
	case string:
		return []string{x}, true
	case []any:
		var out []string
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}

// EvaluateComponent evaluates a component (from ComponentList): its VMs'
// plan and how many. Bindings that cannot be evaluated yet (for example a
// cluster not chosen) leave their field empty and are reported in
// missing, so a preview can still show partial results.
func (b Blueprint) EvaluateComponent(c Component, inputs map[string]any) (plan VMPlan, count int, missing []string) {
	vars := b.Vars(inputs)
	count = 1
	if c.Count != nil {
		n, ok := numberOrCEL(c.Count, vars)
		if !ok {
			missing = append(missing, "count")
		} else {
			count = n
		}
	}
	plan.Template, plan.Library, plan.StoragePolicy = c.Template, c.Library, c.StoragePolicy
	plan.Tags = append(plan.Tags, c.Tags...)
	for i, d := range c.Disks {
		dp := DiskPlan{StoragePolicy: d.StoragePolicy}
		n, ok := numberOrCEL(d.SizeGB, vars)
		if !ok {
			missing = append(missing, fmt.Sprintf("disks/%d/size_gb", i))
		}
		dp.SizeGB = n
		if d.Datastore != "" {
			if v, err := policy.Eval(d.Datastore, vars); err == nil {
				dp.Datastore, _ = v.(string)
			}
			if dp.Datastore == "" {
				missing = append(missing, fmt.Sprintf("disks/%d/datastore", i))
			}
		}
		plan.Disks = append(plan.Disks, dp)
	}
	for i, n := range c.NICs {
		np := NICPlan{Key: NICKey(n, i), IPAM: n.IPAM}
		if np.IPAM == "" {
			np.IPAM = IPAMEfficientIP
		}
		if v, err := policy.Eval(n.Network, vars); err == nil {
			np.Network, _ = v.(string)
		}
		if np.Network == "" {
			missing = append(missing, fmt.Sprintf("nics/%d/network", i))
		}
		plan.NICs = append(plan.NICs, np)
	}
	b.Bindings = c.Bindings
	intField := func(key string, dst *int) {
		expr, ok := b.Bindings[key]
		if !ok {
			return
		}
		v, err := policy.Eval(expr, vars)
		if err != nil {
			missing = append(missing, key)
			return
		}
		n, ok := number(v)
		if !ok {
			missing = append(missing, key)
			return
		}
		*dst = n
	}
	strField := func(key string, dst *string) {
		expr, ok := b.Bindings[key]
		if !ok {
			return
		}
		v, err := policy.Eval(expr, vars)
		if s, ok2 := v.(string); err == nil && ok2 && s != "" {
			*dst = s
			return
		}
		missing = append(missing, key)
	}
	intField("vcpu", &plan.VCPU)
	intField("memory_gb", &plan.MemoryGB)
	intField("disk_gb", &plan.DiskGB)
	strField("cluster", &plan.Cluster)
	strField("datastore", &plan.Datastore)
	strField("network", &plan.Network)
	if expr, ok := b.Bindings["template"]; ok {
		if v, err := policy.Eval(expr, vars); err == nil {
			if s, ok := v.(string); ok && s != "" {
				plan.Template = s
			}
		}
	}
	if expr, ok := b.Bindings["post_config"]; ok {
		if v, err := policy.Eval(expr, vars); err == nil {
			if list, ok := v.([]any); ok {
				for _, x := range list {
					if s, ok := x.(string); ok {
						plan.PostConfig = append(plan.PostConfig, s)
					}
				}
			}
		}
	}
	if expr, ok := b.Bindings["tags"]; ok {
		if list, ok := celStrings(expr, vars); ok {
			plan.Tags = append(plan.Tags, list...)
		} else {
			missing = append(missing, "tags")
		}
	}
	return plan, count, missing
}

// OrderedPostConfig returns the chosen post-config templates in the order
// the blueprint lists them, so jobs always run in a stable order.
func (b Blueprint) OrderedPostConfig(chosen []string, order []string) []string {
	set := map[string]bool{}
	for _, c := range chosen {
		set[c] = true
	}
	var out []string
	for _, k := range order {
		if set[k] {
			out = append(out, k)
			delete(set, k)
		}
	}
	for _, c := range chosen {
		if set[c] {
			out = append(out, c)
		}
	}
	return out
}

// CompositeStep is one step of a composite item (ADR-0022): another item,
// an AWX template or a custom workflow; or, with Parallel, a group of them.
type CompositeStep struct {
	Key   string `json:"key,omitempty"`
	Title string `json:"title,omitempty"`
	// Item is another item's slug: a VM, AWX or custom workflow item.
	Item     string       `json:"item,omitempty"`
	AWX      *AWXTemplate `json:"awx,omitempty"`
	Workflow string       `json:"workflow,omitempty"`
	// With binds the step's inputs: strings are CEL over inputs and the
	// outputs of earlier steps (steps.<key>), other values are as they are.
	With map[string]any `json:"with,omitempty"`
	// When, a CEL condition, skips the step when false.
	When     string          `json:"when,omitempty"`
	Undo     *CompositeUndo  `json:"undo,omitempty"`
	Parallel []CompositeStep `json:"parallel,omitempty"`
}

// CompositeUndo undoes an AWX or workflow step when a later one fails.
type CompositeUndo struct {
	JobTemplate string         `json:"job_template,omitempty"`
	Workflow    string         `json:"workflow,omitempty"`
	With        map[string]any `json:"with,omitempty"`
}
