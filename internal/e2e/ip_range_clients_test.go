package e2e

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

// TestCLIIPRanges reads rvav's IP ranges with rvavctl (I1.2): the list,
// a range by name with the VM that holds each address, and an address
// given back once no deployment has its VM.
func TestCLIIPRanges(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		var token api.ApiTokenCreated
		h.json("POST", "/api/v1/tokens", "mkeller", api.ApiTokenCreate{Name: "rvavctl"}, 201, &token)
		marc := newRvavctl(t, ctx)
		marc.env["RVAV_URL"], marc.env["RVAV_TOKEN"] = h.srv.URL, token.Token

		_, stderr := marc.run(0, "", "ip-ranges", "list")
		mustContain(t, "no ranges", stderr, "No IP ranges.")
		var lab api.IpRange
		h.json("POST", "/api/v1/admin/ip-ranges", "mkeller", labRange("Lab", "10.32.20.10", "10.32.20.19", "10.32.20.11"),
			201, &lab)
		for _, q := range []store.AllocationRequest{
			{RequestID: "req_lab", DeploymentID: "dep_lab", Hostname: "lab-db01"},
			{RequestID: "req_lab", DeploymentID: "dep_lab", Hostname: "lab-db01", NIC: "backup"},
		} {
			q.VCenter, q.Network = rangeVCenter, rangeNetwork
			if _, _, err := h.store.AllocateAddress(ctx, q); err != nil {
				t.Fatal(err)
			}
		}

		out, _ := marc.run(0, "", "ip-ranges", "list")
		mustContain(t, "ip-ranges list", out, "NETWORK", lab.Id, "Lab", rangeNetwork, "10.32.20.10-10.32.20.19", "7 of 9")
		var ranges []api.IpRange
		marc.json(&ranges, "ip-ranges", "list", "--network", "dvpg-app-dev-vl310")
		if len(ranges) != 0 {
			t.Fatalf("ranges of another network = %+v", ranges)
		}
		out, _ = marc.run(0, "", "ip-ranges", "show", "lab")
		mustContain(t, "ip-ranges show", out, "Lab ("+lab.Id+")", "10.32.20.0/24 via 10.32.20.1", "Excluded:", "10.32.20.11",
			"7 of 9", "none: rvav writes no DNS records", "ADDRESS", "10.32.20.10", "lab-db01", "backup", "10.32.20.12", "(none)")
		var shown struct {
			Range     api.IpRange        `json:"range"`
			Addresses []api.IpAllocation `json:"addresses"`
		}
		marc.json(&shown, "ip-ranges", "show", lab.Id)
		if shown.Range.Id != lab.Id || len(shown.Addresses) != 2 || shown.Addresses[1].Adapter == nil ||
			*shown.Addresses[1].Adapter != "backup" {
			t.Fatalf("ip-ranges show -o json = %+v", shown)
		}
		_, stderr = marc.run(1, "", "ip-ranges", "show", "Nope")
		mustContain(t, "unknown range", stderr, `no IP range "Nope"`, "rvavctl ip-ranges list")

		// dep_lab has no VM and its request isn't running: its addresses are
		// left over, and given back.
		_, stderr = marc.run(0, "", "ip-ranges", "release", "Lab", "10.32.20.12")
		mustContain(t, "release", stderr, "10.32.20.12 given back to Lab.")
		_, stderr = marc.run(1, "", "ip-ranges", "release", "Lab", "10.32.20.12")
		mustContain(t, "release again", stderr, "Not found: It does not exist")
		marc.json(&shown, "ip-ranges", "show", "Lab")
		if len(shown.Addresses) != 1 || shown.Range.Free != 8 {
			t.Fatalf("after the release: %d addresses, %d free", len(shown.Addresses), shown.Range.Free)
		}
	})
}

const ipRangeConfig = `
resource "rvav_ip_range" "lab" {
  name               = %q
  vcenter            = %q
  network            = %q
  cidr               = "10.32.20.0/24"
  start_address      = "10.32.20.10"
  end_address        = %q
  gateway            = "10.32.20.1"
  dns_servers        = ["10.32.20.53"]
  dns_search_domains = ["corp.example", "svc.corp.example"]
  excluded           = %s
}
`

// TestTerraformIPRanges manages an IP range with the provider (I1.2):
// create, import, change in place, drift (deleted outside Terraform), a
// destroy refused while a VM holds an address, and destroy.
func TestTerraformIPRanges(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end test")
	}
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		h := setup(t, ctx, c)
		tf := newTerraform(t, h, "mkeller")
		v1 := tf.config(ipRangeConfig, "Lab", rangeVCenter, rangeNetwork, "10.32.20.19", `["10.32.20.11"]`)
		v2 := tf.config(ipRangeConfig, "Lab apps", rangeVCenter, rangeNetwork, "10.32.20.40",
			`["10.32.20.11", "10.32.20.15-10.32.20.16"]`)
		var id string
		inRvav := func(name string, size int) resource.TestCheckFunc {
			return func(*terraform.State) error {
				var r api.IpRange
				h.json("GET", "/api/v1/admin/ip-ranges/"+id, "mkeller", nil, 200, &r)
				if r.Name != name || r.Size != size || deref(r.Description) != "" || r.DnsDomain != "" {
					return fmt.Errorf("range in rvav: %+v", r)
				}
				return nil
			}
		}
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: tf.factories,
			Steps: []resource.TestStep{
				{
					Config: v1,
					Check: resource.ComposeAggregateTestCheckFunc(
						grab("rvav_ip_range.lab", "id", &id),
						resource.TestCheckResourceAttr("rvav_ip_range.lab", "dns_search_domains.1", "svc.corp.example"),
						resource.TestCheckNoResourceAttr("rvav_ip_range.lab", "dns_domain"),
						inRvav("Lab", 9),
					),
				},
				{ResourceName: "rvav_ip_range.lab", ImportState: true, ImportStateVerify: true},
				{
					Config:           v2,
					ConfigPlanChecks: expect("rvav_ip_range.lab", plancheck.ResourceActionUpdate),
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttrPtr("rvav_ip_range.lab", "id", &id),
						inRvav("Lab apps", 28),
					),
				},
				{
					PreConfig: func() {
						h.json("DELETE", "/api/v1/admin/ip-ranges/"+id, "mkeller", nil, 204, nil)
					},
					Config:           v2,
					ConfigPlanChecks: expect("rvav_ip_range.lab", plancheck.ResourceActionCreate),
					Check:            grab("rvav_ip_range.lab", "id", &id),
				},
				{
					// A VM holds an address: rvav refuses to destroy the range.
					PreConfig: func() {
						if _, _, err := h.store.AllocateAddress(ctx, store.AllocationRequest{VCenter: rangeVCenter,
							Network: rangeNetwork, RequestID: "req_lab", DeploymentID: "dep_lab", Hostname: "lab-db01"}); err != nil {
							t.Fatal(err)
						}
					},
					Config:      tf.provider,
					ExpectError: regexp.MustCompile(`VMs hold addresses from Lab apps`),
				},
				{
					PreConfig: func() {
						h.json("DELETE", "/api/v1/admin/ip-ranges/"+id+"/allocations/10.32.20.10", "mkeller", nil, 204, nil)
					},
					Config: tf.provider,
				},
			},
			CheckDestroy: func(*terraform.State) error {
				var list api.IpRangeList
				h.json("GET", "/api/v1/admin/ip-ranges", "mkeller", nil, 200, &list)
				if len(list.Items) != 0 {
					return fmt.Errorf("ranges after destroy: %+v", list.Items)
				}
				return nil
			},
		})
	})
}
