package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/integrations/vsphere"
	"github.com/damhau/rvav/internal/store"
)

// Console is a VM console opened for a user (ADR-0012).
type Console struct {
	// VMRC opens VMware Remote Console, with a one-time clone ticket of
	// the console account when there is one.
	VMRC string
	// Web is where the HTML console connects, when it was asked for.
	Web *vsphere.WebMKS
}

// OpenConsole checks the console day-2 policy for the VM, records it in the
// audit log, and returns a VMRC link and, when web is true, a WebMKS ticket.
func (s *Service) OpenConsole(ctx context.Context, p *auth.Principal, deploymentID, resourceID string, web bool) (Console, error) {
	var out Console
	d, err := s.visibleDeployment(ctx, p, deploymentID)
	if err != nil {
		return out, err
	}
	resources, err := s.Store.ListResources(ctx, d.ID)
	if err != nil {
		return out, err
	}
	var target *day2.Target
	for _, t := range targets(d, resources) {
		if t.ResourceID == resourceID && t.Type == "vsphere.vm" {
			target = &t
		}
	}
	if target == nil {
		return out, ErrNotFound
	}
	a := day2.Get("console")
	j, err := s.judge(ctx, p, d)
	if err != nil {
		return out, err
	}
	if dec := j.decide(a, *target, nil); dec.Denied != "" {
		return out, fmt.Errorf("%w: %s", ErrForbidden, dec.Denied)
	}
	if d.Status == "deleted" || d.Status == "deleting" {
		return out, &ConflictError{Msg: "The deployment is " + d.Status + "."}
	}
	if reason := a.Unavailable(*target, day2.Choices{}); reason != "" {
		return out, &ConflictError{Msg: reason}
	}
	if s.VCenter == nil {
		return out, &UpstreamError{System: "vCenter", Err: errors.New("no vCenter is configured")}
	}
	// The VM's own vCenter (ADR-0024); the console account is the first's.
	vc, err := s.VCenter(ctx, target.VM.Vcenter)
	if err != nil {
		return out, &UpstreamError{System: "vCenter", Err: err}
	}
	_, err = s.Store.GetVCenter(ctx, target.VM.Vcenter)
	primary := errors.Is(err, store.ErrNotFound) // not one administrators added
	// Without a console account, VMRC asks the user to sign in with their
	// own vCenter account, whose permissions then apply.
	out.VMRC = fmt.Sprintf("vmrc://%s/?moid=%s", vc.Host(), url.QueryEscape(target.VM.Moref))
	if s.ConsoleAccount != nil && primary {
		acct, err := s.ConsoleAccount(ctx)
		if err != nil {
			return out, &UpstreamError{System: "vCenter", Err: err}
		}
		ticket, err := acct.CloneTicket(ctx)
		if err != nil {
			return out, &UpstreamError{System: "vCenter", Err: err}
		}
		out.VMRC = fmt.Sprintf("vmrc://clone:%s@%s/?moid=%s", url.PathEscape(ticket), vc.Host(), url.QueryEscape(target.VM.Moref))
	}
	if web {
		if out.Web, err = vc.WebMKSTicket(ctx, target.VM.Moref); err != nil {
			return out, &UpstreamError{System: "vCenter", Err: err}
		}
	}
	_ = s.Store.Audit(ctx, s.Store.Pool, store.AuditEvent{ActorID: &p.User.ID, Action: "console.open", TargetType: "resource",
		TargetID: target.ResourceID, ProjectID: &d.ProjectID, Detail: map[string]any{"vm": target.ResourceName, "web": web}})
	return out, nil
}
