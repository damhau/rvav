package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/integrations/vsphere"
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
	return a.run(ctx, in.RequestID, in.ProjectID, in.Op.Key, func() (string, string, error) {
		detail, err := a.op(ctx, in)
		if err != nil {
			switch {
			case vsphere.IsNotFound(err):
				return "", "", permanent("The VM is no longer in vCenter.")
			case errors.Is(err, vsphere.ErrToolsNotRunning):
				return "", "", permanent("%v", err)
			case isPermanent(err):
				return "", "", err
			}
			return "", "", fmt.Errorf("vCenter: %w", err)
		}
		if in.ResourceID != "" && in.Moref != "" {
			a.refreshVM(ctx, in)
		}
		return "", detail, nil
	})
}

func (a *Activities) op(ctx context.Context, in OpInput) (string, error) {
	v := a.VSphere
	switch in.Op.Op {
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

// refreshVM stores what vCenter reports about the VM now. A failure only
// delays the update to the next inventory sync.
func (a *Activities) refreshVM(ctx context.Context, in OpInput) {
	res, err := a.Store.GetResource(ctx, in.ResourceID)
	if err != nil {
		a.Log.Warn("refresh vm: load resource", "resource", in.ResourceID, "err", err)
		return
	}
	vm, err := a.VSphere.GetVM(ctx, in.Moref)
	if err != nil {
		a.Log.Warn("refresh vm", "moref", in.Moref, "err", err)
		return
	}
	var old struct {
		VM struct {
			Fqdn     string `json:"fqdn"`
			Template string `json:"template"`
		} `json:"vm"`
	}
	_ = json.Unmarshal(res.Properties, &old)
	res.Properties = vmprops.Build(a.VSphere.Name(), *vm, old.VM.Fqdn, old.VM.Template, nil, 0)
	res.Name, res.SyncState = vm.Name, "in_sync"
	if err := a.Store.UpdateResourceState(ctx, a.Store.Pool, res.ID, res.SyncState, res.Properties); err != nil {
		a.Log.Warn("refresh vm: store", "resource", res.ID, "err", err)
		return
	}
	a.Events.Resource(ctx, in.ProjectID, res)
	a.Events.Deployment(ctx, in.DeploymentID)
}

func isPermanent(err error) bool {
	var app interface{ Type() string }
	return errors.As(err, &app) && app.Type() == "Permanent"
}

// day2Status is the deployment's status after a day-2 request ended.
func day2Status(plan Plan, outcome string) string {
	if plan.Day2 == nil {
		return "ready"
	}
	switch {
	case outcome == "succeeded" && plan.Day2.After != "":
		return plan.Day2.After
	case outcome == "failed" && plan.Day2.After == "deleted":
		return "failed"
	}
	return "ready"
}
