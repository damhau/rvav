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
}

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
	DeploymentID string   `json:"deployment_id"`
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
}

// StepDef is a top-level step created, pending, when the request is submitted.
type StepDef struct {
	Key    string
	Title  string
	Detail string
	System string
	Status string
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
)

// VMKey returns the step key for one VM of the plan.
func (p Plan) VMKey(base string, vm VMUnit) string {
	if len(p.VMs) > 1 {
		return base + "@" + vm.Name
	}
	return base
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
	for _, vm := range p.VMs {
		steps = append(steps,
			StepDef{Key: p.VMKey(KeyIPAM, vm), Title: p.vmTitle("Reserve an IP address", vm), Detail: "Subnet " + vm.Subnet, System: "efficientip", Status: "pending"},
			StepDef{Key: p.VMKey(KeyDNS, vm), Title: p.vmTitle("Create A and PTR records", vm), Detail: vm.FQDN, System: "efficientip", Status: "pending"},
			StepDef{Key: p.VMKey(KeyClone, vm), Title: p.vmTitle("Clone from "+vm.Template, vm), Detail: vm.Cluster + " · " + vm.Datastore, System: "vcenter", Status: "pending"},
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
func (p Plan) Limit() string {
	names := make([]string, len(p.VMs))
	for i, vm := range p.VMs {
		names[i] = vm.Name
	}
	return strings.Join(names, ",")
}

// WorkflowID is the Temporal workflow ID of a request.
func WorkflowID(requestID string) string { return fmt.Sprintf("request-%s", requestID) }
