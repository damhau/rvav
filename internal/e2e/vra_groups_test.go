package e2e

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/vra"
)

const vraGroupedTemplate = `formatVersion: 1
inputs:
  environment:
    type: string
    enum: [dev, prd]
  corp:
    type: object
    $ref: /ref/property-groups/corpInputs
resources:
  Cloud_vSphere_Machine_1:
    type: Cloud.vSphere.Machine
    properties:
      image: rhel9
      flavor: medium
      costCenter: ${input.corp.costCenter}
      dnsDomain: ${propgroup.dnsSettings.domain}
      cloudConfig: |
        #cloud-config
        fqdn: ${self.resourceName}.${propgroup.dnsSettings.domain}
`

// TestVRAPropertyGroups: vRA's property groups imported as rvav's (#138):
// made before the items that name them, at the version the plan says;
// imported again, nothing changes; one changed in rvav since is left as
// it is, and the plan says so.
func TestVRAPropertyGroups(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	model := simulator.VPX()
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		const admin = "mkeller"
		e := vra.SampleEstate(time.Now())
		e.Blueprints = append(e.Blueprints, vra.Blueprint{ID: "bp-grouped", Name: "Grouped server", ProjectID: "vra-prj-web",
			Status: "RELEASED", Content: vraGroupedTemplate, Versions: []vra.BlueprintVersion{
				{ID: "bp-grouped-v1", BlueprintID: "bp-grouped", Version: "1", Status: "RELEASED", Content: vraGroupedTemplate}}})
		h.json("POST", "/api/v1/admin/vra/snapshots", admin, e, 201, nil)

		plan := func() api.VraPlan {
			t.Helper()
			var p api.VraPlan
			h.json("GET", "/api/v1/admin/vra/plan", admin, nil, 200, &p)
			return p
		}
		group := func(p api.VraPlan, key string) api.VraPropertyGroupPlan {
			t.Helper()
			i := slices.IndexFunc(p.PropertyGroups, func(g api.VraPropertyGroupPlan) bool { return g.Key == key })
			if i < 0 {
				t.Fatalf("no plan for group %s in %+v", key, p.PropertyGroups)
			}
			return p.PropertyGroups[i]
		}
		item := func(p api.VraPlan) api.VraItemPlan {
			t.Helper()
			return p.Items[slices.IndexFunc(p.Items, func(x api.VraItemPlan) bool { return x.BlueprintId == "bp-grouped" })]
		}

		p := plan()
		if corp := group(p, "corpInputs"); corp.Action != "create" || corp.Version != 1 || corp.Type != "input" || len(corp.Inputs) != 2 {
			t.Fatalf("corp = %+v", corp)
		}
		if p.Summary.PropertyGroupsCreate != 2 || item(p).Action != "create" {
			t.Fatalf("plan = %+v, item %+v", p.Summary, item(p))
		}

		var res api.VraImportResult
		h.json("POST", "/api/v1/admin/vra/import", admin, api.VraImportRequest{}, 200, &res)
		if !slices.Equal(res.PropertyGroupsCreated, []string{"corpInputs", "dnsSettings"}) || !slices.Contains(res.Created, "Grouped server") ||
			len(res.Failed) != 0 {
			t.Fatalf("import = %+v", res)
		}
		// Used by no item yet: the imported one is a draft.
		var dns api.PropertyGroup
		h.json("GET", "/api/v1/admin/property-groups/dnsSettings", admin, nil, 200, &dns)
		if dns.Name != "DNS settings" || dns.Version != 1 || dns.Constants["domain"] != "corp.example" || dns.Constants["ttl"] != float64(300) ||
			len(dns.UsedBy) != 0 {
			t.Fatalf("dns = %+v", dns)
		}

		id := deref(item(res.Plan).RvavItemId)
		var it api.AdminCatalogItem
		h.json("GET", "/api/v1/admin/catalog/items/"+id, admin, nil, 200, &it)
		var v api.CatalogItemVersionDetail
		h.json("GET", "/api/v1/admin/catalog/items/"+id+"/versions/"+it.Versions[0].Id, admin, nil, 200, &v)
		bp := v.Definition.Blueprint
		refs, _ := json.Marshal(bp["property_groups"])
		ud := bp["guest"].(map[string]any)["cloud_init"].(map[string]any)["user_data"].(string)
		props, _ := bp["properties"].(map[string]any)
		if string(refs) != `[{"key":"corpInputs","version":1},{"key":"dnsSettings","version":1}]` ||
			!strings.Contains(ud, "fqdn: {{ .vm.name }}.corp.example") || props["costCenter"] != "${inputs.costCenter}" ||
			props["dnsDomain"] != "${propgroup.dnsSettings.domain}" {
			t.Fatalf("grouped blueprint = %s %+v", refs, bp)
		}

		// Again: the groups are rvav's as vRA's give them.
		p = plan()
		if group(p, "corpInputs").Action != "unchanged" || item(p).Action != "unchanged" || p.Summary.PropertyGroupsCreate != 0 {
			t.Fatalf("again = %+v %+v", p.PropertyGroups, item(p))
		}

		// Changed in rvav: left as it is, and items name its latest (this one,
		// still a draft, waits for it to be published or discarded).
		corp := group(p, "corpInputs")
		h.json("PUT", "/api/v1/admin/property-groups/corpInputs", admin, api.PropertyGroupWrite{Name: "Corporate inputs",
			Inputs: corp.Inputs[:1], Constants: map[string]any{}}, 200, nil)
		p = plan()
		if g := group(p, "corpInputs"); g.Action != "differs" || g.Version != 2 {
			t.Fatalf("changed in rvav = %+v", g)
		}
		if it := item(p); it.Action != "conflict" || !slices.ContainsFunc(it.Notes, func(n api.VraNote) bool {
			return strings.Contains(n.Text, "Property group corpInputs differs in rvav")
		}) {
			t.Fatalf("item after the change = %+v", it)
		}
	}, model)
}
