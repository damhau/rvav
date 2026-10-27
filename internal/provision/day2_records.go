package provision

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/store"
)

// Day-2 operations on rvav's records and on EfficientIP: renames, owner
// and project changes, deletion.

func (a *Activities) deploymentResources(ctx context.Context, deploymentID, typ string) ([]store.Resource, error) {
	list, err := a.Store.ListResources(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	var out []store.Resource
	for _, r := range list {
		if typ == "" || r.Type == typ {
			out = append(out, r)
		}
	}
	return out, nil
}

type dnsProps struct {
	FQDN  string `json:"fqdn"`
	Type  string `json:"record_type"`
	Value string `json:"value"`
	Zone  string `json:"zone"`
	ID    string `json:"efficientip_id"`
}

// renameDNS replaces the A record of the old name and points the PTR
// record to the new one. Records already renamed are left alone.
func (a *Activities) renameDNS(ctx context.Context, in OpInput) (string, error) {
	oldFQDN, fqdn := argStr(in.Op.Args, "old_fqdn"), argStr(in.Op.Args, "fqdn")
	records, err := a.deploymentResources(ctx, in.DeploymentID, "dns.record")
	if err != nil {
		return "", err
	}
	for _, r := range records {
		var p dnsProps
		_ = json.Unmarshal(r.Properties, &p)
		next := p
		switch {
		case p.Type == "A" && p.FQDN == oldFQDN:
			next.FQDN = fqdn
		case p.Type == "PTR" && p.Value == oldFQDN:
			next.Value = fqdn
		default:
			continue
		}
		if err := a.IPAM.DeleteRecord(ctx, efficientip.Record{ID: p.ID, FQDN: p.FQDN, Type: p.Type, Value: p.Value}); err != nil {
			return "", fmt.Errorf("EfficientIP DNS: %w", err)
		}
		created, err := a.IPAM.CreateRecord(ctx, efficientip.Record{FQDN: next.FQDN, Type: next.Type, Value: next.Value, Zone: next.Zone})
		if err != nil {
			return "", fmt.Errorf("EfficientIP DNS: %w", err)
		}
		next.ID = created.ID
		ext := created.ID
		if ext == "" {
			ext = next.Type + ":" + next.FQDN
		}
		props, _ := json.Marshal(next)
		r.Name, r.Properties = next.Type+" "+next.FQDN, props
		if err := a.Store.RenameResource(ctx, a.Store.Pool, r.ID, r.Name, &ext, props); err != nil {
			return "", err
		}
		a.Events.Resource(ctx, in.ProjectID, r)
	}
	return oldFQDN + " → " + fqdn, nil
}

// renameRecords gives the deployment, its VM and its address the new name.
func (a *Activities) renameRecords(ctx context.Context, in OpInput) (string, error) {
	name, oldName, fqdn := argStr(in.Op.Args, "name"), argStr(in.Op.Args, "old_name"), argStr(in.Op.Args, "fqdn")
	d, err := a.Store.GetDeployment(ctx, in.DeploymentID)
	if err != nil {
		return "", err
	}
	if d.Name == oldName {
		if err := a.Store.RenameDeployment(ctx, a.Store.Pool, d.ID, name); err != nil {
			return "", err
		}
	}
	list, err := a.deploymentResources(ctx, in.DeploymentID, "")
	if err != nil {
		return "", err
	}
	for _, r := range list {
		var props map[string]any
		_ = json.Unmarshal(r.Properties, &props)
		switch {
		case r.ID == in.ResourceID:
			if vm, ok := props["vm"].(map[string]any); ok && fqdn != "" {
				vm["fqdn"] = fqdn
			}
			r.Name = name
		case r.Type == "ipam.address" && props["hostname"] == oldName:
			props["hostname"], props["fqdn"] = name, fqdn
		default:
			continue
		}
		r.Properties, _ = json.Marshal(props)
		if err := a.Store.RenameResource(ctx, a.Store.Pool, r.ID, r.Name, nil, r.Properties); err != nil {
			return "", err
		}
		a.Events.Resource(ctx, in.ProjectID, r)
	}
	a.Events.Deployment(ctx, in.DeploymentID)
	return "", nil
}

func (a *Activities) setOwner(ctx context.Context, in OpInput) (string, error) {
	owner, err := a.Store.GetUser(ctx, argStr(in.Op.Args, "owner"))
	if err != nil {
		return "", permanent("The new owner is not an rvav user.")
	}
	d, err := a.Store.GetDeployment(ctx, in.DeploymentID)
	if err != nil {
		return "", err
	}
	if err := a.Store.SetDeploymentOwner(ctx, a.Store.Pool, d.ID, owner.ID); err != nil {
		return "", err
	}
	a.Events.Deployment(ctx, d.ID)
	a.Events.Notify(ctx, owner.ID, "You now own "+d.Name, d.OwnerName+" handed it over to you.", "info", "/deployments/"+d.ID)
	return "From " + d.OwnerName + " to " + owner.Name, nil
}

// moveFolder moves the VMs into the folder the new project's placement
// rules give their cluster.
func (a *Activities) moveFolder(ctx context.Context, in OpInput) (string, error) {
	d, err := a.Store.GetDeployment(ctx, in.DeploymentID)
	if err != nil {
		return "", err
	}
	rules, err := a.Store.PlacementRules(ctx, argStr(in.Op.Args, "project"), d.Environment)
	if err != nil {
		return "", err
	}
	vms, err := a.deploymentResources(ctx, d.ID, "vsphere.vm")
	if err != nil {
		return "", err
	}
	var moved []string
	for _, r := range vms {
		var p struct {
			VM struct {
				Moref   string `json:"moref"`
				Cluster string `json:"cluster"`
				Folder  string `json:"folder"`
			} `json:"vm"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		folder := ""
		for _, rule := range rules {
			if matchPatterns(rule.ClusterPatterns, p.VM.Cluster) {
				folder = rule.Folder
				break
			}
		}
		if folder == "" || folder == p.VM.Folder {
			continue
		}
		if err := heartbeatWhile(ctx, func() error { return a.VSphere.MoveToFolder(ctx, p.VM.Moref, folder) }); err != nil {
			return "", err
		}
		a.refreshVM(ctx, OpInput{ProjectID: in.ProjectID, DeploymentID: d.ID, ResourceID: r.ID, Moref: p.VM.Moref})
		moved = append(moved, r.Name+" → "+folder)
	}
	if len(moved) == 0 {
		return "Already in the project's folder", nil
	}
	return strings.Join(moved, ", "), nil
}

func matchPatterns(patterns []string, name string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

func (a *Activities) setProject(ctx context.Context, in OpInput) (string, error) {
	project, err := a.Store.GetProject(ctx, argStr(in.Op.Args, "project"))
	if err != nil {
		return "", permanent("The project no longer exists.")
	}
	d, err := a.Store.GetDeployment(ctx, in.DeploymentID)
	if err != nil {
		return "", err
	}
	if err := a.Store.SetDeploymentProject(ctx, a.Store.Pool, d.ID, project.ID); err != nil {
		return "", err
	}
	a.Events.Deployment(ctx, d.ID)
	return "From " + d.ProjectName + " to " + project.Name, nil
}

// deleteResources deletes one resource (destroy_vm) or every resource of a
// type (delete_resources) of the deployment.
func (a *Activities) deleteResources(ctx context.Context, in OpInput) (string, error) {
	list, err := a.deploymentResources(ctx, in.DeploymentID, argStr(in.Op.Args, "type"))
	if err != nil {
		return "", err
	}
	var done []string
	for _, r := range list {
		if id := argStr(in.Op.Args, "resource"); id != "" && r.ID != id {
			continue
		}
		what, err := a.deleteResource(ctx, r)
		if err != nil {
			return "", fmt.Errorf("%s %s: %w", r.Type, r.Name, err)
		}
		a.Events.Deployment(ctx, in.DeploymentID)
		if what != "" {
			done = append(done, what)
		}
	}
	return strings.Join(done, ", "), nil
}

// setLease ends the deployment's lease the given number of days from now.
func (a *Activities) setLease(ctx context.Context, in OpInput) (string, error) {
	until := time.Now().UTC().Add(time.Duration(argInt(in.Op.Args, "days")) * 24 * time.Hour).Truncate(time.Second)
	if err := a.Store.ExtendLease(ctx, a.Store.Pool, in.DeploymentID, until); err != nil {
		return "", err
	}
	a.Events.Deployment(ctx, in.DeploymentID)
	return "Until " + until.Format("2 Jan 2006 15:04 UTC"), nil
}

// leaseExpired records that the lease's expiry powered the VMs off: the
// grace period before deletion starts now.
func (a *Activities) leaseExpired(ctx context.Context, in OpInput) (string, error) {
	d, err := a.Store.GetDeployment(ctx, in.DeploymentID)
	if err != nil {
		return "", err
	}
	if d.LeaseExpiredAt == nil {
		if err := a.Store.SetLeaseExpired(ctx, a.Store.Pool, d.ID, time.Now().UTC()); err != nil {
			return "", err
		}
	}
	a.Events.Deployment(ctx, d.ID)
	if d.LeaseGraceDays != nil && d.ExpiryAction == "power_off_then_delete" {
		a.Events.Notify(ctx, d.OwnerID, "The lease of "+d.Name+" ended", fmt.Sprintf(
			"Its VMs are powered off; it is deleted in %d days unless you extend the lease.", *d.LeaseGraceDays), "warning", "/deployments/"+d.ID)
		return fmt.Sprintf("Deleted in %d days unless the lease is extended", *d.LeaseGraceDays), nil
	}
	return "", nil
}
