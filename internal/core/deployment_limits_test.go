package core

import (
	"maps"
	"slices"
	"strings"
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
		{"the first that sets it", []store.DeploymentLimitPolicy{pol("a", nil, hard, nil), pol("b", nil, hard, n(8)), pol("c", nil, hard, n(4))}, "b c"},
		{"a soft global first decides alone", []store.DeploymentLimitPolicy{pol("g", nil, soft, n(16)), pol("p", &web, hard, n(4))}, "g"},
		{"a project's tightens a hard global", []store.DeploymentLimitPolicy{pol("p", &web, hard, n(4)), pol("g", nil, hard, n(16))}, "p g"},
		{"a hard global holds over a looser project's", []store.DeploymentLimitPolicy{pol("p", &web, hard, n(16)), pol("g", nil, hard, n(4))}, "p g"},
		{"a soft global gives way", []store.DeploymentLimitPolicy{pol("p", &web, hard, n(16)), pol("g", nil, soft, n(4))}, "p"},
		{"a hard global holds over a soft project's", []store.DeploymentLimitPolicy{pol("p", &web, soft, n(1)), pol("g", nil, hard, n(4))}, "p g"},
		{"every hard global", []store.DeploymentLimitPolicy{pol("p", &web, hard, n(16)), pol("g1", nil, hard, n(32)),
			pol("g2", nil, hard, n(2))}, "p g1 g2"},
		{"not a project's after", []store.DeploymentLimitPolicy{pol("g", nil, hard, n(8)), pol("p", &web, hard, n(2))}, "g"},
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
			var got []string
			for _, h := range limitsHere(c.all, web, dev)[api.VcpuPerDeployment] {
				got = append(got, h.pol.ID)
			}
			if strings.Join(got, " ") != c.want {
				t.Fatalf("held by %q, want %q", got, c.want)
			}
		})
	}
}

func TestBindingLine(t *testing.T) {
	hard, soft := api.PolicyEnforcementHard, api.PolicyEnforcementSoft
	line := func(policy string, limit, value int, enf api.PolicyEnforcement) api.DeploymentLimitLine {
		return api.DeploymentLimitLine{Policy: policy, Limit: limit, Value: value, Exceeds: value > limit, Enforcement: enf}
	}
	cases := []struct {
		name  string
		lines []api.DeploymentLimitLine
		want  string
	}{
		{"the least room", []api.DeploymentLimitLine{line("p", 8, 2, hard), line("g", 4, 2, hard)}, "g"},
		{"the first on a tie", []api.DeploymentLimitLine{line("p", 4, 2, hard), line("g", 4, 2, hard)}, "p"},
		{"a hard one gone over", []api.DeploymentLimitLine{line("p", 1, 2, soft), line("g", 1, 2, hard)}, "g"},
		{"a soft one gone over", []api.DeploymentLimitLine{line("p", 1, 2, soft), line("g", 4, 2, hard)}, "p"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := bindingLine(c.lines).Policy; got != c.want {
				t.Fatalf("shown %q, want %q", got, c.want)
			}
		})
	}
}

func TestLimitReasons(t *testing.T) {
	web := "prj_web"
	n := func(v int) *int { return &v }
	all := []store.DeploymentLimitPolicy{
		{ID: "p", Name: "Web", ProjectID: &web, Enabled: true, Enforcement: store.EnforcementHard, DeploymentsPerUser: n(3), VCPUPerDeployment: n(16),
			MemoryGBPerDeployment: n(64)},
		{ID: "g", Name: "rvav", Enabled: true, Enforcement: store.EnforcementHard, VCPUPerDeployment: n(8), MemoryGBPerDeployment: n(128)},
	}
	held := limitsHere(all, web, "dev")
	shown := map[api.DeploymentLimitKind]limitChoice{}
	for k, h := range held {
		shown[k] = tightestLimit(h, func(c limitChoice) int { return c.limit })
	}
	why, holds := limitReasons(held, shown)
	if want := "Decides deployments per user and memory per deployment. Its vCPU per deployment gives way to the global rvav, hard and stricter."; why["p"] != want || !holds["p"] {
		t.Fatalf("why p = %q", why["p"])
	}
	if want := "Decides vCPU per deployment. Its memory per deployment holds too: a hard global limit applies on top of the policy that decides."; why["g"] != want || !holds["g"] {
		t.Fatalf("why g = %q", why["g"])
	}
	if got := slices.Sorted(maps.Keys(holds)); !slices.Equal(got, []string{"g", "p"}) {
		t.Fatalf("holds = %v", got)
	}
}
