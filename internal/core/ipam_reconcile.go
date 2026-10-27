package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/auth"
	"github.com/damhau/rvav/internal/present"
)

// ReconcileIPAM compares EfficientIP with what deployments own: addresses
// and records tagged rvav_managed=1 that no deployment owns, and owned ones
// EfficientIP no longer has (ADR-0015). It changes nothing.
func (s *Service) ReconcileIPAM(ctx context.Context, p *auth.Principal) (api.IpamReconcile, error) {
	report := api.IpamReconcile{CheckedAt: time.Now().UTC(), OrphanAddresses: []api.IpamOrphanAddress{},
		OrphanRecords: []api.IpamOrphanRecord{}, Missing: []api.IpamMissing{}}
	if !p.IsAdmin() {
		return report, ErrForbidden
	}
	if s.IPAM == nil {
		return report, &UpstreamError{System: "EfficientIP", Err: errors.New("no EfficientIP is configured")}
	}
	addresses, records, err := s.IPAM.Managed(ctx)
	if err != nil {
		return report, &UpstreamError{System: "EfficientIP", Err: err}
	}
	owned, err := s.Store.IPAMResources(ctx)
	if err != nil {
		return report, err
	}
	report.ManagedAddresses, report.ManagedRecords = len(addresses), len(records)

	type props struct {
		ID      string `json:"efficientip_id"`
		Address string `json:"address"`
		FQDN    string `json:"fqdn"`
		Type    string `json:"record_type"`
		Value   string `json:"value"`
	}
	recordKey := func(typ, fqdn, value string) string { return typ + " " + normalize(fqdn) + " " + normalize(value) }
	idKey := func(resourceType, id string) string {
		if resourceType == "ipam.address" {
			return "ip:" + id
		}
		return "rr:" + id
	}
	ownedIDs, ownedIPs, ownedRecords := map[string]bool{}, map[string]bool{}, map[string]bool{}
	parsed := make([]props, len(owned))
	for i, r := range owned {
		_ = json.Unmarshal(r.Properties, &parsed[i])
		pr := parsed[i]
		if pr.ID != "" {
			ownedIDs[idKey(r.Type, pr.ID)] = true
		}
		if r.Type == "ipam.address" {
			ownedIPs[pr.Address] = true
		} else {
			ownedRecords[recordKey(pr.Type, pr.FQDN, pr.Value)] = true
		}
	}

	managed := map[string]bool{}
	for _, a := range addresses {
		managed["ip:"+a.ID] = true
		if !ownedIDs["ip:"+a.ID] && !ownedIPs[a.IP] {
			report.OrphanAddresses = append(report.OrphanAddresses, api.IpamOrphanAddress{EfficientipId: a.ID, Ip: a.IP, Name: a.Hostname, Subnet: a.Subnet})
		}
	}
	for _, r := range records {
		managed["rr:"+r.ID] = true
		if !ownedIDs["rr:"+r.ID] && !ownedRecords[recordKey(r.Type, r.FQDN, r.Value)] {
			report.OrphanRecords = append(report.OrphanRecords, api.IpamOrphanRecord{EfficientipId: r.ID, Fqdn: r.FQDN, Type: r.Type, Value: r.Value})
		}
	}

	// Owned objects not among the tagged ones may still exist (a record
	// adopted from SOLIDserver has no tag): ask for them by ID.
	var addressIDs, recordIDs []string
	for i, r := range owned {
		id := parsed[i].ID
		switch {
		case id == "" || managed[idKey(r.Type, id)]:
		case r.Type == "ipam.address":
			addressIDs = append(addressIDs, id)
		default:
			recordIDs = append(recordIDs, id)
		}
	}
	haveAddresses, haveRecords, err := s.IPAM.Existing(ctx, addressIDs, recordIDs)
	if err != nil {
		return report, &UpstreamError{System: "EfficientIP", Err: err}
	}
	for i, r := range owned {
		id := parsed[i].ID
		if id == "" || managed[idKey(r.Type, id)] {
			continue
		}
		if (r.Type == "ipam.address" && !haveAddresses[id]) || (r.Type == "dns.record" && !haveRecords[id]) {
			report.Missing = append(report.Missing, api.IpamMissing{ResourceId: r.ID, DeploymentId: r.DeploymentID,
				DeploymentName: r.DeploymentName, Type: api.IpamMissingType(r.Type), Name: r.Name, EfficientipId: present.Ptr(id)})
		}
	}
	return report, nil
}

func normalize(name string) string { return strings.ToLower(strings.TrimRight(name, ".")) }
