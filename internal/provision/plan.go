// Package provision runs requests as Temporal workflows: placing and
// building VMs in vCenter, reserving addresses and DNS in EfficientIP, and
// running AWX jobs. Every step is recorded so the UI can show a live timeline.
package provision

import (
	"fmt"
	"strings"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
)

// Plan is everything the workflow needs, computed when the request is
// submitted and stored on the request.
type Plan struct {
	Kind             string `json:"kind"`
	ApprovalRequired bool   `json:"approval_required"`
	// AutoApproved names policies that matched but approved by themselves.
	AutoApproved []string        `json:"auto_approved,omitempty"`
	Quota        map[string]int  `json:"quota"`
	VMs          []VMUnit        `json:"vms,omitempty"`
	PostConfig   []Job           `json:"post_config,omitempty"`
	AWX          *Job            `json:"awx,omitempty"`
	Guest        blueprint.Guest `json:"guest"`
	Rollback     bool            `json:"rollback_on_failure"`
	ExtraVars    map[string]any  `json:"extra_vars,omitempty"`
	Context      map[string]any  `json:"context,omitempty"`
	// Day2 is set on day-2 requests instead of VMs or AWX.
	Day2 *Day2 `json:"day2,omitempty"`
	// Workflow is set on requests of custom workflow items (ADR-0022).
	Workflow *WorkflowCall `json:"workflow,omitempty"`
	// Composite is set on requests of composite items (ADR-0022), instead
	// of VMs, AWX or Workflow.
	Composite []CompositeStep `json:"composite,omitempty"`
	// Components are a multi-VM blueprint's (ADR-0024): VMs say which they
	// belong to, and each has its guest, anti-affinity and post-
	// configuration (PostConfig and Guest are then unused).
	Components []ComponentUnit `json:"components,omitempty"`
}

// ComponentUnit is one component of a multi-VM blueprint's plan.
type ComponentUnit struct {
	Key   string          `json:"key"`
	Title string          `json:"title"`
	Guest blueprint.Guest `json:"guest"`
	// AntiAffinity: its VMs (two or more) are kept on separate hosts.
	AntiAffinity bool  `json:"anti_affinity,omitempty"`
	PostConfig   []Job `json:"post_config,omitempty"`
}

// KeyComponent prefixes a component's step key; its VMs' steps are under it.
const KeyComponent = "component:"

// StepKey is the component's step in the timeline.
func (c ComponentUnit) StepKey() string { return KeyComponent + c.Key }

// VMKey is the key of one of its VMs' steps.
func (c ComponentUnit) VMKey(base string, vm VMUnit) string {
	return c.StepKey() + "/" + base + "@" + vm.Name
}

// RuleName is the DRS rule keeping its VMs apart in the deployment.
func (c ComponentUnit) RuleName(deploymentID string) string {
	return "rvav-" + deploymentID + "-" + c.Key
}

// vmKeys name the steps of components' VMs: under each component's own
// step in a plan, or under a composite step (ADR-0024).
type vmKeys struct {
	vm        func(c ComponentUnit, base string, vm VMUnit) string
	rule      func(c ComponentUnit) string
	inventory func(c ComponentUnit) string
	job       func(c ComponentUnit, j Job) string
	// parent is a component's own step, "" when it has none.
	parent func(c ComponentUnit) string
}

func planKeys() vmKeys {
	return vmKeys{
		vm:        func(c ComponentUnit, base string, vm VMUnit) string { return c.VMKey(base, vm) },
		rule:      func(c ComponentUnit) string { return c.StepKey() + "/" + KeyDRSRule },
		inventory: func(c ComponentUnit) string { return c.StepKey() + "/" + KeyInventory },
		job:       func(c ComponentUnit, j Job) string { return c.StepKey() + "/" + KeyJob + j.Key },
		parent:    func(c ComponentUnit) string { return c.StepKey() },
	}
}

// keys are a composite step's: its components' steps are its own, named
// "@<VM>" or "@<component>".
func (c CompositeStep) keys() vmKeys {
	at := func(base, of string) string { return c.StepKey() + "/" + base + "@" + of }
	return vmKeys{
		vm:        func(_ ComponentUnit, base string, vm VMUnit) string { return at(base, vm.Name) },
		rule:      func(u ComponentUnit) string { return at(KeyDRSRule, u.Key) },
		inventory: func(u ComponentUnit) string { return at(KeyInventory, u.Key) },
		job:       func(u ComponentUnit, j Job) string { return at(KeyJob+j.Key, u.Key) },
		parent:    func(ComponentUnit) string { return "" },
	}
}

// componentSteps are the steps of components' VMs, under parent ("" for
// each component's own step).
func componentSteps(vms []VMUnit, comps []ComponentUnit, keys vmKeys, parent string) []StepDef {
	var steps []StepDef
	for _, c := range comps {
		var own []VMUnit
		for _, vm := range vms {
			if vm.Component == c.Key {
				own = append(own, vm)
			}
		}
		if len(own) == 0 {
			continue
		}
		under := parent
		if p := keys.parent(c); p != "" {
			under = p
			steps = append(steps, StepDef{Key: p, Title: c.Title, Detail: strings.ReplaceAll(names(own), ",", ", "), System: "vcenter", Status: "pending"})
		}
		for _, vm := range own {
			steps = append(steps,
				StepDef{Key: keys.vm(c, KeyIPAM, vm), Title: vm.ipTitle() + " · " + vm.Name, Detail: vm.subnets(), System: "efficientip", Status: "pending", Parent: under},
				StepDef{Key: keys.vm(c, KeyDNS, vm), Title: "Create A and PTR records · " + vm.Name, Detail: vm.FQDN, System: "efficientip", Status: "pending", Parent: under},
				StepDef{Key: keys.vm(c, KeyClone, vm), Title: vm.cloneTitle() + " · " + vm.Name, Detail: vm.Cluster + " · " + vm.Datastore, System: "vcenter", Status: "pending", Parent: under},
				StepDef{Key: keys.vm(c, KeyCustomize, vm), Title: "Apply guest customization · " + vm.Name, Detail: "Hostname, static IP and DNS", System: "vcenter", Status: "pending", Parent: under},
				StepDef{Key: keys.vm(c, KeyPowerOn, vm), Title: "Power on and wait for guest IP · " + vm.Name, System: "vcenter", Status: "pending", Parent: under},
			)
		}
		if c.AntiAffinity {
			steps = append(steps, StepDef{Key: keys.rule(c), Title: "Keep " + c.Title + " on separate hosts",
				Detail: "DRS rule " + c.RuleName(own[0].DeploymentID) + " in " + own[0].Cluster, System: "vcenter", Status: "pending", Parent: under})
		}
		if len(c.PostConfig) > 0 {
			steps = append(steps, StepDef{Key: keys.inventory(c), Title: "Refresh AWX inventory", System: "awx", Status: "pending", Parent: under})
			for _, j := range c.PostConfig {
				steps = append(steps, StepDef{Key: keys.job(c, j), Title: "AWX job · " + j.Template,
					Detail: "limit=" + names(own), System: "awx", Status: "pending", Parent: under})
			}
		}
	}
	return steps
}

// ComponentVMs are the plan's VMs of a component, in order.
func (p Plan) ComponentVMs(key string) []VMUnit {
	var out []VMUnit
	for _, vm := range p.VMs {
		if vm.Component == key {
			out = append(out, vm)
		}
	}
	return out
}

func names(vms []VMUnit) string {
	list := make([]string, len(vms))
	for i, vm := range vms {
		list[i] = vm.Name
	}
	return strings.Join(list, ",")
}

// CompositeStep is one step of a composite item's plan. Steps of the same
// group run at the same time; groups run in order.
type CompositeStep struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Group int    `json:"group"`
	// When skips the step when false; With binds the inputs of AWX and
	// workflow steps, both CEL over inputs and steps.<key>, at run time.
	When string         `json:"when,omitempty"`
	With map[string]any `json:"with,omitempty"`
	// A VM step, planned at submit: its VMs and post-configuration; with
	// Components, a multi-VM item's (ADR-0024), each with its own.
	Components []ComponentUnit `json:"components,omitempty"`
	VMs        []VMUnit        `json:"vms,omitempty"`
	Guest      blueprint.Guest `json:"guest,omitempty"`
	PostConfig []Job           `json:"post_config,omitempty"`
	AWX        *Job            `json:"awx,omitempty"`
	Workflow   *WorkflowCall   `json:"workflow,omitempty"`
	Undo       *CompositeUndo  `json:"undo,omitempty"`
}

// CompositeUndo undoes a step when a later one fails.
type CompositeUndo struct {
	AWX      *Job           `json:"awx,omitempty"`
	Workflow *WorkflowCall  `json:"workflow,omitempty"`
	With     map[string]any `json:"with,omitempty"`
}

// KeyStep prefixes a composite step's key.
const KeyStep = "step:"

// Key is the step's key in the timeline.
func (c CompositeStep) StepKey() string { return KeyStep + c.Key }

// VMKey is the key of one of its VM steps.
func (c CompositeStep) VMKey(base string, vm VMUnit) string {
	k := c.StepKey() + "/" + base
	if len(c.VMs) > 1 {
		k += "@" + vm.Name
	}
	return k
}

// Limit is the AWX host limit covering its VMs.
func (c CompositeStep) Limit() string { return names(c.VMs) }

// Day2 is the plan of a day-2 request: operations on one resource, in order.
type Day2 struct {
	Action string `json:"action"`
	// Name is the action's name when it was asked for (an AWX action's is
	// set by administrators).
	Name         string    `json:"name,omitempty"`
	DeploymentID string    `json:"deployment_id"`
	ResourceID   string    `json:"resource_id,omitempty"`
	Moref        string    `json:"moref,omitempty"`
	Ops          []day2.Op `json:"ops"`
	// Before, During and After are the deployment's status when the action
	// was requested, while it runs and once it succeeded.
	Before string `json:"before,omitempty"`
	During string `json:"during,omitempty"`
	After  string `json:"after"`
}

// VMUnit is one VM and the deployment it belongs to.
type VMUnit struct {
	DeploymentID string `json:"deployment_id"`
	// Component is its blueprint component's key (ADR-0024), and Index its
	// place among the component's VMs.
	Component    string   `json:"component,omitempty"`
	Index        int      `json:"index,omitempty"`
	Name         string   `json:"name"`
	FQDN         string   `json:"fqdn"`
	VCPU         int      `json:"vcpu"`
	MemoryGB     int      `json:"memory_gb"`
	DiskGB       int      `json:"disk_gb"`
	Cluster      string   `json:"cluster"`
	Datastore    string   `json:"datastore"`
	Network      string   `json:"network"`
	Template     string   `json:"template"`
	Folder       string   `json:"folder"`
	Subnet       string   `json:"subnet"`
	SubnetID     string   `json:"subnet_id,omitempty"`
	Gateway      string   `json:"gateway"`
	PrefixLength int      `json:"prefix_length"`
	DNSZone      string   `json:"dns_zone"`
	DNSServers   []string `json:"dns_servers"`
	// Disks and NICs are added after the template's, and Tags attached
	// (ADR-0024).
	Disks []blueprint.DiskPlan `json:"disks,omitempty"`
	NICs  []NICUnit            `json:"nics,omitempty"`
	Tags  []string             `json:"tags,omitempty"`
	// Library is the content library Template is an item of, and
	// StoragePolicy the VM's (ADR-0024).
	Library       string `json:"library,omitempty"`
	StoragePolicy string `json:"storage_policy,omitempty"`
	// VCenter is the vCenter its cluster is in ("" for the first).
	VCenter string `json:"vcenter,omitempty"`
}

// NICUnit is an adapter added after the template's: with efficientip, an
// address in its network's subnet and records for FQDN.
type NICUnit struct {
	Key          string   `json:"key"`
	Network      string   `json:"network"`
	IPAM         string   `json:"ipam"`
	FQDN         string   `json:"fqdn,omitempty"`
	Subnet       string   `json:"subnet,omitempty"`
	SubnetID     string   `json:"subnet_id,omitempty"`
	PrefixLength int      `json:"prefix_length,omitempty"`
	DNSZone      string   `json:"dns_zone,omitempty"`
	DNSServers   []string `json:"dns_servers,omitempty"`
}

func (vm VMUnit) cloneTitle() string {
	if vm.Library != "" {
		return "Deploy " + vm.Template + " from " + vm.Library
	}
	return "Clone from " + vm.Template
}

func (vm VMUnit) ipTitle() string {
	if len(vm.Addressed()) > 0 {
		return "Reserve IP addresses"
	}
	return "Reserve an IP address"
}

func (vm VMUnit) subnets() string {
	list := []string{vm.Subnet}
	for _, n := range vm.Addressed() {
		list = append(list, n.Subnet)
	}
	if len(list) == 1 {
		return "Subnet " + vm.Subnet
	}
	return "Subnets " + strings.Join(list, ", ")
}

// Addressed are the VM's added adapters that get an address and records.
func (vm VMUnit) Addressed() []NICUnit {
	var out []NICUnit
	for _, n := range vm.NICs {
		if n.IPAM == blueprint.IPAMEfficientIP {
			out = append(out, n)
		}
	}
	return out
}

// Job is an AWX job template run as one step.
type Job struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	Template string `json:"template"`
	// TemplateID, when known, is what is launched; else the template is
	// looked up by name.
	TemplateID int  `json:"template_id,omitempty"`
	Workflow   bool `json:"workflow,omitempty"`
	// Inventory and Credentials, when set, are launched with (an AWX item's
	// form chose them).
	Inventory   int   `json:"inventory,omitempty"`
	Credentials []int `json:"credentials,omitempty"`
}

// StepDef is a step created, pending, when the request is submitted:
// top-level, or under Parent (a composite step's own steps).
type StepDef struct {
	Key    string
	Title  string
	Detail string
	System string
	Status string
	Parent string
}

// Step keys. VM steps get "@<vm name>" appended when a request builds
// more than one VM.
const (
	KeySubmit    = "submit"
	KeyPolicy    = "policy"
	KeyIPAM      = "ipam.reserve"
	KeyDNS       = "dns.create"
	KeyClone     = "vsphere.clone"
	KeyCustomize = "vsphere.customize"
	KeyPowerOn   = "vsphere.power_on"
	KeyInventory = "awx.inventory"
	KeyJob       = "awx.job:"
	KeyReady     = "ready"
	// KeyDRSRule is a component's DRS rule, under its step.
	KeyDRSRule = "vsphere.drs_rule"
)

// VMKey returns the step key for one VM of the plan.
func (p Plan) VMKey(base string, vm VMUnit) string {
	if len(p.VMs) > 1 {
		return base + "@" + vm.Name
	}
	return base
}

func vmTitle(vms []VMUnit, title string, vm VMUnit) string {
	if len(vms) > 1 {
		return title + " · " + vm.Name
	}
	return title
}

func (p Plan) vmTitle(title string, vm VMUnit) string {
	if len(p.VMs) > 1 {
		return title + " · " + vm.Name
	}
	return title
}

// Steps lists the top-level steps of the plan in order.
func (p Plan) Steps(environment string) []StepDef {
	steps := []StepDef{{Key: KeySubmit, Title: "Submitted", System: "rvav", Status: "done"}}
	switch {
	case p.ApprovalRequired:
		steps = append(steps, StepDef{Key: KeyPolicy, Title: "Waiting for approval", System: "policy", Status: "running"})
	case len(p.AutoApproved) > 0:
		steps = append(steps, StepDef{Key: KeyPolicy, Title: "Approved automatically",
			Detail: strings.Join(p.AutoApproved, ", ") + " approve requests like this one by themselves", System: "policy", Status: "done"})
	default:
		steps = append(steps, StepDef{Key: KeyPolicy, Title: "Approval not required",
			Detail: "No approval policy applies in " + environment, System: "policy", Status: "done"})
	}
	if p.Day2 != nil {
		for _, op := range p.Day2.Ops {
			steps = append(steps, StepDef{Key: op.Key, Title: op.Title, Detail: op.Detail, System: op.System, Status: "pending"})
		}
		return append(steps, StepDef{Key: KeyReady, Title: "Done", System: "rvav", Status: "pending"})
	}
	if p.Kind == blueprint.KindAWX && p.AWX != nil {
		return append(steps,
			StepDef{Key: KeyJob + p.AWX.Key, Title: "AWX job · " + p.AWX.Template, System: "awx", Status: "pending"},
			StepDef{Key: KeyReady, Title: "Done", System: "rvav", Status: "pending"})
	}
	if len(p.Composite) > 0 {
		for _, c := range p.Composite {
			parent := c.StepKey()
			switch {
			case len(c.Components) > 0:
				steps = append(steps, StepDef{Key: parent, Title: c.Title, Detail: c.Limit(), System: "vcenter", Status: "pending"})
				steps = append(steps, componentSteps(c.VMs, c.Components, c.keys(), parent)...)
			case len(c.VMs) > 0:
				steps = append(steps, StepDef{Key: parent, Title: c.Title, Detail: c.Limit(), System: "vcenter", Status: "pending"})
				for _, vm := range c.VMs {
					steps = append(steps,
						StepDef{Key: c.VMKey(KeyIPAM, vm), Title: vmTitle(c.VMs, vm.ipTitle(), vm), Detail: vm.subnets(), System: "efficientip", Status: "pending", Parent: parent},
						StepDef{Key: c.VMKey(KeyDNS, vm), Title: vmTitle(c.VMs, "Create A and PTR records", vm), Detail: vm.FQDN, System: "efficientip", Status: "pending", Parent: parent},
						StepDef{Key: c.VMKey(KeyClone, vm), Title: vmTitle(c.VMs, vm.cloneTitle(), vm), Detail: vm.Cluster + " · " + vm.Datastore, System: "vcenter", Status: "pending", Parent: parent},
						StepDef{Key: c.VMKey(KeyCustomize, vm), Title: vmTitle(c.VMs, "Apply guest customization", vm), Detail: "Hostname, static IP and DNS", System: "vcenter", Status: "pending", Parent: parent},
						StepDef{Key: c.VMKey(KeyPowerOn, vm), Title: vmTitle(c.VMs, "Power on and wait for guest IP", vm), System: "vcenter", Status: "pending", Parent: parent},
					)
				}
				if len(c.PostConfig) > 0 {
					steps = append(steps, StepDef{Key: parent + "/" + KeyInventory, Title: "Refresh AWX inventory", System: "awx", Status: "pending", Parent: parent})
					for _, j := range c.PostConfig {
						steps = append(steps, StepDef{Key: parent + "/" + KeyJob + j.Key, Title: "AWX job · " + j.Template,
							Detail: "limit=" + c.Limit(), System: "awx", Status: "pending", Parent: parent})
					}
				}
			case c.AWX != nil:
				steps = append(steps, StepDef{Key: parent, Title: c.Title, Detail: "AWX job · " + c.AWX.Template, System: "awx", Status: "pending"})
			case c.Workflow != nil:
				steps = append(steps, StepDef{Key: parent, Title: c.Title, Detail: c.Workflow.Type, System: "rvav", Status: "pending"})
			}
		}
		return append(steps, StepDef{Key: KeyReady, Title: "Ready", System: "rvav", Status: "pending"})
	}
	if len(p.Components) > 0 {
		steps = append(steps, componentSteps(p.VMs, p.Components, planKeys(), "")...)
		return append(steps, StepDef{Key: KeyReady, Title: "Ready", System: "rvav", Status: "pending"})
	}
	if p.Kind == blueprint.KindWorkflow && p.Workflow != nil {
		return append(steps,
			StepDef{Key: KeyWorkflow + p.Workflow.Key, Title: p.Workflow.Title, Detail: p.Workflow.Type, System: "rvav", Status: "pending"},
			StepDef{Key: KeyReady, Title: "Done", System: "rvav", Status: "pending"})
	}
	for _, vm := range p.VMs {
		steps = append(steps,
			StepDef{Key: p.VMKey(KeyIPAM, vm), Title: p.vmTitle(vm.ipTitle(), vm), Detail: vm.subnets(), System: "efficientip", Status: "pending"},
			StepDef{Key: p.VMKey(KeyDNS, vm), Title: p.vmTitle("Create A and PTR records", vm), Detail: vm.FQDN, System: "efficientip", Status: "pending"},
			StepDef{Key: p.VMKey(KeyClone, vm), Title: p.vmTitle(vm.cloneTitle(), vm), Detail: vm.Cluster + " · " + vm.Datastore, System: "vcenter", Status: "pending"},
			StepDef{Key: p.VMKey(KeyCustomize, vm), Title: p.vmTitle("Apply guest customization", vm), Detail: "Hostname, static IP and DNS", System: "vcenter", Status: "pending"},
			StepDef{Key: p.VMKey(KeyPowerOn, vm), Title: p.vmTitle("Power on and wait for guest IP", vm), System: "vcenter", Status: "pending"},
		)
	}
	if len(p.PostConfig) > 0 {
		steps = append(steps, StepDef{Key: KeyInventory, Title: "Refresh AWX inventory", System: "awx", Status: "pending"})
		for _, j := range p.PostConfig {
			steps = append(steps, StepDef{Key: KeyJob + j.Key, Title: "AWX job · " + j.Template,
				Detail: "limit=" + p.Limit(), System: "awx", Status: "pending"})
		}
	}
	return append(steps, StepDef{Key: KeyReady, Title: "Ready", System: "rvav", Status: "pending"})
}

// Limit is the AWX host limit covering every VM of the plan.
func (p Plan) Limit() string { return names(p.VMs) }

// WorkflowID is the Temporal workflow ID of a request.
func WorkflowID(requestID string) string { return fmt.Sprintf("request-%s", requestID) }
