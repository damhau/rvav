package provision

import (
	"cmp"
	"slices"

	"go.temporal.io/sdk/workflow"

	"github.com/damhau/rvav/internal/blueprint"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/outbox"
	"github.com/damhau/rvav/internal/vmprops"
)

// Update is an update request's plan (ADR-0042). Ops change the VMs it
// keeps, in place; the plan's VMs and Components are those it adds, built
// as a new request builds them; Remove takes away those it scales in, with
// their records; Jobs are post-configuration run on the VMs it keeps.
type Update struct {
	DeploymentID string `json:"deployment_id"`
	// Before is the deployment's status before, given back when the
	// update fails: a failed deployment stays failed.
	Before string       `json:"before"`
	Ops    []day2.Op    `json:"ops,omitempty"`
	Remove []day2.Op    `json:"remove,omitempty"`
	Kept   []KeptVM     `json:"kept,omitempty"`
	Jobs   []UpdateJobs `json:"jobs,omitempty"`
	// VMs are the IDs of the deployment's VMs the update was planned on:
	// it doesn't start once they changed. Nil in a plan made before X1.6.
	VMs []string `json:"vms"`
	// DiskMarks are, by VM resource, the disks it keeps whose place or key
	// differs in the new version (ADR-0052).
	DiskMarks map[string][]vmprops.DiskMark `json:"disk_marks,omitempty"`
}

// Stale says why an update's plan no longer fits its deployment, which
// has these VMs now; "" when it does.
func (u *Update) Stale(vms []string) string {
	if u == nil || u.VMs == nil {
		return ""
	}
	if slices.Equal(slices.Sorted(slices.Values(u.VMs)), slices.Sorted(slices.Values(vms))) {
		return ""
	}
	return "The deployment's VMs changed since this update was submitted: a VM joined or left it. Update it again."
}

// KeptVM is a VM the update keeps, as the VMs it adds and the jobs it
// runs see it.
type KeptVM struct {
	Component string `json:"component,omitempty"`
	Index     int    `json:"index"`
	Name      string `json:"name"`
	FQDN      string `json:"fqdn,omitempty"`
	IP        string `json:"ip,omitempty"`
	Moref     string `json:"moref"`
}

// UpdateJobs are AWX jobs run on one component's kept VMs.
type UpdateJobs struct {
	Component string `json:"component,omitempty"`
	Jobs      []Job  `json:"jobs"`
}

func (j UpdateJobs) at() string { return "@" + cmp.Or(j.Component, blueprint.SingleKey) }

// InventoryKey is the step refreshing the inventory before them.
func (j UpdateJobs) InventoryKey() string { return KeyInventory + j.at() }

// JobKey is the step of one of them.
func (j UpdateJobs) JobKey(job Job) string { return KeyJob + job.Key + j.at() }

func (u Update) kept() []builtVM {
	out := make([]builtVM, len(u.Kept))
	for i, k := range u.Kept {
		out[i] = builtVM{vm: VMUnit{DeploymentID: u.DeploymentID, Component: k.Component, Index: k.Index, Name: k.Name, FQDN: k.FQDN},
			ip: k.IP, moref: k.Moref}
	}
	return out
}

func (u Update) keptOf(component string) ([]builtVM, []VMUnit) {
	var vms []builtVM
	var units []VMUnit
	for _, b := range u.kept() {
		if b.vm.Component == component {
			vms, units = append(vms, b), append(units, b.vm)
		}
	}
	return vms, units
}

// updateSteps are an update's steps: in place, the VMs added, those
// removed, then the jobs on the VMs kept.
func (p Plan) updateSteps() []StepDef {
	u := p.Update
	var steps []StepDef
	for _, op := range u.Ops {
		steps = append(steps, StepDef{Key: op.Key, Title: op.Title, Detail: op.Detail, System: op.System, Status: "pending"})
	}
	if len(p.Components) > 0 {
		steps = append(steps, componentSteps(p.VMs, p.Components, planKeys(), "")...)
	} else {
		steps = append(steps, p.vmSteps()...)
	}
	steps = append(steps, resourceSteps(p.Resources)...)
	for _, op := range u.Remove {
		steps = append(steps, StepDef{Key: op.Key, Title: op.Title, Detail: op.Detail, System: op.System, Status: "pending"})
	}
	for _, j := range u.Jobs {
		_, units := u.keptOf(j.Component)
		if len(units) == 0 {
			continue
		}
		steps = append(steps, StepDef{Key: j.InventoryKey(), Title: "Refresh AWX inventory", System: "awx", Status: "pending"})
		for _, job := range j.Jobs {
			steps = append(steps, StepDef{Key: j.JobKey(job), Title: "AWX job · " + job.Template,
				Detail: "limit=" + names(units), System: "awx", Status: "pending"})
		}
	}
	return steps
}

// runUpdate runs an update's plan (ADR-0042) in the order of its steps.
func runUpdate(ctx workflow.Context, info Info) (string, error) {
	p, u := info.Plan, info.Plan.Update
	target := OpInput{DeploymentID: u.DeploymentID}
	if key, err := runOps(ctx, info, u.Ops, target); err != nil {
		return key, err
	}
	for _, topic := range []string{outbox.ComputeAllocationPre, outbox.NetworkConfigure} {
		if err := machineEvents(ctx, info, topic, p.VMs); err != nil {
			return "", err
		}
		if key, err := machineChecks(ctx, info, topic, "", p.VMs); err != nil {
			return key, err
		}
	}
	built := u.kept()
	switch {
	case len(p.Components) > 0:
		vms, failed, err := buildComponents(ctx, info, p.VMs, p.Components, planKeys(), u.kept())
		if err != nil {
			return failed, err
		}
		built = append(built, vms...)
	case len(p.VMs) > 0:
		vms, failed, err := buildVMs(ctx, info, p.VMs, p.Guest, p.VMKey)
		if err != nil {
			return failed, err
		}
		if failed, err := postConfig(ctx, info, vms, p.PostConfig, KeyInventory, func(j Job) string { return KeyJob + j.Key }, p.Limit(), p.Context); err != nil {
			return failed, err
		}
	}
	if len(p.Resources) > 0 {
		if key, err := makeResources(ctx, info, p.Resources, componentOutputs(built, vmComponents(built, p.Components))); err != nil {
			return key, err
		}
	}
	if key, err := runOps(ctx, info, u.Remove, target); err != nil {
		return key, err
	}
	for _, j := range u.Jobs {
		vms, units := u.keptOf(j.Component)
		if len(vms) == 0 {
			continue
		}
		if failed, err := postConfig(ctx, info, vms, j.Jobs, j.InventoryKey(), j.JobKey, names(units), p.Context); err != nil {
			return failed, err
		}
	}
	return "", nil
}
