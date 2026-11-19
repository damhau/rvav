package core

import (
	"testing"

	"github.com/damhau/rvav/internal/api"
	"github.com/damhau/rvav/internal/store"
)

func TestLimitsHere(t *testing.T) {
	web, dev := "prj_web", "dev"
	n := func(v int) *int { return &v }
	pol := func(id string, project *string, enforcement string, vcpu *int) store.DeploymentLimitPolicy {
		return store.DeploymentLimitPolicy{ID: id, Name: id, ProjectID: project, Enabled: true, Enforcement: enforcement, VCPUPerDeployment: vcpu}
	}
	hard, soft := store.EnforcementHard, store.EnforcementSoft
	cases := []struct {
		name string
		all  []store.DeploymentLimitPolicy
		want string
	}{
		{"none", nil, ""},
		{"the first that sets it", []store.DeploymentLimitPolicy{pol("a", nil, hard, nil), pol("b", nil, hard, n(8)), pol("c", nil, hard, n(4))}, "b"},
		{"a global first decides", []store.DeploymentLimitPolicy{pol("g", nil, soft, n(16)), pol("p", &web, hard, n(4))}, "g"},
		{"a project's tightens a global", []store.DeploymentLimitPolicy{pol("p", &web, hard, n(4)), pol("g", nil, hard, n(16))}, "p"},
		{"a hard stricter global wins", []store.DeploymentLimitPolicy{pol("p", &web, hard, n(16)), pol("g", nil, hard, n(4))}, "g"},
		{"a soft global doesn't", []store.DeploymentLimitPolicy{pol("p", &web, hard, n(16)), pol("g", nil, soft, n(4))}, "p"},
		{"the same, hard over a soft project's", []store.DeploymentLimitPolicy{pol("p", &web, soft, n(4)), pol("g", nil, hard, n(4))}, "g"},
		{"only the first global after", []store.DeploymentLimitPolicy{pol("p", &web, hard, n(16)), pol("g1", nil, hard, n(32)),
			pol("g2", nil, hard, n(2))}, "p"},
		{"another project's", []store.DeploymentLimitPolicy{pol("o", new("prj_fin"), hard, n(1)), pol("g", nil, hard, n(8))}, "g"},
		{"disabled", []store.DeploymentLimitPolicy{func() store.DeploymentLimitPolicy {
			p := pol("off", nil, hard, n(1))
			p.Enabled = false
			return p
		}(), pol("g", nil, hard, n(8))}, "g"},
		{"another environment", []store.DeploymentLimitPolicy{func() store.DeploymentLimitPolicy {
			p := pol("prd", nil, hard, n(1))
			p.Environment = new("prd")
			return p
		}(), pol("g", nil, hard, n(8))}, "g"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chosen, _, _ := limitsHere(c.all, web, dev)
			got := chosen[api.VcpuPerDeployment].pol.ID
			if got != c.want {
				t.Fatalf("decided by %q, want %q", got, c.want)
			}
		})
	}
}

func TestLimitsHereWhy(t *testing.T) {
	web := "prj_web"
	n := func(v int) *int { return &v }
	all := []store.DeploymentLimitPolicy{
		{ID: "p", Name: "Web", ProjectID: &web, Enabled: true, Enforcement: store.EnforcementHard, DeploymentsPerUser: n(3), VCPUPerDeployment: n(16),
			MemoryGBPerDeployment: n(64)},
		{ID: "g", Name: "rvav", Enabled: true, Enforcement: store.EnforcementHard, VCPUPerDeployment: n(8)},
	}
	_, why, decides := limitsHere(all, web, "dev")
	if want := "Decides deployments per user and memory per deployment. Its vCPU per deployment gives way to the global rvav, hard and stricter."; why["p"] != want || !decides["p"] {
		t.Fatalf("why p = %q", why["p"])
	}
	if why["g"] != "Decides vCPU per deployment." || !decides["g"] {
		t.Fatalf("why g = %q", why["g"])
	}
}
