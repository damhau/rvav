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

func TestCounter(t *testing.T) {
	tpl, err := Parse("web{seq:03}")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Start() != 1 || tpl.Step() != 1 {
		t.Fatalf("default counter %d, %d", tpl.Start(), tpl.Step())
	}
	tpl = tpl.WithCounter(0, 5)
	if tpl.Start() != 0 || tpl.Step() != 5 || tpl.Name("web", tpl.Start()+tpl.Step()) != "web005" {
		t.Fatalf("counter %d, %d", tpl.Start(), tpl.Step())
	}
	if n := tpl.WithCounter(1000, 1).Name("web", 1000); n != "web1000" {
		t.Fatalf("a number wider than the padding = %s", n)
	}
}
