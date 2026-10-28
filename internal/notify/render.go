package notify

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	"strings"
	"sync"
	texttemplate "text/template"
	"time"

	"github.com/damhau/rvav/internal/store"
)

// Templates per kind and locale (ADR-0023): templates/<locale>/<kind>.tmpl
// defines "subject", "text" and "html_body"; _layout.tmpl wraps the HTML
// and adds the footers.

//go:embed all:templates
var templates embed.FS

// Locales are the languages messages are written in.
var Locales = []string{"en", "fr"}

// Data is what templates get.
type Data struct {
	Kind, Locale, UserName string
	// Title, Body and Level are the notification's, as the app shows it
	// (English); Body carries what can't be translated, such as an error.
	Title, Body, Level string
	// Link opens what it is about in rvav; Preferences, the user's choices.
	Link, Preferences string
	Subject           store.NotificationSubject
	// Extra is what the occasion added (days left, the decision's comment).
	Extra map[string]any
}

// Str is an extra value as text.
func (d Data) Str(key string) string {
	if v, ok := d.Extra[key]; ok && v != nil {
		return fmt.Sprint(v)
	}
	return ""
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
	m := months[locale]
	if m == nil {
		m = months["en"]
	}
	if locale == "fr" {
		return fmt.Sprintf("%d %s %d à %02d:%02d UTC", u.Day(), m[u.Month()-1], u.Year(), u.Hour(), u.Minute())
	}
	return fmt.Sprintf("%d %s %d at %02d:%02d UTC", u.Day(), m[u.Month()-1], u.Year(), u.Hour(), u.Minute())
}

func funcs(locale string) map[string]any {
	return map[string]any{
		"date": func(t *time.Time) string { return date(locale, t) },
		"join": strings.Join,
	}
}

type parsed struct {
	text *texttemplate.Template
	html *htmltemplate.Template
}

var cache sync.Map // locale/kind -> parsed

func load(kind, locale string) (parsed, error) {
	key := locale + "/" + kind
	if p, ok := cache.Load(key); ok {
		return p.(parsed), nil
	}
	layout, err := templates.ReadFile("templates/" + locale + "/_layout.tmpl")
	if err != nil {
		return parsed{}, err
	}
	body, err := templates.ReadFile("templates/" + locale + "/" + kind + ".tmpl")
	if err != nil {
		return parsed{}, fmt.Errorf("no %s template for %s", locale, kind)
	}
	// The kind's own parsed after the layout: its definitions (a button's
	// label) replace the layout's.
	var p parsed
	if p.text, err = texttemplate.New(kind).Funcs(funcs(locale)).Parse(string(layout)); err != nil {
		return p, err
	}
	if p.text, err = p.text.Parse(string(body)); err != nil {
		return p, err
	}
	if p.html, err = htmltemplate.New(kind).Funcs(funcs(locale)).Parse(string(layout)); err != nil {
		return p, err
	}
	if p.html, err = p.html.Parse(string(body)); err != nil {
		return p, err
	}
	cache.Store(key, p)
	return p, nil
}

// render writes a notification in a locale; a locale without its
// templates falls back to English.
func render(kind, locale string, d Data) (Message, error) {
	p, err := load(kind, locale)
	if err != nil && locale != "en" {
		d.Locale = "en"
		p, err = load(kind, "en")
	}
	if err != nil {
		return Message{}, err
	}
	var m Message
	run := func(name string, dst *string) error {
		var b bytes.Buffer
		if err := p.text.ExecuteTemplate(&b, name, d); err != nil {
			return err
		}
		*dst = strings.TrimSpace(b.String())
		return nil
	}
	if err := run("subject", &m.Subject); err != nil {
		return m, err
	}
	if err := run("text_all", &m.Text); err != nil {
		return m, err
	}
	if err := run("text", &m.Short); err != nil {
		return m, err
	}
	var b bytes.Buffer
	if err := p.html.ExecuteTemplate(&b, "html", d); err != nil {
		return m, err
	}
	m.HTML, m.Title, m.Data = b.String(), m.Subject, d
	m.Subject = strings.Join(strings.Fields(m.Subject), " ")
	m.Title = m.Subject
	return m, nil
}
