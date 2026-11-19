package provision

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vmprops"
)

// OpInput is one operation of a day-2 request (ADR-0013).
type OpInput struct {
	RequestID    string  `json:"request_id"`
	ProjectID    string  `json:"project_id"`
	DeploymentID string  `json:"deployment_id"`
	ResourceID   string  `json:"resource_id,omitempty"`
	Moref        string  `json:"moref,omitempty"`
	Op           day2.Op `json:"op"`
}

// RunOp runs one operation of a day-2 action and refreshes the resource
// from vCenter, so the deployment shows the result at once.
func (a *Activities) RunOp(ctx context.Context, in OpInput) error {
	// An operation of a whole-deployment action names its VM.
	if m := argStr(in.Op.Args, "moref"); m != "" {
		in.Moref, in.ResourceID = m, argStr(in.Op.Args, "resource")
	}
	return a.run(ctx, in.RequestID, in.ProjectID, in.Op.Key, func() (string, string, error) {
		if in.Op.Op == "nsx_join" || in.Op.Op == "nsx_leave" {
			return a.changeGroups(ctx, in)
		}
		if in.Op.Op == day2.OpForget {
			detail, err := a.forget(ctx, in)
			return "", detail, err
		}
		detail, err := a.op(ctx, in)
		if errors.Is(err, vsphere.ErrNoDisk) && in.Op.Op == "remove_disk" && activity.IsActivity(ctx) && activity.GetInfo(ctx).Attempt > 1 {
			// The attempt before removed it and stopped before it said so.
			detail, err = "", nil
		}
		if err != nil {
			var refused *vsphere.Refused
			switch {
			case vsphere.IsNotFound(err):
				return "", "", permanent("The VM is no longer in vCenter.")
			case errors.Is(err, vsphere.ErrNoDisk):
				return "", "", permanent("%s is no longer on the VM: it was removed or changed since the request was made.",
					cmp.Or(argStr(in.Op.Args, "disk"), "The disk"))
			case errors.As(err, &refused):
				return "", "", permanent("%s", refused.Msg)
			case errors.Is(err, vsphere.ErrToolsNotRunning):
				return "", "", permanent("%v", err)
			case isPermanent(err):
				return "", "", err
			}
			return "", "", fmt.Errorf("vCenter: %w", err)
		}
		if in.ResourceID != "" && in.Moref != "" {
			if err := a.refreshVM(ctx, in, detail); err != nil {
				// Accepting the changes is only this: tried again as a whole.
				if in.Op.Op == "accept_changes" {
					return "", "", fmt.Errorf("record the VM: %w", err)
				}
				a.Log.Warn("refresh vm: given up", "resource", in.ResourceID, "err", err)
			}
		}
		return "", detail, nil
	})
}

func (a *Activities) op(ctx context.Context, in OpInput) (string, error) {
	v, err := a.resourceVC(ctx, in.ResourceID)
	if err != nil {
		return "", err
	}
	args := in.Op.Args
	switch in.Op.Op {
	case "reconfigure":
		return "", v.Reconfigure(ctx, in.Moref, argInt(args, "cpu_count"), argInt(args, "memory_mb"))
	case "add_disk":
		return v.AddDisk(ctx, in.Moref, vsphere.DiskSpec{SizeGB: argInt(args, "size_gb"), Datastore: argStr(args, "datastore"),
			StoragePolicy: argStr(args, "storage_policy"), Provisioning: argStr(args, "provisioning")})
	case "extend_disk":
		return "Grow the file system in the guest to use the space", v.ExtendDisk(ctx, in.Moref, diskRef(args), argInt(args, "size_gb"))
	case "remove_disk":
		return "", v.RemoveDisk(ctx, in.Moref, diskRef(args))
	case "set_tags":
		return "", v.SetTags(ctx, in.Moref, argStrs(args, "categories"), argStrs(args, "tags"))
	case "add_nic":
		return v.AddNIC(ctx, in.Moref, argStr(args, "network"))
	case "set_nic_network":
		return "", v.SetNICNetwork(ctx, in.Moref, nicRef(args), argStr(args, "network"))
	case "remove_nic":
		return "", v.RemoveNIC(ctx, in.Moref, nicRef(args))
	case "rename_vm":
		return "", v.Rename(ctx, in.Moref, argStr(args, "name"))
	case "check_name":
		return a.checkName(ctx, in)
	case "rename_address":
		return a.renameAddress(ctx, in)
	case "rename_dns":
		return a.renameDNS(ctx, in)
	case "rename_records":
		return a.renameRecords(ctx, in)
	case "mount_iso":
		return "", v.MountISO(ctx, in.Moref, argStr(args, "iso"))
	case "unmount_iso":
		return "", v.UnmountISO(ctx, in.Moref)
	case "set_owner":
		return a.setOwner(ctx, in)
	case "allow_delete":
		return a.allowDelete(ctx, in)
	case "move_folder":
		return a.moveFolder(ctx, in)
	case "set_project":
		return a.setProject(ctx, in)
	case "set_details":
		return a.setDetails(ctx, in)
	case "set_lease":
		return a.setLease(ctx, in)
	case "lease_expired":
		return a.leaseExpired(ctx, in)
	case "lease_check":
		return a.leaseCheck(ctx, in)
	case "destroy_vm", "delete_resources":
		return a.deleteResources(ctx, in)
	case "set_drs_rule":
		// The VMs an update keeps of a component it scales in (ADR-0042).
		_, detail, err := a.setRule(ctx, RuleInput{RequestID: in.RequestID, ProjectID: in.ProjectID, DeploymentID: in.DeploymentID,
			Component: argStr(args, "component"), Name: argStr(args, "name"), Cluster: argStr(args, "cluster"),
			VCenter: argStr(args, "vcenter"), VMs: argStrs(args, "vms"), Morefs: argStrs(args, "morefs")})
		return detail, err
	case "create_snapshot":
		return "", heartbeatWhile(ctx, func() error {
			return v.CreateSnapshot(ctx, in.Moref, argStr(args, "name"), argStr(args, "description"), args["memory"] == true)
		})
	case "revert_snapshot":
		return "", heartbeatWhile(ctx, func() error { return v.RevertSnapshot(ctx, in.Moref, argStr(args, "name")) })
	case "delete_snapshot":
		return "", heartbeatWhile(ctx, func() error { return v.DeleteSnapshot(ctx, in.Moref, argStr(args, "name")) })
	case "accept_changes":
		return "", nil // refreshVM takes the VM as it is
	case "power_on":
		return "", v.PowerOn(ctx, in.Moref)
	case "power_off":
		return "", v.PowerOff(ctx, in.Moref)
	case "reset":
		return "", v.Reset(ctx, in.Moref)
	case "suspend":
		return "", v.Suspend(ctx, in.Moref)
	case "restart_guest":
		return "The guest OS is restarting", v.RebootGuest(ctx, in.Moref)
	case "shutdown_guest":
		if err := v.ShutdownGuest(ctx, in.Moref); err != nil {
			return "", err
		}
		timeout := a.ShutdownTimeout
		if timeout == 0 {
			timeout = 10 * time.Minute
		}
		wctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		err := heartbeatWhile(ctx, func() error { return v.WaitPoweredOff(wctx, in.Moref) })
		if err != nil && wctx.Err() != nil && ctx.Err() == nil {
			return "", permanent("The guest OS did not shut down within %s. Power off stops the VM without it.", timeout)
		}
		return "", err
	}
	return "", permanent("unknown operation %q", in.Op.Op)
}

// resourceVC connects to the vCenter a VM resource is in (the first for
// none: a deployment's own operation).
func (a *Activities) resourceVC(ctx context.Context, resourceID string) (vsphere.Driver, error) {
	if resourceID == "" {
		return a.vc(ctx, "")
	}
	res, err := a.Store.GetResource(ctx, resourceID)
	if err != nil {
		return nil, err
	}
	return a.vc(ctx, vmprops.VCenterOf(res.Properties))
}

// settles are what of a VM each operation changes: rvav takes them as they
// are now, and keeps the rest as it left them, to flag changes made
// outside rvav (S13.2).
var settles = map[string][]string{
	"power_on": {vmprops.Power}, "power_off": {vmprops.Power}, "reset": {vmprops.Power}, "suspend": {vmprops.Power},
	"restart_guest": {vmprops.Power}, "shutdown_guest": {vmprops.Power},
	"reconfigure": {vmprops.Size}, "add_disk": {vmprops.Disks}, "extend_disk": {vmprops.Disks}, "remove_disk": {vmprops.Disks},
	"add_nic": {vmprops.Networks}, "set_nic_network": {vmprops.Networks}, "remove_nic": {vmprops.Networks},
	"set_tags": {vmprops.Tags}, "revert_snapshot": vmprops.Hardware, "accept_changes": vmprops.All,
}

// refreshVM stores what vCenter reports about the VM now, the aspects the
// operation changed taken as rvav left them (S13.2). A vCenter or database
// hiccup is tried again: left out, rvav's own change would look like drift.
// added is the label of the disk or adapter the operation added.
func (a *Activities) refreshVM(ctx context.Context, in OpInput, added string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if serr := vsphere.Sleep(ctx, time.Duration(attempt)*time.Second); serr != nil {
				return serr
			}
		}
		if err = a.refreshVMOnce(ctx, in, added); err == nil {
			return nil
		}
		a.Log.Warn("refresh vm", "resource", in.ResourceID, "moref", in.Moref, "attempt", attempt+1, "err", err)
	}
	return err
}

// markAdded records a disk or adapter an update added as the blueprint's
// one its operation names (ADR-0042).
func markAdded(ours *vmprops.Ours, vm vsphere.VM, op day2.Op, label string) {
	if label == "" {
		return
	}
	if _, ok := op.Args["blueprint_disk"]; ok && op.Op == "add_disk" {
		for _, d := range vm.Disks {
			if d.Label == label && d.Key != 0 {
				if ours.BlueprintDisks == nil {
					ours.BlueprintDisks = map[int32]int{}
				}
				ours.BlueprintDisks[d.Key] = argInt(op.Args, "blueprint_disk")
				if key := argStr(op.Args, "blueprint_disk_key"); key != "" {
					if ours.BlueprintDiskKeys == nil {
						ours.BlueprintDiskKeys = map[int32]string{}
					}
					ours.BlueprintDiskKeys[d.Key] = key
				}
			}
		}
	}
	if key := argStr(op.Args, "blueprint_nic"); key != "" && op.Op == "add_nic" {
		for _, n := range vm.NICs {
			if n.Label == label && n.Key != 0 {
				if ours.BlueprintNICs == nil {
					ours.BlueprintNICs = map[int32]string{}
				}
				ours.BlueprintNICs[n.Key] = key
			}
		}
	}
}

func (a *Activities) refreshVMOnce(ctx context.Context, in OpInput, added string) error {
	res, err := a.Store.GetResource(ctx, in.ResourceID)
	if err != nil {
		return err
	}
	vc, err := a.vc(ctx, vmprops.VCenterOf(res.Properties))
	if err != nil {
		return err
	}
	vm, err := vc.GetVM(ctx, in.Moref)
	if err != nil {
		return err
	}
	// GetVM doesn't read tags (the sync does): read them when the
	// operation takes them as they are.
	if slices.Contains(settles[in.Op.Op], vmprops.Tags) {
		if vm.Tags, err = vc.VMTags(ctx, in.Moref); err != nil {
			return err
		}
		if vm.Tags == nil {
			vm.Tags = []string{}
		}
	}
	// The sync writes the row too: replace it only if it is still what was
	// read, else read it again.
	for range 3 {
		ours := vmprops.OursOf(res.Properties)
		markAdded(&ours, *vm, in.Op, added)
		props, drifted := vmprops.Settle(res.Properties, vmprops.Build(vc.Name(), *vm, ours, nil, 0), settles[in.Op.Op]...)
		state := "in_sync"
		if drifted {
			state = "drifted"
		}
		ok, err := a.Store.UpdateResourceStateIf(ctx, res.ID, res.Properties, state, props)
		if err != nil {
			return err
		}
		if ok {
			res.Properties, res.Name, res.SyncState = props, vm.Name, state
			_ = a.Store.RefreshDeploymentSyncState(ctx, a.Store.Pool, res.DeploymentID)
			a.Events.Resource(ctx, in.ProjectID, res)
			a.Events.Deployment(ctx, in.DeploymentID)
			return nil
		}
		if res, err = a.Store.GetResource(ctx, in.ResourceID); err != nil {
			return err
		}
	}
	return errors.New("the VM's record kept changing")
}

func diskRef(args map[string]any) vsphere.DiskRef {
	return vsphere.DiskRef{Key: int32(argInt(args, "key")), File: argStr(args, "file"), Label: argStr(args, "disk")}
}

func nicRef(args map[string]any) vsphere.NICRef {
	return vsphere.NICRef{Key: int32(argInt(args, "key")), Label: argStr(args, "nic")}
}

func argInt(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func argStr(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

// argStrs reads a list of strings, as planned ([]string) or as the
// workflow's history decodes it ([]any).
func argStrs(args map[string]any, key string) []string {
	switch v := args[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return []string{}
}

func isPermanent(err error) bool {
	var app interface{ Type() string }
	return errors.As(err, &app) && app.Type() == "Permanent"
}

// day2Status is the deployment's status after a day-2 request ended: what
// the action made of it, or what it was before. A delete that stopped
// half-way leaves it failed.
func day2Status(plan Plan, outcome string, started bool) string {
	if u := plan.Update; u != nil && u.Before == "failed" && outcome != "succeeded" {
		return u.Before
	}
	if plan.Day2 == nil {
		return "ready"
	}
	d := plan.Day2
	switch {
	case outcome == "succeeded" && d.After != "":
		return d.After
	case d.After == "deleted" && started && outcome != "succeeded":
		return "failed"
	case d.Before == "failed":
		return d.Before
	}
	return "ready"
}

// RolledBack records the end of a rollback requested after a failure: the
// deployments are deleted, or failed again with the error.
func (a *Activities) RolledBack(ctx context.Context, requestID, errMsg string) error {
	r, err := a.Store.GetRequest(ctx, requestID)
	if err != nil {
		return err
	}
	status := "deleted"
	if errMsg != "" {
		status = "failed"
		now := time.Now().UTC()
		sys := "rvav"
		steps, _ := a.Store.ListSteps(ctx, requestID)
		st := store.Step{ID: ids.New(ids.Step), RequestID: requestID, Position: len(steps), Key: "rollback", Title: "Roll back failed",
			System: &sys, Status: "failed", StartedAt: &now, FinishedAt: &now, Error: &errMsg}
		if err := a.Store.InsertSteps(ctx, a.Store.Pool, []store.Step{st}); err != nil && !store.IsUniqueViolation(err) {
			return err
		}
	}
	if err := a.Store.SetDeploymentsStatusForRequest(ctx, a.Store.Pool, requestID, status); err != nil {
		return err
	}
	a.publishAll(ctx, r.ProjectID, requestID)
	if errMsg == "" {
		a.Events.Notify(ctx, store.NewNotification{UserID: r.OwnerID, Kind: "request_rolled_back", Title: "Rolled back: " + r.Title,
			Level: "info", Link: "/requests/" + r.Number, RequestID: &r.ID, DedupeKey: runKey(ctx, r.ID, "request_rolled_back")})
	}
	return nil
}
