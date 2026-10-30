package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/ids"
	"github.com/damhau/rvav/internal/integrations/efficientip"
	"github.com/damhau/rvav/internal/integrations/nsx"
	"github.com/damhau/rvav/internal/store"
)

// NSX segments made for a deployment (ADR-0028): the subnet carved from an
// EfficientIP block, the segment made on the Tier-1, its port group waited
// for in vCenter, before the VMs; deleted after them.

// SegmentInput is a segment to make, in the vCenter its VMs go to.
type SegmentInput struct {
	RequestID, ProjectID, Key string
	Segment                   SegmentUnit
	VCenter                   string
}

// SegmentResult is the subnet the segment was given.
type SegmentResult struct {
	SubnetID     string `json:"subnet_id"`
	CIDR         string `json:"cidr"`
	Gateway      string `json:"gateway"`
	PrefixLength int    `json:"prefix_length"`
}

func (a *Activities) instance() string {
	if a.Instance == "" {
		return "rvav"
	}
	return a.Instance
}

// MakeSegment makes a segment for a deployment, or finds what an earlier
// run made of it: its subnet, the segment, its port group in vCenter. It
// records both on the deployment as soon as they exist, so a request that
// fails later still deletes them.
func (a *Activities) MakeSegment(ctx context.Context, in SegmentInput) (SegmentResult, error) {
	var out SegmentResult
	err := a.run(ctx, in.RequestID, in.ProjectID, in.Key, func() (string, string, error) {
		sg := in.Segment
		if a.NSX == nil {
			return "", "", permanent("rvav has no NSX Manager: set one in Integrations")
		}
		// A segment of that ID rvav didn't make is in the way.
		existing, err := a.NSX.Segment(ctx, sg.ID)
		switch {
		case err == nil && !nsx.Owned(existing.Tags, a.instance()):
			return "", "", permanent("NSX has a segment %s that rvav didn't make", sg.ID)
		case err != nil && !errors.Is(err, nsx.ErrNotFound):
			return "", "", fmt.Errorf("NSX: %w", err)
		}

		sub, err := a.IPAM.AllocateSubnet(ctx, sg.IPBlock, sg.PrefixLength, sg.ID, map[string]string{
			efficientip.ParamZone: sg.DNSZone, efficientip.ParamDNSServers: strings.Join(sg.DNSServers, ",")})
		if errors.Is(err, efficientip.ErrBlockFull) {
			return "", "", permanent("EfficientIP block %s has no free /%d left: ask an administrator", sg.IPBlock, sg.PrefixLength)
		} else if err != nil {
			return "", "", fmt.Errorf("EfficientIP: %w", err)
		}
		prefix, err := netip.ParsePrefix(sub.CIDR)
		if err != nil {
			return "", "", fmt.Errorf("EfficientIP gave subnet %q: %w", sub.CIDR, err)
		}
		gateway := prefix.Masked().Addr().Next()
		out = SegmentResult{SubnetID: sub.ID, CIDR: prefix.Masked().String(), Gateway: gateway.String(), PrefixLength: prefix.Bits()}
		if err := a.recordSegmentResource(ctx, in, "ipam.subnet", out.CIDR, sub.ID, map[string]any{"subnet": out.CIDR,
			"block": sg.IPBlock, "efficientip_id": sub.ID, "segment": sg.Name}); err != nil {
			return "", "", err
		}

		err = a.NSX.PutSegment(ctx, nsx.Segment{ID: sg.ID, DisplayName: sg.Name, ConnectivityPath: nsx.Tier1Path + sg.Tier1,
			Subnets: []nsx.SegmentSubnet{{GatewayAddress: fmt.Sprintf("%s/%d", out.Gateway, out.PrefixLength)}},
			Tags:    []nsx.Tag{{Scope: nsx.OwnerScope, Tag: a.instance()}, {Scope: nsx.DeployScope, Tag: sg.DeploymentID}}})
		var apiErr *nsx.APIError
		if errors.As(err, &apiErr) && apiErr.Status == 400 {
			return "", "", permanent("NSX refused segment %s: %s", sg.Name, apiErr.Message)
		} else if err != nil {
			return "", "", fmt.Errorf("NSX: %w", err)
		}
		if err := a.recordSegmentResource(ctx, in, "nsx.segment", sg.Name, sg.ID, map[string]any{"segment_id": sg.ID,
			"tier1": sg.Tier1, "subnet": out.CIDR, "gateway": out.Gateway, "key": sg.Key}); err != nil {
			return "", "", err
		}

		// Realised by NSX, then a port group in vCenter VMs can join.
		vc, err := a.vc(ctx, in.VCenter)
		if err != nil {
			return "", "", err
		}
		wait := a.nsxTimeout()
		deadline := time.Now().Add(wait)
		for {
			state, err := a.NSX.SegmentState(ctx, sg.ID)
			if err != nil && !errors.Is(err, nsx.ErrNotFound) {
				return "", "", fmt.Errorf("NSX: %w", err)
			}
			if state == "failed" || state == "error" {
				return "", "", permanent("NSX couldn't realise segment %s (%s)", sg.Name, state)
			}
			if state == "success" {
				ok, err := vc.NetworkExists(ctx, sg.Name)
				if err != nil {
					return "", "", err
				}
				if ok {
					break
				}
			}
			if time.Now().After(deadline) {
				return "", "", fmt.Errorf("segment %s isn't a port group in vCenter after %s (NSX: %s)", sg.Name, wait, state)
			}
			select {
			case <-ctx.Done():
				return "", "", ctx.Err()
			case <-time.After(a.pollInterval()):
			}
		}

		if err := a.Store.SaveSegmentSubnet(ctx, store.NetworkSubnet{Network: sg.Name, Subnet: out.CIDR, Gateway: out.Gateway,
			DNSZone: sg.DNSZone, DNSServers: sg.DNSServers, EfficientIPSubnetID: sub.ID, SubnetName: sg.ID}); err != nil {
			return "", "", err
		}
		return "Make NSX segment " + sg.Name, fmt.Sprintf("On %s · subnet %s, gateway %s", sg.Tier1, out.CIDR, out.Gateway), nil
	})
	return out, err
}

func (a *Activities) pollInterval() time.Duration {
	if a.PollInterval > 0 {
		return a.PollInterval
	}
	return 2 * time.Second
}

// recordSegmentResource records a segment or its subnet on the deployment,
// once.
func (a *Activities) recordSegmentResource(ctx context.Context, in SegmentInput, typ, name, external string, props map[string]any) error {
	if a.findResource(ctx, in.RequestID, in.Segment.DeploymentID, typ, name) != nil {
		return nil
	}
	p, _ := json.Marshal(props)
	res, err := a.Store.UpsertResource(ctx, a.Store.Pool, store.Resource{ID: ids.New(ids.Resource), DeploymentID: in.Segment.DeploymentID,
		RequestID: &in.RequestID, Type: typ, Name: name, ExternalID: &external, Properties: p})
	if err != nil {
		return err
	}
	a.Events.Resource(ctx, in.ProjectID, res)
	return nil
}

// deleteSegment deletes a segment rvav made, and its mapping; one rvav
// didn't make is left alone.
func (a *Activities) deleteSegment(ctx context.Context, r store.Resource) (string, error) {
	if a.NSX == nil {
		return "", permanent("rvav has no NSX Manager: segment %s can't be deleted", r.Name)
	}
	id := r.Name
	if r.ExternalID != nil {
		id = *r.ExternalID
	}
	seg, err := a.NSX.Segment(ctx, id)
	switch {
	case errors.Is(err, nsx.ErrNotFound):
	case err != nil:
		return "", fmt.Errorf("NSX: %w", err)
	case !nsx.Owned(seg.Tags, a.instance()):
		return "", permanent("segment %s isn't rvav's in NSX any more: left alone", r.Name)
	default:
		if err := a.NSX.DeleteSegment(ctx, id); err != nil {
			return "", fmt.Errorf("NSX: %w", err)
		}
	}
	if err := a.Store.DeleteSegmentSubnet(ctx, r.Name); err != nil {
		return "", err
	}
	return "deleted NSX segment " + r.Name, nil
}

// deleteSubnet deletes a subnet carved for a segment.
func (a *Activities) deleteSubnet(ctx context.Context, r store.Resource) (string, error) {
	var p struct {
		ID string `json:"efficientip_id"`
	}
	_ = json.Unmarshal(r.Properties, &p)
	if err := a.IPAM.DeleteSubnet(ctx, p.ID); err != nil {
		return "", fmt.Errorf("EfficientIP: %w", err)
	}
	return "deleted subnet " + r.Name, nil
}
