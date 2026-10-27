// Package present maps store rows to the API types generated from the
// OpenAPI spec. HTTP handlers and the event publisher share it so a request
// looks the same in a response and in an event.
package present

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/day2"
	"github.com/damhau/rvav/internal/store"
)

func Ptr[T any](v T) *T { return &v }

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func UserRef(id, name, email string) api.UserRef {
	r := api.UserRef{Id: id, Name: name}
	if email != "" {
		e := openapi_types.Email(email)
		r.Email = &e
	}
	return r
}

func ProjectRef(id, code, name string) api.ProjectRef {
	return api.ProjectRef{Id: id, Code: code, Name: name}
}

func Environment(e store.Environment) api.Environment {
	return api.Environment{Key: e.Key, Name: e.Name, Production: e.Production, Order: e.SortOrder}
}

func Project(p store.Project, role string) api.Project {
	out := api.Project{
		Id: p.ID, Code: p.Code, Name: p.Name,
		Description:  strPtr(p.Description),
		CostCenter:   strPtr(p.CostCenter),
		Environments: append([]string{}, p.Environments...),
	}
	if role != "" {
		r := api.ProjectRole(role)
		out.MyRole = &r
	}
	return out
}

func approvalHint(i store.CatalogItem) api.ApprovalHint {
	switch {
	case i.AlwaysApproval:
		return api.Always
	case i.HasApprovalPolicy:
		return api.Conditional
	}
	return api.Never
}

func CatalogItem(i store.CatalogItem) api.CatalogItem {
	out := api.CatalogItem{
		Id: i.ID, Slug: i.Slug, Name: i.Name, Summary: i.Summary,
		Category: i.Category, Type: api.CatalogItemType(i.Type), Icon: i.Icon,
		Approval: approvalHint(i), Favourite: i.Favourite,
		Featured:               Ptr(i.Featured),
		RequestsLast30Days:     Ptr(i.Requests30d),
		TypicalDurationSeconds: i.TypicalSeconds,
		DescriptionMd:          strPtr(i.DescriptionMD),
	}
	if len(i.Tags) > 0 {
		out.Tags = Ptr(append([]string{}, i.Tags...))
	}
	if i.PublishedVersionID != nil {
		out.Version = api.CatalogItemVersionRef{Id: *i.PublishedVersionID, PublishedAt: i.VersionPublishedAt}
		if i.VersionNumber != nil {
			out.Version.Number = *i.VersionNumber
		}
	}
	out.Source.Kind = api.CatalogItemSourceKind(i.SourceKind)
	out.Source.AwxTemplateId = i.AWXTemplateID
	out.Source.AwxTemplateName = i.AWXTemplateName
	return out
}

// RedactInputs replaces values of secret fields with "••••".
func RedactInputs(raw json.RawMessage, secret map[string]bool) map[string]any {
	inputs := map[string]any{}
	_ = json.Unmarshal(raw, &inputs)
	for k := range inputs {
		if secret[k] {
			inputs[k] = "••••"
		}
	}
	return inputs
}

// SecretFields lists the inputs marked secret in a UI schema.
func SecretFields(uiSchema json.RawMessage) map[string]bool {
	var ui api.UiSchema
	_ = json.Unmarshal(uiSchema, &ui)
	out := map[string]bool{}
	for k, f := range ui.Fields {
		if f.Secret != nil && *f.Secret {
			out[k] = true
		}
	}
	return out
}

func Request(r store.RequestRow, secret map[string]bool) api.Request {
	out := api.Request{
		Id: r.ID, Number: r.Number, Title: r.Title, Kind: api.Provision,
		Status:      api.RequestStatus(r.Status),
		Version:     api.CatalogItemVersionRef{Id: r.VersionID, Number: r.VersionNumber},
		Project:     ProjectRef(r.ProjectID, r.ProjectCode, r.ProjectName),
		Environment: r.Environment,
		Requester:   UserRef(r.RequesterID, r.RequesterName, r.RequesterEmail),
		SubmittedAt: r.SubmittedAt,
		ScheduledAt: r.ScheduledAt,
		StartedAt:   r.StartedAt,
		FinishedAt:  r.FinishedAt,
		Count:       Ptr(r.Count),
		Inputs:      Ptr(RedactInputs(r.Inputs, secret)),
	}
	out.CatalogItem.Id, out.CatalogItem.Slug, out.CatalogItem.Name = r.ItemID, r.ItemSlug, r.ItemName
	out.CatalogItem.Type = api.CatalogItemType(r.ItemType)
	if r.Kind == "day2" {
		out.Kind = api.Day2
		out.DeploymentId = r.DeploymentID
		if r.Action != nil {
			out.Action = &struct {
				Key  string `json:"key"`
				Name string `json:"name"`
			}{Key: *r.Action, Name: day2.Name(*r.Action)}
		}
		if r.ResourceID != nil && r.ResourceName != nil {
			out.Resource = &struct {
				Id   string `json:"id"`
				Name string `json:"name"`
			}{Id: *r.ResourceID, Name: *r.ResourceName}
		}
	}
	if r.Justification != "" {
		out.Justification = Ptr(r.Justification)
	}
	if r.OnBehalfOfID != nil && r.OnBehalfOfName != nil {
		email := ""
		if r.OnBehalfOfEmail != nil {
			email = *r.OnBehalfOfEmail
		}
		out.OnBehalfOf = Ptr(UserRef(*r.OnBehalfOfID, *r.OnBehalfOfName, email))
	}
	out.DeploymentIds = Ptr(append([]string{}, r.DeploymentIDs...))
	out.Progress.Done, out.Progress.Total = r.StepsDone, r.StepsTotal
	if r.CurrentStepID != nil && r.CurrentStepTitle != nil {
		out.CurrentStep = &struct {
			Id    string `json:"id"`
			Title string `json:"title"`
		}{Id: *r.CurrentStepID, Title: *r.CurrentStepTitle}
	}
	if r.ErrorMessage != nil && *r.ErrorMessage != "" {
		stepID := ""
		if r.ErrorStepID != nil {
			stepID = *r.ErrorStepID
		}
		out.Error = &struct {
			Message string `json:"message"`
			StepId  string `json:"step_id"`
		}{Message: *r.ErrorMessage, StepId: stepID}
	}
	return out
}

// Step maps one step without children.
func Step(s store.Step) api.Step {
	out := api.Step{
		Id: s.ID, Key: s.Key, Title: s.Title, Status: api.StepStatus(s.Status),
		StartedAt: s.StartedAt, FinishedAt: s.FinishedAt, HasLog: s.HasLog,
		Detail: strPtr(s.Detail), Error: s.Error, Children: []api.Step{},
	}
	if s.System != nil {
		out.System = Ptr(api.StepSystem(*s.System))
	}
	if s.ExternalKind != nil && s.ExternalID != nil {
		out.ExternalRef = &struct {
			Id   string                  `json:"id"`
			Kind api.StepExternalRefKind `json:"kind"`
			Url  *string                 `json:"url,omitempty"`
		}{Id: *s.ExternalID, Kind: api.StepExternalRefKind(*s.ExternalKind), Url: s.ExternalURL}
	}
	return out
}

// StepTree nests steps under their parents, keeping position order.
func StepTree(steps []store.Step) []api.Step {
	children := map[string][]store.Step{}
	var roots []store.Step
	for _, s := range steps {
		if s.ParentID == nil {
			roots = append(roots, s)
		} else {
			children[*s.ParentID] = append(children[*s.ParentID], s)
		}
	}
	var build func(s store.Step) api.Step
	build = func(s store.Step) api.Step {
		out := Step(s)
		kids := children[s.ID]
		sort.SliceStable(kids, func(i, j int) bool { return kids[i].Position < kids[j].Position })
		for _, k := range kids {
			out.Children = append(out.Children, build(k))
		}
		return out
	}
	sort.SliceStable(roots, func(i, j int) bool { return roots[i].Position < roots[j].Position })
	out := make([]api.Step, 0, len(roots))
	for _, r := range roots {
		out = append(out, build(r))
	}
	return out
}

func Deployment(d store.DeploymentRow) api.Deployment {
	out := api.Deployment{
		Id: d.ID, Name: d.Name, Status: api.DeploymentStatus(d.Status),
		Project:            ProjectRef(d.ProjectID, d.ProjectCode, d.ProjectName),
		Environment:        d.Environment,
		Version:            api.CatalogItemVersionRef{Id: d.VersionID, Number: d.VersionNumber},
		Owner:              UserRef(d.OwnerID, d.OwnerName, d.OwnerEmail),
		CreatedAt:          d.CreatedAt,
		CreatedByRequestId: d.CreatedByRequestID,
		VmCount:            d.VMCount,
		VmsPoweredOn:       d.VMsPoweredOn,
		SyncState:          api.SyncState(d.SyncState),
		Lease: api.Lease{
			ExpiresAt:      d.LeaseExpiresAt,
			ExtensionsUsed: Ptr(d.LeaseExtensionsUsed),
			ExtensionsMax:  d.LeaseExtensionsMax,
			ExpiryAction:   Ptr(api.LeaseExpiryAction(d.ExpiryAction)),
			ExpiredAt:      d.LeaseExpiredAt,
		},
	}
	if d.LeaseExpiresAt != nil {
		// The policy that applies now sets the limits.
		out.Lease.MaxDays = d.LeaseMaxDays
		if d.LeaseMaxExtensions != nil {
			out.Lease.ExtensionsMax = d.LeaseMaxExtensions
		}
	}
	if d.LeaseGraceDays != nil {
		out.Lease.GraceDays = Ptr(*d.LeaseGraceDays)
		if d.LeaseExpiredAt != nil && d.ExpiryAction == "power_off_then_delete" {
			at := d.LeaseExpiredAt.Add(time.Duration(*d.LeaseGraceDays) * 24 * time.Hour)
			out.Lease.DeletesAt = &at
		}
	}
	out.CatalogItem.Id, out.CatalogItem.Slug, out.CatalogItem.Name = d.ItemID, d.ItemSlug, d.ItemName
	if d.PrimaryIP != nil {
		out.PrimaryAddress = &struct {
			Fqdn *string `json:"fqdn,omitempty"`
			Ip   string  `json:"ip"`
		}{Ip: *d.PrimaryIP, Fqdn: d.PrimaryFQDN}
	}
	return out
}

func DeploymentDetail(d store.DeploymentRow, resources []store.Resource, secret map[string]bool) api.DeploymentDetail {
	b, _ := json.Marshal(Deployment(d))
	var out api.DeploymentDetail
	_ = json.Unmarshal(b, &out)
	out.Inputs = RedactInputs(d.Inputs, secret)
	out.Resources = make([]api.Resource, 0, len(resources))
	for _, r := range resources {
		if res, ok := Resource(r); ok {
			out.Resources = append(out.Resources, res)
		}
	}
	return out
}

// VMResourceProps is how a vsphere.vm resource stores its properties.
type VMResourceProps struct {
	VM api.VmProperties `json:"vm"`
}

// Resource maps a resource to the API union type.
func Resource(r store.Resource) (api.Resource, bool) {
	var out api.Resource
	switch r.Type {
	case "vsphere.vm":
		var p VMResourceProps
		if err := json.Unmarshal(r.Properties, &p); err != nil {
			return out, false
		}
		if p.VM.Disks == nil {
			p.VM.Disks = []struct {
				CapacityGb int     `json:"capacity_gb"`
				Datastore  *string `json:"datastore,omitempty"`
				Label      string  `json:"label"`
			}{}
		}
		if p.VM.Nics == nil {
			p.VM.Nics = []struct {
				IpAddresses *[]string `json:"ip_addresses,omitempty"`
				Label       *string   `json:"label,omitempty"`
				Mac         *string   `json:"mac,omitempty"`
				Network     string    `json:"network"`
			}{}
		}
		err := out.FromVmResource(api.VmResource{Id: r.ID, Name: r.Name, SyncState: api.SyncState(r.SyncState),
			Type: "vsphere.vm", Vm: p.VM})
		return out, err == nil
	case "ipam.address":
		var p struct {
			Address string `json:"address"`
			Subnet  string `json:"subnet"`
			ID      string `json:"efficientip_id"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		err := out.FromIpAddressResource(api.IpAddressResource{Id: r.ID, Name: r.Name, SyncState: api.SyncState(r.SyncState),
			Type: "ipam.address", Address: p.Address, Subnet: p.Subnet, EfficientipId: strPtr(p.ID)})
		return out, err == nil
	case "dns.record":
		var p struct {
			FQDN  string `json:"fqdn"`
			Type  string `json:"record_type"`
			Value string `json:"value"`
			Zone  string `json:"zone"`
		}
		_ = json.Unmarshal(r.Properties, &p)
		err := out.FromDnsRecordResource(api.DnsRecordResource{Id: r.ID, Name: r.Name, SyncState: api.SyncState(r.SyncState),
			Type: "dns.record", Fqdn: p.FQDN, RecordType: api.DnsRecordResourceRecordType(strings.ToUpper(p.Type)),
			Value: p.Value, Zone: strPtr(p.Zone)})
		return out, err == nil
	}
	return out, false
}
