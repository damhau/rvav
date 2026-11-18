package vraimport

import "strings"

// strategyFor is the placement rule strategy (ADR-0036) that places as a
// vRA placement policy does; "" for none. vRA's SPREAD counts machines per
// zone, rvav's spread VMs per cluster; SPREAD_MEMORY's lowest memory use
// comes nearest to the most free memory.
func strategyFor(policy string) string {
	switch strings.ToUpper(policy) {
	case "DEFAULT", "SPREAD_MEMORY":
		return "most_free_memory"
	case "BINPACK":
		return "binpack"
	case "SPREAD":
		return "spread"
	}
	return ""
}

// diskProvisioning is a vRA disk's provisioningType as rvav's; "" for
// another value or an expression.
func diskProvisioning(v any) string {
	switch s, _ := v.(string); s {
	case "thin":
		return "thin"
	case "thick":
		return "thick_lazy_zeroed"
	case "eagerZeroedThick":
		return "thick_eager_zeroed"
	}
	return ""
}
