package provision

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/store"
	"github.com/damhau/rvav/internal/vmprops"
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

type addressProps struct {
	Address  string `json:"address"`
	Subnet   string `json:"subnet"`
	ID       string `json:"efficientip_id"`
	Hostname string `json:"hostname"`
	FQDN     string `json:"fqdn"`
	NIC      string `json:"nic"`
}

// renames are the FQDNs the VM's rename changes, old to new: its own, and
// each added adapter's, <name>-<key> in its zone (ADR-0024).
func (a *Activities) renames(ctx context.Context, in OpInput) (map[string]string, error) {
	oldName, name := argStr(in.Op.Args, "old_name"), argStr(in.Op.Args, "name")
	out := map[string]string{argStr(in.Op.Args, "old_fqdn"): argStr(in.Op.Args, "fqdn")}
	addresses, err := a.deploymentResources(ctx, in.DeploymentID, "ipam.address")
	if err != nil {
		return nil, err
	}
	for _, r := range addresses {
		var p addressProps
		_ = json.Unmarshal(r.Properties, &p)
		if p.NIC == "" || p.Hostname != oldName && p.Hostname != name {
			continue
		}
		if zone, ok := strings.CutPrefix(p.FQDN, oldName+"-"+p.NIC+"."); ok {
			out[p.FQDN] = name + "-" + p.NIC + "." + zone
		}
	}
	delete(out, "")
	return out, nil
}

// renameAddress renames the VM's addresses in EfficientIP, under the FQDN
// as when they were reserved: its own, and its added adapters'.
func (a *Activities) renameAddress(ctx context.Context, in OpInput) (string, error) {
	oldName, fqdn := argStr(in.Op.Args, "old_name"), argStr(in.Op.Args, "fqdn")
	addresses, err := a.deploymentResources(ctx, in.DeploymentID, "ipam.address")
	if err != nil {
		return "", err
	}
	renames, err := a.renames(ctx, in)
	if err != nil {
		return "", err
	}
	var done []string
	for _, r := range addresses {
		var p addressProps
		_ = json.Unmarshal(r.Properties, &p)
		if p.Hostname != oldName && p.Hostname != argStr(in.Op.Args, "name") {
			continue
		}
		to := fqdn
		if p.NIC != "" {
			if to = renames[p.FQDN]; to == "" {
				continue // renamed already
			}
		}
		addr := efficientip.Address{ID: p.ID, IP: p.Address, Subnet: p.Subnet, Hostname: cmp.Or(p.FQDN, p.Hostname)}
		if err := a.IPAM.RenameAddress(ctx, addr, to); err != nil {
			return "", fmt.Errorf("EfficientIP: %w", err)
		}
		done = append(done, p.Address+" is now "+to)
	}
	if len(done) == 0 {
		return "No address to rename", nil
	}
	return strings.Join(done, ", "), nil
}

// checkName makes sure, just before the rename, that DNS and IPAM don't
// have the new name: it was checked when the action was asked for, but an
// approval may have taken days.
func (a *Activities) checkName(ctx context.Context, in OpInput) (string, error) {
	fqdn := argStr(in.Op.Args, "fqdn")
	renames, err := a.renames(ctx, in)
	if err != nil {
		return "", err
	}
	for _, to := range renames {
		taken, err := a.IPAM.NameExists(ctx, to)
		if err != nil {
			return "", fmt.Errorf("EfficientIP: %w", err)
		}
		if taken {
			return "", permanent("%s is already in DNS or IPAM", to)
		}
	}
	return fqdn + " is free", nil
}

// renameDNS adds the A record of the new name before deleting the old one,
// so the VM keeps a name when the new record can't be made, and points the
// PTR record to the new name in place. Records already renamed are left
// alone.
func (a *Activities) renameDNS(ctx context.Context, in OpInput) (string, error) {
	oldFQDN, fqdn := argStr(in.Op.Args, "old_fqdn"), argStr(in.Op.Args, "fqdn")
	records, err := a.deploymentResources(ctx, in.DeploymentID, "dns.record")
	if err != nil {
		return "", err
	}
	// The VM's own records and its added adapters'.
	renames, err := a.renames(ctx, in)
	if err != nil {
		return "", err
	}
	for _, r := range records {
		var p dnsProps
		_ = json.Unmarshal(r.Properties, &p)
		next := p
		switch {
		case (p.Type == "A" || p.Type == "AAAA") && renames[p.FQDN] != "":
			next.FQDN = renames[p.FQDN]
		case p.Type == "PTR" && renames[p.Value] != "":
			next.Value = renames[p.Value]
		default:
			continue
		}
		old := efficientip.Record{ID: p.ID, FQDN: p.FQDN, Type: p.Type, Value: p.Value, Zone: p.Zone}
		var created efficientip.Record
		if p.Type == "PTR" {
			created, err = a.IPAM.UpdateRecord(ctx, old, next.Value)
		} else {
			created, err = a.IPAM.CreateRecord(ctx, efficientip.Record{FQDN: next.FQDN, Type: next.Type, Value: next.Value, Zone: next.Zone})
			if err == nil {
				err = a.IPAM.DeleteRecord(ctx, old)
			}
		}
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
	// A deployment named after its VM follows it, unless another of the
	// project's deployments holds the name (ADR-0046).
	if d.Name == oldName {
		if _, err := a.Store.SetDeploymentDetails(ctx, d.ProjectID, d.ID, name, nil); err != nil {
			return "", err
		}
	}
	list, err := a.deploymentResources(ctx, in.DeploymentID, "")
	if err != nil {
		return "", err
	}
	renames, err := a.renames(ctx, in)
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
			old, _ := props["fqdn"].(string)
			props["hostname"], props["fqdn"] = name, fqdn
			// An added adapter's address keeps its own name (ADR-0024).
			if nic, _ := props["nic"].(string); nic != "" {
				props["fqdn"] = cmp.Or(renames[old], old)
			}
			if id, _ := props["allocation_id"].(string); props["source"] == rangeSource {
				to, _ := props["fqdn"].(string)
				if err := a.Store.RenameAllocation(ctx, a.Store.Pool, id, name, to); err != nil {
					return "", err
				}
			}
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
	// Streams following the deployment decided who sees it when they
	// subscribed: in an owner_only project the old owner no longer does.
	a.Events.AccessChanged(ctx)
	a.Events.Deployment(ctx, d.ID)
	a.Events.Notify(ctx, store.NewNotification{UserID: owner.ID, Kind: "owner_changed", Title: "You now own " + d.Name,
		Body: d.OwnerName + " handed it over to you.", Level: "info", Link: "/deployments/" + d.ID, DeploymentID: &d.ID,
		Data: map[string]any{"from": d.OwnerName}, DedupeKey: runKey(ctx, in.RequestID, "owner_changed")})
	return "From " + d.OwnerName + " to " + owner.Name, nil
}

// allowDelete lets the deployment's VMs kept from deletion be deleted
// (ADR-0038).
func (a *Activities) allowDelete(ctx context.Context, in OpInput) (string, error) {
	names, err := a.Store.AllowDelete(ctx, a.Store.Pool, in.DeploymentID)
	if err != nil {
		return "", err
	}
	a.Events.Deployment(ctx, in.DeploymentID)
	if len(names) == 0 {
		return "Its VMs could already be deleted", nil
	}
	return strings.Join(names, ", ") + " can be deleted", nil
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
		vc, err := a.vc(ctx, vmprops.VCenterOf(r.Properties))
		if err != nil {
			return "", err
		}
		if err := heartbeatWhile(ctx, func() error { return vc.MoveToFolder(ctx, p.VM.Moref, folder) }); err != nil {
			return "", err
		}
		_ = a.refreshVM(ctx, OpInput{ProjectID: in.ProjectID, DeploymentID: d.ID, ResourceID: r.ID, Moref: p.VM.Moref}, "")
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
	held, err := a.Store.MoveDeploymentNamed(ctx, d.ID, d.Name, project.ID)
	if err != nil {
		return "", err
	}
	if held != "" {
		return "", permanent("%s already has a deployment named %s: rename this one first.", project.Name, held)
	}
	a.Events.AccessChanged(ctx)
	a.Events.Deployment(ctx, d.ID)
	return "From " + d.ProjectName + " to " + project.Name, nil
}

// setDetails gives the deployment the name and description its owner
// edited (ADR-0046).
func (a *Activities) setDetails(ctx context.Context, in OpInput) (string, error) {
	name, desc := argStr(in.Op.Args, "name"), argStr(in.Op.Args, "description")
	d, err := a.Store.GetDeployment(ctx, in.DeploymentID)
	if err != nil {
		return "", err
	}
	held, err := a.Store.SetDeploymentDetails(ctx, d.ProjectID, d.ID, name, &desc)
	if err != nil {
		return "", err
	}
	if held != "" {
		return "", permanent("%s already has a deployment named %s.", d.ProjectName, held)
	}
	a.Events.Deployment(ctx, d.ID)
	switch {
	case d.Name == name:
		return "New description", nil
	case d.Description == desc:
		return "From " + d.Name + " to " + name, nil
	}
	return "From " + d.Name + " to " + name + ", with a new description", nil
}

// deleteResources deletes one resource (destroy_vm) or every resource of a
// type (delete_resources) of the deployment.
func (a *Activities) deleteResources(ctx context.Context, in OpInput) (string, error) {
	all, err := a.deploymentResources(ctx, in.DeploymentID, "")
	if err != nil {
		return "", err
	}
	typ := argStr(in.Op.Args, "type")
	// A VM removed by an update names its own records (ADR-0042): an
	// empty list deletes none.
	ids := argStrs(in.Op.Args, "resources")
	_, named := in.Op.Args["resources"]
	if id := argStr(in.Op.Args, "resource"); id != "" {
		ids, named = append(ids, id), true
	}
	// A deletion going on past a VM it couldn't delete leaves that VM its
	// addresses and records: released, they would be given to another
	// while it still runs with them.
	var running map[string]string
	if in.Op.KeepGoing && !named {
		running = stillUsed(all)
	}
	var done []string
	var failed []error
	for _, r := range all {
		if typ != "" && r.Type != typ || named && !slices.Contains(ids, r.ID) {
			continue
		}
		if vm, ok := running[r.ID]; ok {
			done = append(done, "kept "+leftName(r)+" of "+vm+", which is still there")
			continue
		}
		what, err := a.deleteResource(ctx, in.RequestID, r)
		if err != nil {
			err = fmt.Errorf("%s %s: %w", r.Type, r.Name, err)
			if !in.Op.KeepGoing {
				return "", err
			}
			failed = append(failed, err)
			continue
		}
		a.Events.Deployment(ctx, in.DeploymentID)
		if what != "" {
			done = append(done, what)
		}
	}
	return strings.Join(done, ", "), errors.Join(failed...)
}

// stillUsed are, by ID, the addresses and DNS records of the recorded
// VMs, with the name of the VM each one is for.
func stillUsed(list []store.Resource) map[string]string {
	var rest []day2.Resource
	var vms []day2.Resource
	for _, r := range list {
		res := day2.Resource{ID: r.ID, Type: r.Type, Name: r.Name, Properties: r.Properties}
		if r.Type == "vsphere.vm" {
			res.VM = day2.VM(r.Properties)
			vms = append(vms, res)
			continue
		}
		rest = append(rest, res)
	}
	out := map[string]string{}
	for _, vm := range vms {
		for _, r := range day2.OwnResources(vm, append([]day2.Resource{vm}, rest...)) {
			if r.Type == "ipam.address" || r.Type == "dns.record" || r.Type == ComputerResource {
				out[r.ID] = vm.Name
			}
		}
	}
	return out
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

// leaseCheck stops a lease expiry or deletion when the lease no longer
// ends: it was extended after rvav decided to act.
func (a *Activities) leaseCheck(ctx context.Context, in OpInput) (string, error) {
	d, err := a.Store.GetDeployment(ctx, in.DeploymentID)
	if err != nil {
		return "", err
	}
	if d.LeaseExpiresAt == nil || d.LeaseExpiresAt.After(a.now()) {
		return "", permanent("The lease was extended; nothing to do.")
	}
	return "Ended " + d.LeaseExpiresAt.UTC().Format("2 Jan 2006 15:04 UTC"), nil
}

// leaseExpired records that the lease's expiry powered the VMs off: the
// grace period before deletion starts now.
func (a *Activities) leaseExpired(ctx context.Context, in OpInput) (string, error) {
	d, err := a.Store.GetDeployment(ctx, in.DeploymentID)
	if err != nil {
		return "", err
	}
	if d.LeaseExpiredAt == nil {
		if err := a.Store.SetLeaseExpired(ctx, a.Store.Pool, d.ID, a.now()); err != nil {
			return "", err
		}
	}
	a.Events.Deployment(ctx, d.ID)
	if d.LeaseGraceDays != nil && d.ExpiryAction == "power_off_then_delete" {
		a.Events.Notify(ctx, store.NewNotification{UserID: d.OwnerID, Kind: "lease_expired", Title: "The lease of " + d.Name + " ended",
			Body:  fmt.Sprintf("Its VMs are powered off; it is deleted in %d days unless you extend the lease.", *d.LeaseGraceDays),
			Level: "warning", Link: "/deployments/" + d.ID, DeploymentID: &d.ID, Data: map[string]any{"grace_days": *d.LeaseGraceDays},
			DedupeKey: "lease_expired:" + d.ID + ":" + d.LeaseExpiresAt.UTC().Format(time.RFC3339)})
		return fmt.Sprintf("Deleted in %d days unless the lease is extended", *d.LeaseGraceDays), nil
	}
	return "", nil
}
