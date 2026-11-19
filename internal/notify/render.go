package notify

import (
	"bytes"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"io"
	"slices"
	"strings"
	"sync"
	texttemplate "text/template"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/damhau/rvav/internal/brand"
)

// Templates per kind, channel and language (ADR-0023, ADR-0059). The
// built-in ones are templates/<locale>/<kind>.yaml: subject, text and html;
// Teams and Slack take the subject and text as their title and text. An
// administrator's template replaces a built-in one. _layout.tmpl puts an
// email's text and HTML in the greeting, facts, button and footer.

//go:embed all:templates
var templates embed.FS

// Locales are the languages messages are written in.
var Locales = []string{"en", "fr"}

// Channels are those whose messages have templates.
var Channels = []string{"email", "teams", "slack"}

// Parts are a template's sources: an email's subject, text and HTML; a
// Teams card's or a Slack message's title (Subject) and text.
type Parts struct {
	Subject string `yaml:"subject"`
	Text    string `yaml:"text"`
	HTML    string `yaml:"html"`
}

var builtins = sync.OnceValues(func() (map[string]map[string]Parts, error) {
	out := map[string]map[string]Parts{}
	for _, locale := range Locales {
		entries, err := templates.ReadDir("templates/" + locale)
		if err != nil {
			return nil, err
		}
		out[locale] = map[string]Parts{}
		for _, e := range entries {
			kind, ok := strings.CutSuffix(e.Name(), ".yaml")
			if !ok {
				continue
			}
			b, err := templates.ReadFile("templates/" + locale + "/" + e.Name())
			if err != nil {
				return nil, err
			}
			var p Parts
			if err := yaml.Unmarshal(b, &p); err != nil {
				return nil, fmt.Errorf("%s/%s: %w", locale, e.Name(), err)
			}
			out[locale][kind] = p
		}
	}
	return out, nil
})

// Default is the built-in template of a kind for a channel and language.
func Default(kind, channel, locale string) (Parts, bool) {
	all, err := builtins()
	if err != nil {
		return Parts{}, false
	}
	p, ok := all[Locale(locale)][kind]
	if channel != "email" {
		p.HTML = ""
	}
	return p, ok
}

// Locale is a language messages are written in: en for any other.
func Locale(locale string) string {
	if slices.Contains(Locales, locale) {
		return locale
	}
	return "en"
}

var months = map[string][]string{
	"en": {"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	"fr": {"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"},
}

// date writes a time as a date in the locale, in UTC.
func date(locale string, t *time.Time) string {
	if t == nil {
		return ""
	}
	u := t.UTC()
	m := months[Locale(locale)]
	if locale == "fr" {
		return fmt.Sprintf("%d %s %d à %02d:%02d UTC", u.Day(), m[u.Month()-1], u.Year(), u.Hour(), u.Minute())
	}
	return fmt.Sprintf("%d %s %d at %02d:%02d UTC", u.Day(), m[u.Month()-1], u.Year(), u.Hour(), u.Minute())
}

// Functions are what templates may call besides text/template's own.
var Functions = []Variable{
	{"date", "function", "Writes a date in the template's language, in UTC: {{date .Lease.ExpiresAt}}."},
	{"join", "function", `Joins a list with a separator: {{join .Approval.Reasons "; "}}.`},
}

func funcs(locale string) map[string]any {
	return map[string]any{
		"date": func(t *time.Time) string { return date(locale, t) },
		"join": strings.Join,
	}
}

// Size limits of a template's parts, in bytes.
const (
	MaxSubject = 1 << 10
	MaxText    = 20 << 10
	MaxHTML    = 200 << 10
)

var errDefine = errors.New("a template can't define others ({{define}} or {{block}})")

func parseText(kind, locale, name, src string) (*texttemplate.Template, error) {
	t, err := texttemplate.New(name).Funcs(funcs(locale)).Parse(src)
	if err != nil {
		return nil, parseError(err)
	}
	if len(t.Templates()) > 1 {
		return nil, errDefine
	}
	return t, checkFields(kind, t.Tree)
}

func parseHTML(kind, locale, src string) (*htmltemplate.Template, error) {
	t, err := htmltemplate.New("html").Funcs(funcs(locale)).Parse(src)
	if err != nil {
		return nil, parseError(err)
	}
	if len(t.Templates()) > 1 {
		return nil, errDefine
	}
	return t, checkFields(kind, t.Tree)
}

// parseError says where a template doesn't parse, "line 2: ...", without
// the template's internal name.
func parseError(err error) error {
	msg := strings.TrimPrefix(err.Error(), "html/template: ")
	if rest, ok := strings.CutPrefix(msg, "template: "); ok {
		// name:line: message, or name:line:column: message
		parts := strings.SplitN(rest, ":", 4)
		if len(parts) >= 3 && isNumber(parts[1]) {
			tail := strings.Join(parts[2:], ":")
			if len(parts) == 4 && isNumber(parts[2]) {
				tail = parts[3]
			}
			return fmt.Errorf("line %s: %s", parts[1], strings.TrimSpace(tail))
		}
	}
	return errors.New(msg)
}

// execError is why a template couldn't be written, without where in
// text/template it happened.
func execError(err error) error {
	msg := strings.TrimPrefix(err.Error(), "html/template: ")
	if _, after, ok := strings.Cut(msg, ": executing "); ok {
		if _, why, ok := strings.Cut(after, ": "); ok {
			return errors.New(why)
		}
	}
	return errors.New(msg)
}

func isNumber(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

type executor interface {
	Execute(w io.Writer, data any) error
}

func run(t executor, d any) (string, error) {
	var b bytes.Buffer
	if err := t.Execute(&b, d); err != nil {
		return "", execError(err)
	}
	return b.String(), nil
}

// compiled is a template's parts parsed.
type compiled struct {
	subject, text *texttemplate.Template
	html          *htmltemplate.Template
}

// Problem is what is wrong with a part of a template: subject, text or
// html.
type Problem struct{ Part, Message string }

func compile(kind, channel, locale string, p Parts) (compiled, []Problem) {
	var c compiled
	var problems []Problem
	add := func(part string, err error) {
		problems = append(problems, Problem{part, err.Error()})
	}
	fits := func(part, src string, limit int) bool {
		switch {
		case strings.TrimSpace(src) == "":
			add(part, errors.New("it is empty"))
		case len(src) > limit:
			add(part, fmt.Errorf("it is longer than %d KB", limit>>10))
		default:
			return true
		}
		return false
	}
	var err error
	if fits("subject", p.Subject, MaxSubject) {
		if c.subject, err = parseText(kind, locale, "subject", p.Subject); err != nil {
			add("subject", err)
		}
	}
	if fits("text", p.Text, MaxText) {
		if c.text, err = parseText(kind, locale, "text", p.Text); err != nil {
			add("text", err)
		}
	}
	switch {
	case channel != "email" && p.HTML != "":
		add("html", errors.New("only emails have an HTML part"))
	case channel == "email" && fits("html", p.HTML, MaxHTML):
		if c.html, err = parseHTML(kind, locale, p.HTML); err != nil {
			add("html", err)
		}
	}
	return c, problems
}

// written is what a template's parts give for some data.
type written struct{ Subject, Text, HTML string }

// write runs the parts. Teams and Slack text is markup: the data in it is
// escaped, what the template itself writes is not.
func (c compiled) write(channel string, d Data) (written, []Problem) {
	var out written
	var problems []Problem
	try := func(part string, t executor, d Data, dst *string) {
		s, err := run(t, d)
		if err != nil {
			problems = append(problems, Problem{part, err.Error()})
			return
		}
		*dst = s
	}
	textData := d
	switch channel {
	case "teams":
		textData = d.escaped(markdown.Replace)
	case "slack":
		textData = d.escaped(escape)
	}
	try("subject", c.subject, d, &out.Subject)
	try("text", c.text, textData, &out.Text)
	if c.html != nil {
		try("html", c.html, d, &out.HTML)
	}
	out.Subject = strings.Join(strings.Fields(out.Subject), " ")
	out.Text = strings.TrimSpace(out.Text)
	if len(problems) == 0 && out.Subject == "" {
		problems = append(problems, Problem{"subject", "it writes nothing"})
	}
	return out, problems
}

// Check says what is wrong with a template, part by part: it doesn't
// parse, names a field its kind doesn't have, or fails on the kind's
// sample.
func Check(kind, channel, locale string, p Parts) []Problem {
	c, problems := compile(kind, channel, locale, p)
	if len(problems) > 0 {
		return problems
	}
	_, problems = c.write(channel, Sample(kind, Locale(locale), "https://rvav.example", DefaultPortal))
	for i := range problems {
		problems[i].Message = "with the sample data, " + problems[i].Message
	}
	return problems
}

// Brand is the portal's branding as messages show it.
type Brand struct {
	// Name is the portal's name; "rvav" until one is set.
	Name string
	// Accent and AccentFg are the light theme's: the email's button.
	Accent, AccentFg string
	// Logo is the light theme's logo when it is a PNG or JPEG picture:
	// mail clients don't show SVG.
	Logo *Logo
}

// Logo is a picture an email carries.
type Logo struct {
	ContentType string
	Content     []byte
}

// LogoCID names the logo an email carries in its HTML.
const LogoCID = "logo@rvav"

func (b Brand) withDefaults() Brand {
	b.Name = portalOr(b.Name)
	if b.Accent == "" {
		b.Accent, b.AccentFg = brand.Default, "#ffffff"
	}
	return b
}

type layoutData struct {
	Data
	Subject          string
	Content          string
	ContentHTML      htmltemplate.HTML
	Facts            []Fact
	Button           string
	Accent, AccentFg htmltemplate.CSS
	LogoSrc          htmltemplate.URL
}

type layouts struct {
	text *texttemplate.Template
	html *htmltemplate.Template
}

var layoutCache sync.Map

func layoutOf(locale string) (layouts, error) {
	if l, ok := layoutCache.Load(locale); ok {
		return l.(layouts), nil
	}
	src, err := templates.ReadFile("templates/" + locale + "/_layout.tmpl")
	if err != nil {
		return layouts{}, err
	}
	var l layouts
	if l.text, err = texttemplate.New("layout").Parse(string(src)); err != nil {
		return l, err
	}
	if l.html, err = htmltemplate.New("layout").Parse(string(src)); err != nil {
		return l, err
	}
	layoutCache.Store(locale, l)
	return l, nil
}

// render writes a message from a template for a channel. An email's logo
// is logoSrc: its content ID when sent, a data URL in a preview.
func render(channel string, p Parts, d Data, b Brand, logoSrc string) (Message, error) {
	c, problems := compile(d.Kind, channel, d.Locale, p)
	if len(problems) == 0 {
		var w written
		if w, problems = c.write(channel, d); len(problems) == 0 {
			return compose(channel, w, d, b, logoSrc)
		}
	}
	return Message{}, fmt.Errorf("the %s template of %s in %s: %s: %s", channel, d.Kind, d.Locale, problems[0].Part,
		problems[0].Message)
}

func compose(channel string, w written, d Data, b Brand, logoSrc string) (Message, error) {
	m := Message{Data: d, Subject: w.Subject, Text: w.Text, Short: w.Text}
	if channel != "email" {
		return m, nil
	}
	l, err := layoutOf(d.Locale)
	if err != nil {
		return m, err
	}
	b = b.withDefaults()
	ld := layoutData{Data: d, Subject: w.Subject, Content: w.Text, Facts: m.Facts(),
		Button: labelsFor(d.Locale).button(d.Kind, d.Portal),
		// The HTML template's output, escaped by html/template as it was written.
		ContentHTML: htmltemplate.HTML(w.HTML), //nolint:gosec
		// Colours brand.Accents checked: #rrggbb.
		Accent: htmltemplate.CSS(b.Accent), AccentFg: htmltemplate.CSS(b.AccentFg), //nolint:gosec
		// A content ID or a data URL of a checked picture.
		LogoSrc: htmltemplate.URL(logoSrc)} //nolint:gosec
	if m.Text, err = run(l.text.Lookup("text"), ld); err != nil {
		return m, err
	}
	if m.HTML, err = run(l.html.Lookup("html"), ld); err != nil {
		return m, err
	}
	if b.Logo != nil && strings.HasPrefix(logoSrc, "cid:") {
		m.Logo = b.Logo
	}
	return m, nil
}

// Preview is a template written with its kind's sample, as it would be
// sent: an email's subject, text and HTML, a Teams card or Slack blocks.
type Preview struct {
	Message Message
	Card    map[string]any
	Blocks  any
}

// PreviewOf writes a template with its kind's sample data; an email shows
// its logo as a data URL.
func PreviewOf(kind, channel, locale string, p Parts, b Brand, publicURL string) (Preview, []Problem) {
	b = b.withDefaults()
	d := Sample(kind, Locale(locale), publicURL, b.Name)
	c, problems := compile(kind, channel, d.Locale, p)
	if len(problems) > 0 {
		return Preview{}, problems
	}
	w, problems := c.write(channel, d)
	if len(problems) > 0 {
		return Preview{}, problems
	}
	logo := ""
	if b.Logo != nil {
		logo = "data:" + b.Logo.ContentType + ";base64," + base64.StdEncoding.EncodeToString(b.Logo.Content)
	}
	m, err := compose(channel, w, d, b, logo)
	if err != nil {
		return Preview{}, []Problem{{"html", err.Error()}}
	}
	m.Logo = nil
	out := Preview{Message: m}
	switch channel {
	case "teams":
		out.Card = Card(m)
	case "slack":
		out.Blocks = Blocks(m)
	}
	return out, nil
}
