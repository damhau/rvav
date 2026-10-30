// Package vraimport turns what internal/vra read from vRA into rvav's own
// objects (P2, ADR-0027), and says for each what it couldn't carry over
// and why. It maps; it doesn't guess: what has no equivalent is a note,
// never an approximation.
package vraimport

import (
	"bytes"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/vra"
)

// Note is something about a mapping a person should know: info (how it
// was mapped), warning (mapped, but check it) or unmapped (left out).
type Note struct {
	Level string `json:"level"` // info, warning, unmapped
	Where string `json:"where"` // the vRA part: inputs.size, resources.lb, form.dbVersion...
	Text  string `json:"text"`
}

// Note levels.
const (
	Info     = "info"
	Warning  = "warning"
	Unmapped = "unmapped"
)

// Item statuses: everything mapped; mapped with parts left out; not
// mapped at all.
const (
	Complete = "complete"
	Partial  = "partial"
	Skipped  = "skipped"
)

// ItemDoc is a catalog item as the catalog's YAML import takes it
// (ADR-0019).
type ItemDoc struct {
	APIVersion string       `yaml:"apiVersion" json:"apiVersion"`
	Kind       string       `yaml:"kind" json:"kind"`
	Metadata   ItemMeta     `yaml:"metadata" json:"metadata"`
	Versions   []VersionDoc `yaml:"versions" json:"versions"`
}

type ItemMeta struct {
	Slug        string   `yaml:"slug" json:"slug"`
	Name        string   `yaml:"name" json:"name"`
	Summary     string   `yaml:"summary" json:"summary"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Category    string   `yaml:"category" json:"category"`
	Type        string   `yaml:"type" json:"type"`
	Icon        string   `yaml:"icon" json:"icon"`
	Tags        []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

type VersionDoc struct {
	Number        string         `yaml:"number" json:"number"`
	Changelog     string         `yaml:"changelog,omitempty" json:"changelog,omitempty"`
	FormSchema    map[string]any `yaml:"form_schema" json:"form_schema"`
	UISchema      map[string]any `yaml:"ui_schema" json:"ui_schema"`
	InitialValues map[string]any `yaml:"initial_values,omitempty" json:"initial_values,omitempty"`
	Blueprint     map[string]any `yaml:"blueprint" json:"blueprint"`
}

// slug is a name made fit for a catalog slug.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// YAML is the document as the catalog's import takes it.
func (d *ItemDoc) YAML() ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Plan is what an import would make of an estate: so far its catalog
// items.
type Plan struct {
	Items []ItemPlan `json:"items"`
}

// MakePlan maps everything in the estate.
func MakePlan(e *vra.Estate) Plan {
	var p Plan
	for _, bp := range e.Blueprints {
		p.Items = append(p.Items, MapTemplate(e, bp))
	}
	return p
}
