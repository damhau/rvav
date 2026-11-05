package policy

import "testing"

func TestMaskLiterals(t *testing.T) {
	for _, c := range []struct {
		expr string
		lone bool
		want string
	}{
		// The review's probes: a token compared, a map picking a network,
		// other projects' codes.
		{"inputs.token == 'tok_live_SECRET'", false, "inputs.token == '…'"},
		{"{'prod':'vlan-pci-secret'}[inputs.env]", true, "{'…':'…'}[inputs.env]"},
		{`project.code in ["fin", "hr"] && inputs.size == "L"`, false, `project.code in ["…", "…"] && inputs.size == "…"`},
		// What the expression reads and how stays as written.
		{"inputs.data_gb >= 50", false, "inputs.data_gb >= 50"},
		{"inputs.nodes * 2 + 1", false, "inputs.nodes * 2 + 1"},
		{"inputs.backup == true && steps.nodes.fqdn != null", false, "inputs.backup == true && steps.nodes.fqdn != null"},
		{"inputs.database != ''", false, "inputs.database != ''"}, // an empty literal says nothing
		{`inputs.role + "_owner"`, false, `inputs.role + "…"`},
		{`inputs.post_config.exists(k, k.startsWith("secret-"))`, false, `inputs.post_config.exists(k, k.startsWith("…"))`},
		{`inputs.env == "prod" ? "vlan-1" : "vlan-2"`, true, `inputs.env == "…" ? "…" : "…"`},
		// Raw, bytes and triple-quoted literals, escapes, and text after one.
		{`r"a\d" + b'x' + """multi "line" """ + 'it\'s' + inputs.x`, false, `r"…" + b'…' + """…""" + '…' + inputs.x`},
		{`"é—ü" == inputs.name`, false, `"…" == inputs.name`},
		// Comments go.
		{"inputs.env == 'prod' // the PCI token is tok_123\n  && inputs.ok", false, "inputs.env == '…'\n  && inputs.ok"},
		// A lone literal, or a list of them, is the value the item uses.
		{`"dvpg-web-prod-vl110"`, true, `"dvpg-web-prod-vl110"`},
		{`['linux-baseline', 'idm-join']`, true, `['linux-baseline', 'idm-join']`},
		{`"dvpg-web-prod-vl110"`, false, `"…"`},
		{`['linux-baseline', inputs.extra]`, true, `['…', inputs.extra]`},
		// Not CEL: nothing of it.
		{"inputs.token == 'tok_live_SECRET", false, "…"},
		{"inputs.x ==", false, "…"},
	} {
		if got := MaskLiterals(c.expr, c.lone); got != c.want {
			t.Errorf("MaskLiterals(%q, %v) = %q, want %q", c.expr, c.lone, got, c.want)
		}
	}
}
