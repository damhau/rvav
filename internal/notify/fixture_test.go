package notify

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"strconv"
	"strings"
	"testing"
	"text/template"
)

var update = flag.Bool("update", false, "rewrite testdata/templates.json from the kinds and the built-in templates")

// TestTemplatesFixture keeps testdata/templates.json, which the web app's
// mocks copy, the kinds, their variables and sample, and the built-in
// templates.
func TestTemplatesFixture(t *testing.T) {
	type kind struct {
		Kind      string                                  `json:"kind"`
		Name      string                                  `json:"name"`
		When      string                                  `json:"when"`
		Variables []Variable                              `json:"variables"`
		Sample    map[string]map[string]any               `json:"sample"`
		Defaults  map[string]map[string]map[string]string `json:"defaults"`
	}
	var fixture struct {
		Functions []Variable `json:"functions"`
		Kinds     []kind     `json:"kinds"`
	}
	fixture.Functions = Functions
	for _, k := range Kinds {
		out := kind{Kind: k.Kind, Name: k.Name, When: k.When, Variables: k.Variables, Sample: map[string]map[string]any{},
			Defaults: map[string]map[string]map[string]string{}}
		for _, locale := range Locales {
			d := Sample(k.Kind, locale, "https://rvav.corp.example", DefaultPortal)
			values := map[string]any{}
			for _, v := range k.Variables {
				values[v.Name] = sampleValue(t, d, v)
			}
			out.Sample[locale] = values
		}
		for _, channel := range Channels {
			out.Defaults[channel] = map[string]map[string]string{}
			for _, locale := range Locales {
				p, ok := Default(k.Kind, channel, locale)
				if !ok {
					t.Fatalf("%s has no built-in %s template in %s", k.Kind, channel, locale)
				}
				parts := map[string]string{"subject": p.Subject, "text": p.Text}
				if channel == "email" {
					parts["html"] = p.HTML
				}
				out.Defaults[channel][locale] = parts
			}
		}
		fixture.Kinds = append(fixture.Kinds, out)
	}
	var got bytes.Buffer
	enc := json.NewEncoder(&got)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(fixture); err != nil {
		t.Fatal(err)
	}
	const path = "testdata/templates.json"
	if *update {
		if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("%s isn't the kinds and the built-in templates: run go test ./internal/notify -run TestTemplatesFixture -update, read the change and copy it to web/src/mocks/notificationTemplates.json", path)
	}
}

// sampleValue is a variable of the sample as JSON has it: a list, a number,
// true or false, or text.
func sampleValue(t *testing.T, d Data, v Variable) any {
	t.Helper()
	write := func(src string) string {
		tpl, err := template.New("v").Funcs(funcs(d.Locale)).Parse(src)
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		if err := tpl.Execute(&b, d); err != nil {
			t.Fatalf("%s of %s: %v", v.Name, d.Kind, err)
		}
		return b.String()
	}
	switch v.Type {
	case "list of text":
		items := []string{}
		for s := range strings.SplitSeq(write("{{range "+v.Name+"}}{{.}}\x00{{end}}"), "\x00") {
			if s != "" {
				items = append(items, s)
			}
		}
		return items
	case "number":
		n, _ := strconv.Atoi(write("{{" + v.Name + "}}"))
		return n
	case "yes/no":
		return write("{{"+v.Name+"}}") == "true"
	case "date":
		return write("{{date " + v.Name + "}}")
	}
	return write("{{" + v.Name + "}}")
}
