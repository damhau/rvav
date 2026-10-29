package provision

import (
	"context"
	"errors"
	"fmt"
	"time"

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
		detail, err := a.op(ctx, in)
		if err != nil {
			var refused *vsphere.Refused
			switch {
			case vsphere.IsNotFound(err):
				return "", "", permanent("The VM is no longer in vCenter.")
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
			if err := a.refreshVM(ctx, in); err != nil {
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
		label, err := v.AddDisk(ctx, in.Moref, argInt(args, "size_gb"))
		return label, err
	case "extend_disk":
		return "Grow the file system in the guest to use the space", v.ExtendDisk(ctx, in.Moref, argStr(args, "disk"), argInt(args, "size_gb"))
	case "add_nic":
		return "", v.AddNIC(ctx, in.Moref, argStr(args, "network"))
	case "set_nic_network":
		return "", v.SetNICNetwork(ctx, in.Moref, argStr(args, "nic"), argStr(args, "network"))
	case "remove_nic":
		return "", v.RemoveNIC(ctx, in.Moref, argStr(args, "nic"))
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
	case "move_folder":
		return a.moveFolder(ctx, in)
	case "set_project":
		return a.setProject(ctx, in)
	case "set_lease":
		return a.setLease(ctx, in)
	case "lease_expired":
		return a.leaseExpired(ctx, in)
	case "lease_check":
		return a.leaseCheck(ctx, in)
	case "destroy_vm", "delete_resources":
		return a.deleteResources(ctx, in)
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
	"power_on": {vmprops.Power}, "power_off": {vmprops.Power}, "reset": {vmprops.Power},
	"restart_guest": {vmprops.Power}, "shutdown_guest": {vmprops.Power},
	"reconfigure": {vmprops.Size}, "add_disk": {vmprops.Disks}, "extend_disk": {vmprops.Disks},
	"add_nic": {vmprops.Networks}, "set_nic_network": {vmprops.Networks}, "remove_nic": {vmprops.Networks},
	"revert_snapshot": vmprops.All, "accept_changes": vmprops.All,
}

// refreshVM stores what vCenter reports about the VM now, the aspects the
// operation changed taken as rvav left them (S13.2). A vCenter or database
// hiccup is tried again: left out, rvav's own change would look like drift.
func (a *Activities) refreshVM(ctx context.Context, in OpInput) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if serr := vsphere.Sleep(ctx, time.Duration(attempt)*time.Second); serr != nil {
				return serr
			}
		}
		if err = a.refreshVMOnce(ctx, in); err == nil {
			return nil
		}
		a.Log.Warn("refresh vm", "resource", in.ResourceID, "moref", in.Moref, "attempt", attempt+1, "err", err)
	}
	return err
}

func (a *Activities) refreshVMOnce(ctx context.Context, in OpInput) error {
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
	// The sync writes the row too: replace it only if it is still what was
	// read, else read it again.
	for range 3 {
		props, drifted := vmprops.Settle(res.Properties, vmprops.Build(vc.Name(), *vm, vmprops.OursOf(res.Properties), nil, 0),
			settles[in.Op.Op]...)
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

func isPermanent(err error) bool {
	var app interface{ Type() string }
	return errors.As(err, &app) && app.Type() == "Permanent"
}

// day2Status is the deployment's status after a day-2 request ended: what
// the action made of it, or what it was before. A delete that stopped
// half-way leaves it failed.
func day2Status(plan Plan, outcome string, started bool) string {
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
