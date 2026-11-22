package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/ad"
	"github.com/damhau/rvav/internal/store"
)

// Directory is Active Directory as rvav makes VMs' computer objects in it
// (ADR-0064).
type Directory interface {
	Find(ctx context.Context, name string) (*ad.Computer, error)
	Create(ctx context.Context, comp ad.Computer, groups []string) error
	Delete(ctx context.Context, dn string) error
	Rename(ctx context.Context, dn, name, dnsHostName string) (string, error)
}

// ComputerResource is the type of a VM's computer object.
const ComputerResource = "ad.computer"

// computerProps is what an ad.computer resource holds: its description
// is how rvav knows the object is still the one it made.
type computerProps struct {
	VM          string   `json:"vm"`
	DN          string   `json:"dn"`
	Account     string   `json:"sam_account_name"`
	DNSHostName string   `json:"dns_host_name,omitempty"`
	Groups      []string `json:"groups"`
	Description string   `json:"description"`
}

func (a *Activities) directory(ctx context.Context, why string) (Directory, error) {
	var d Directory
	if a.Directory != nil {
		d = a.Directory(ctx)
	}
	if d == nil {
		return nil, permanent("rvav has no Active Directory integration %s", why)
	}
	return d, nil
}

// directoryErr makes what the directory refuses (the service account's
// rights, a constraint) permanent: trying again changes nothing.
func directoryErr(err error) error {
	if ad.Refused(err) {
		return permanent("Active Directory refused: %v", err)
	}
	return err
}

// CreateComputer makes a VM's computer object and adds it to its groups.
// The object is recorded first, so that a rollback deletes it whatever
// happens next; an object rvav didn't make for the deployment is never
// taken over.
func (a *Activities) CreateComputer(ctx context.Context, in VMInput) error {
	return a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		u := in.VM.AD
		if u == nil {
			return "No computer object", "", nil
		}
		d, err := a.directory(ctx, "to make "+in.VM.Name+"'s computer object in")
		if err != nil {
			return "", "", err
		}
		comp := ad.Computer{Name: in.VM.Name, DN: ad.ComputerDN(in.VM.Name, u.OU), DNSHostName: u.DNSHostName,
			Description: ComputerDescription(in.RequestID, in.VM.DeploymentID)}
		account := ad.Account(in.VM.Name)
		rec := a.findResource(ctx, in.RequestID, in.VM.DeploymentID, ComputerResource, in.VM.Name)
		if rec == nil {
			props, _ := json.Marshal(computerProps{VM: in.VM.Name, DN: comp.DN, Account: account, DNSHostName: u.DNSHostName,
				Groups: u.Groups, Description: comp.Description})
			res, err := a.Store.UpsertResource(ctx, a.Store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: in.VM.DeploymentID,
				RequestID: &in.RequestID, Type: ComputerResource, Name: in.VM.Name, ExternalID: &account, Properties: props})
			if err != nil {
				return "", "", err
			}
			rec = &res
			a.Events.Resource(ctx, in.ProjectID, res)
		}
		err = d.Create(ctx, comp, u.Groups)
		if errors.Is(err, ad.ErrExists) {
			// Someone else's: forgotten, so that no rollback deletes it.
			if err := a.Store.DeleteResource(ctx, a.Store.Pool, rec.ID); err != nil {
				return "", "", err
			}
			return "", "", permanent("Active Directory already has a computer %s that rvav didn't make for this deployment, and rvav doesn't take it over. "+
				"Remove it from Active Directory, then retry.", account)
		}
		if err != nil {
			return "", "", directoryErr(err)
		}
		detail := comp.DN
		switch n := len(u.Groups); {
		case n == 1:
			detail += ", in 1 group"
		case n > 1:
			detail += fmt.Sprintf(", in %d groups", n)
		}
		return "Created computer object " + account, detail, nil
	})
}

// ReleaseComputer deletes the computer object of a VM whose clone failed,
// and forgets it. Its step says so; it fails when the object stays, which
// the deployment then still holds.
func (a *Activities) ReleaseComputer(ctx context.Context, in VMInput) error {
	return a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		r := a.findResource(ctx, in.RequestID, in.VM.DeploymentID, ComputerResource, in.VM.Name)
		if r == nil {
			return "", "", nil
		}
		what, err := a.deleteComputer(ctx, *r)
		if err != nil {
			return "", "", fmt.Errorf("the VM wasn't made and its computer object stays: %w", err)
		}
		if err := a.Store.DeleteResource(ctx, a.Store.Pool, r.ID); err != nil {
			return "", "", err
		}
		a.Events.Deployment(ctx, r.DeploymentID)
		return "Removed computer object " + ad.Account(in.VM.Name), "The VM wasn't made: " + what + ". A retry makes it again.", nil
	})
}

// deleteComputer deletes a VM's computer object when it is still the one
// rvav made; one that is someone else's now is left, and said so.
func (a *Activities) deleteComputer(ctx context.Context, r store.Resource) (string, error) {
	var p computerProps
	_ = json.Unmarshal(r.Properties, &p)
	d, err := a.directory(ctx, "to delete the computer object "+p.Account+" from")
	if err != nil {
		return "", err
	}
	found, err := d.Find(ctx, p.VM)
	switch {
	case err != nil:
		return "", err
	case found == nil:
		return "computer object " + p.Account + " already gone", nil
	case found.Description != p.Description:
		return "left computer object " + p.Account + ", which is no longer rvav's", nil
	}
	if err := d.Delete(ctx, found.DN); err != nil {
		return "", directoryErr(err)
	}
	return "deleted computer object " + p.Account, nil
}

// ownComputer is the VM's computer object of the deployment, named name or
// newName (renamed before a retry); nil for none.
func (a *Activities) ownComputer(ctx context.Context, deploymentID, name, newName string) (*store.Resource, computerProps, error) {
	list, err := a.deploymentResources(ctx, deploymentID, ComputerResource)
	if err != nil {
		return nil, computerProps{}, err
	}
	for _, r := range list {
		var p computerProps
		_ = json.Unmarshal(r.Properties, &p)
		if strings.EqualFold(p.VM, name) || strings.EqualFold(p.VM, newName) {
			return &r, p, nil
		}
	}
	return nil, computerProps{}, nil
}

// checkComputer refuses a new name Active Directory has another object
// of: renamed, the VM's would clash with it.
func (a *Activities) checkComputer(ctx context.Context, in OpInput) (string, error) {
	name := argStr(in.Op.Args, "name")
	d, err := a.directory(ctx, "to rename the computer object in")
	if err != nil {
		return "", err
	}
	found, err := d.Find(ctx, name)
	if err != nil || found == nil {
		return ad.Account(name) + " is free", err
	}
	// Renamed by an attempt before: it is the VM's own.
	_, p, err := a.ownComputer(ctx, in.DeploymentID, "", name)
	if err != nil {
		return "", err
	}
	if p.Description == "" || found.Description != p.Description {
		return "", permanent("Active Directory already has a computer %s, %s. Pick another name.", ad.Account(name), found.DN)
	}
	return "Already renamed", nil
}

// renameComputer gives the VM's computer object the VM's new name, its
// DNS name following, and records it.
func (a *Activities) renameComputer(ctx context.Context, in OpInput) (string, error) {
	name, oldName := argStr(in.Op.Args, "name"), argStr(in.Op.Args, "old_name")
	r, p, err := a.ownComputer(ctx, in.DeploymentID, oldName, name)
	if err != nil || r == nil {
		return "", err
	}
	if strings.EqualFold(p.VM, name) {
		return "Renamed to " + p.Account, nil
	}
	d, err := a.directory(ctx, "to rename the computer object "+p.Account+" in")
	if err != nil {
		return "", err
	}
	found, err := d.Find(ctx, oldName)
	switch {
	case err != nil:
		return "", err
	case found == nil:
		// Renamed before the attempt that recorded it stopped.
		if found, err = d.Find(ctx, name); err != nil {
			return "", err
		}
		if found == nil || found.Description != p.Description {
			return "", permanent("Active Directory no longer has the computer object %s.", p.Account)
		}
	case found.Description != p.Description:
		return "Left " + p.Account + ", which is no longer rvav's", nil
	default:
		dns := ""
		if _, domain, ok := strings.Cut(p.DNSHostName, "."); ok {
			dns = name + "." + domain
		}
		if found.DN, err = d.Rename(ctx, found.DN, name, dns); err != nil {
			return "", directoryErr(err)
		}
		found.DNSHostName = dns
	}
	old := p.Account
	p.VM, p.DN, p.Account, p.DNSHostName = name, found.DN, ad.Account(name), found.DNSHostName
	props, _ := json.Marshal(p)
	if err := a.Store.RenameResource(ctx, a.Store.Pool, r.ID, name, &p.Account, props); err != nil {
		return "", err
	}
	r.Name, r.Properties = name, props
	a.Events.Resource(ctx, in.ProjectID, *r)
	return old + " → " + p.Account, nil
}
