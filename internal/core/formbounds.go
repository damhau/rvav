package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/message"
)

// formatBoundsVocab adds formatMinimum and formatMaximum, the bounds of a
// date or date-time input (the keywords JSON Schema's format-assertion
// drafts gave them). JSON Schema's minimum and maximum bound numbers only.
func formatBoundsVocab() *jsonschema.Vocabulary {
	const url = "urn:rvav:vocab:format-bounds"
	meta, err := jsonschema.UnmarshalJSON(strings.NewReader(`{"properties": {
		"formatMinimum": {"type": "string"}, "formatMaximum": {"type": "string"}}}`))
	if err != nil {
		panic(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, meta); err != nil {
		panic(err)
	}
	return &jsonschema.Vocabulary{URL: url, Schema: c.MustCompile(url), Compile: compileFormatBounds}
}

var boundLayouts = map[string]string{"date-time": time.RFC3339, "date": time.DateOnly}

type formatBounds struct {
	format   string
	min, max *time.Time
	minText  string
	maxText  string
}

func compileFormatBounds(_ *jsonschema.CompilerContext, obj map[string]any) (jsonschema.SchemaExt, error) {
	lo, hasLo := obj["formatMinimum"].(string)
	hi, hasHi := obj["formatMaximum"].(string)
	if !hasLo && !hasHi {
		return nil, nil
	}
	format, _ := obj["format"].(string)
	layout, ok := boundLayouts[format]
	if !ok {
		return nil, fmt.Errorf("formatMinimum and formatMaximum bound a date or date-time input only, not %q", format)
	}
	b := &formatBounds{format: format, minText: lo, maxText: hi}
	for _, bound := range []struct {
		set  bool
		text string
		into **time.Time
	}{{hasLo, lo, &b.min}, {hasHi, hi, &b.max}} {
		if !bound.set {
			continue
		}
		t, err := time.Parse(layout, bound.text)
		if err != nil {
			return nil, fmt.Errorf("bound %q isn't a %s", bound.text, format)
		}
		*bound.into = &t
	}
	return b, nil
}

func (b *formatBounds) Validate(ctx *jsonschema.ValidatorContext, v any) {
	s, ok := v.(string)
	if !ok {
		return
	}
	t, err := time.Parse(boundLayouts[b.format], s)
	if err != nil {
		return // the format's own check says what is wrong
	}
	if b.min != nil && t.Before(*b.min) {
		ctx.AddError(&tooEarly{format: b.format, bound: b.minText})
	}
	if b.max != nil && t.After(*b.max) {
		ctx.AddError(&tooLate{format: b.format, bound: b.maxText})
	}
}

type tooEarly struct{ format, bound string }

func (*tooEarly) KeywordPath() []string { return []string{"formatMinimum"} }

func (k *tooEarly) LocalizedString(p *message.Printer) string {
	return p.Sprintf("Pick %s on or after %s.", boundNoun(k.format), k.bound)
}

type tooLate struct{ format, bound string }

func (*tooLate) KeywordPath() []string { return []string{"formatMaximum"} }

func (k *tooLate) LocalizedString(p *message.Printer) string {
	return p.Sprintf("Pick %s on or before %s.", boundNoun(k.format), k.bound)
}

func boundNoun(format string) string {
	if format == "date" {
		return "a date"
	}
	return "a time"
}
