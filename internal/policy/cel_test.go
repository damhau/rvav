package policy

import (
	"strings"
	"testing"
)

// TestCostLimit: an expression that would run too long fails (S14.6).
func TestCostLimit(t *testing.T) {
	list := make([]any, 200)
	for i := range list {
		list[i] = i
	}
	vars := Vars{"inputs": map[string]any{"l": list}}
	if ok, err := Bool(`inputs.l.all(a, a >= 0)`, vars); err != nil || !ok {
		t.Fatalf("a short comprehension = %v, %v", ok, err)
	}
	_, err := Bool(`inputs.l.all(a, inputs.l.all(b, inputs.l.all(c, a + b + c >= 0)))`, vars)
	if err == nil || !strings.Contains(err.Error(), "cost") {
		t.Fatalf("8 million steps ran: %v", err)
	}
}
