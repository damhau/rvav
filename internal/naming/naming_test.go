package naming

import "testing"

func TestComponentAndLabels(t *testing.T) {
	tpl, err := Parse("{project}-{environment}-{component}{seq:02}")
	if err != nil {
		t.Fatal(err)
	}
	vals := Values("web", "prd", nil)
	vals["component"] = "db"
	if p, err := tpl.Prefix(vals); err != nil || tpl.Name(p, 1) != "web-prd-db01" {
		t.Fatalf("prefix %q, %v", p, err)
	}
	// A single VM's component is empty.
	vals["component"] = ""
	if p, err := tpl.Prefix(vals); err != nil || p != "web-prd-" {
		t.Fatalf("prefix %q, %v", p, err)
	}

	label, err := ParseLabel("{project}-{environment}-{inputs.app}")
	if err != nil {
		t.Fatal(err)
	}
	if name, err := label.Prefix(Values("web", "prd", map[string]any{"app": "Shop"})); err != nil || name != "web-prd-shop" {
		t.Fatalf("label %q, %v", name, err)
	}
	if _, err := ParseLabel("{project}{seq:02}"); err == nil {
		t.Fatal("a label with {seq} parsed")
	}
	if _, err := Parse("{project}"); err == nil {
		t.Fatal("a name without {seq} parsed")
	}
}
