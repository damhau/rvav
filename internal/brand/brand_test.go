package brand

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// Every colour of a palette reads where the web app puts it.
func checkPalette(t *testing.T, th theme, p Palette) {
	t.Helper()
	for _, bg := range append([]string{p.AccentSoft}, th.backgrounds...) {
		if r := Contrast(p.Accent, bg); r < MinContrast {
			t.Errorf("%s on %s: %.2f", p.Accent, bg, r)
		}
	}
	for _, pair := range [][2]string{{p.AccentFg, p.Accent}, {th.fg, p.AccentSoft}, {th.muted, p.AccentSoft}} {
		if r := Contrast(pair[0], pair[1]); r < MinContrast {
			t.Errorf("%s on %s: %.2f", pair[0], pair[1], r)
		}
	}
}

func TestAccents(t *testing.T) {
	// rvav's own accent is kept in the light theme; the dark one is lighter.
	l, d, err := Accents("#0E6488")
	if err != nil || l.Accent != Default || l.AccentFg != "#ffffff" {
		t.Fatalf("rvav's accent: %+v %v", l, err)
	}
	checkPalette(t, light, l)
	checkPalette(t, dark, d)
	if Contrast(d.Accent, "#ffffff") >= Contrast(l.Accent, "#ffffff") {
		t.Fatalf("the dark accent %s isn't lighter than %s", d.Accent, l.Accent)
	}

	// A navy reads on white, and is lightened for the dark theme.
	l, d, err = Accents("#123")
	if err != nil || l.Accent != "#112233" || d.Accent == "#112233" {
		t.Fatalf("navy: %+v %+v %v", l, d, err)
	}
	checkPalette(t, light, l)
	checkPalette(t, dark, d)

	// A brand yellow can't be text on white: refused, with the closest
	// darker colour that can.
	_, _, err = Accents("#ffcc00")
	var ce *ContrastError
	if !errors.As(err, &ce) || ce.Suggestion == "" || ce.Ratio >= MinContrast ||
		!strings.Contains(err.Error(), "#ffcc00 has a contrast of 1.") || !strings.Contains(err.Error(), ce.Suggestion+" is the closest") {
		t.Fatalf("yellow: %v", err)
	}
	l, d, err = Accents(ce.Suggestion)
	if err != nil {
		t.Fatalf("the suggestion %s: %v", ce.Suggestion, err)
	}
	checkPalette(t, light, l)
	checkPalette(t, dark, d)

	for _, bad := range []string{"", "0e6488", "#0e648", "red", "#gg0000"} {
		if _, _, err := Accents(bad); err == nil {
			t.Errorf("%q was taken", bad)
		}
	}
}

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{14, 100, 136, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCheckLogo(t *testing.T) {
	if l, err := CheckLogo(pngOf(t, 120, 32)); err != nil || l.ContentType != "image/png" || l.Width != 120 || l.Height != 32 {
		t.Fatalf("a PNG: %+v %v", l, err)
	}
	if _, err := CheckLogo(pngOf(t, 4000, 10)); err == nil || !strings.Contains(err.Error(), "4000×10 pixels") {
		t.Fatalf("a huge PNG: %v", err)
	}
	if _, err := CheckLogo(append(pngOf(t, 10, 10), make([]byte, MaxLogoSize)...)); err == nil || !strings.Contains(err.Error(), "256 KB") {
		t.Fatalf("a large file: %v", err)
	}
	const inkscape = `<?xml version="1.0" encoding="UTF-8"?>
<!-- Created with Inkscape -->
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"
  xmlns:inkscape="http://www.inkscape.org/namespaces/inkscape" xmlns:sodipodi="http://sodipodi.sourceforge.net/DTD/sodipodi-0.dtd"
  viewBox="0 0 120 32" inkscape:version="1.3" xml:space="preserve">
  <sodipodi:namedview id="nv" pagecolor="#ffffff"/>
  <defs><linearGradient id="g"><stop offset="0" stop-color="#0e6488"/></linearGradient>
  <style>.a{fill:url(#g)}</style></defs>
  <title>Acme</title>
  <g class="a"><rect width="32" height="32" rx="4" fill="url(#g)"/><use xlink:href="#r"/>
  <image href="data:image/png;base64,iVBORw0KGgo=" width="1" height="1"/>
  <text x="40" y="22" font-family="sans-serif">Acme</text></g>
</svg>`
	if l, err := CheckLogo([]byte(inkscape)); err != nil || l.ContentType != "image/svg+xml" {
		t.Fatalf("an Inkscape SVG: %+v %v", l, err)
	}
	const ns = `<svg xmlns="http://www.w3.org/2000/svg">`
	refused := map[string]string{
		"script":         `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"event handler":  `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`,
		"handler, case":  `<svg xmlns="http://www.w3.org/2000/svg"><rect ONclick="x"/></svg>`,
		"foreign":        `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><div xmlns="http://www.w3.org/1999/xhtml">x</div></foreignObject></svg>`,
		"xhtml":          `<svg xmlns="http://www.w3.org/2000/svg" xmlns:h="http://www.w3.org/1999/xhtml"><h:iframe src="https://evil.example"/></svg>`,
		"link":           `<svg xmlns="http://www.w3.org/2000/svg"><a href="https://evil.example"><rect/></a></svg>`,
		"external use":   `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><use xlink:href="https://evil.example/x.svg#a"/></svg>`,
		"external image": `<svg xmlns="http://www.w3.org/2000/svg"><image href="https://evil.example/track.png"/></svg>`,
		"svg in image":   `<svg xmlns="http://www.w3.org/2000/svg"><image href="data:image/svg+xml;base64,PHN2Zz4="/></svg>`,
		"javascript":     `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="java&#x09;script:alert(1)"/></svg>`,
		"animation":      `<svg xmlns="http://www.w3.org/2000/svg"><set attributeName="href" to="javascript:alert(1)"/></svg>`,
		"doctype":        `<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY x "y">]><svg xmlns="http://www.w3.org/2000/svg">&x;</svg>`,
		"import":         `<svg xmlns="http://www.w3.org/2000/svg"><style>@import url(https://evil.example/a.css);</style></svg>`,
		"url in style":   `<svg xmlns="http://www.w3.org/2000/svg"><rect style="fill:url( 'https://evil.example/p' )"/></svg>`,
		"url in fill":    `<svg xmlns="http://www.w3.org/2000/svg"><rect fill="url(https://evil.example/p#a)"/></svg>`,
		"stylesheet":     `<?xml-stylesheet href="https://evil.example/a.css"?><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"xml:base":       `<svg xmlns="http://www.w3.org/2000/svg" xml:base="https://evil.example/"/>`,
		"two roots":      `<svg xmlns="http://www.w3.org/2000/svg"/><svg xmlns="http://www.w3.org/2000/svg"/>`,
		// Ways around the style check that milestone O1's review found.
		"comment splits @import": ns + `<style>@imp<!---->ort "https://evil.example/a.css";</style></svg>`,
		"comment splits url(":    ns + `<style>svg{background:u<!---->rl(https://evil.example/p.png)}</style></svg>`,
		"element inside style":   ns + `<style><g/>@import "https://evil.example/a.css";</style></svg>`,
		"escape in url":          ns + `<style>svg{background:u\72l(https://evil.example/p.png)}</style></svg>`,
		"escape in @import":      ns + `<style>@\69mport "https://evil.example/a.css";</style></svg>`,
		"image-set":              ns + `<style>svg{background-image:image-set("https://evil.example/p.png" 1x)}</style></svg>`,
		"escape in attribute":    ns + `<rect style="fill:u\72l(https://evil.example/p#a)"/></svg>`,
		"src()":                  ns + `<style>@font-face{font-family:x;src:src("https://evil.example/f.woff")}</style></svg>`,
	}
	for name, svg := range refused {
		var le *LogoError
		if _, err := CheckLogo([]byte(svg)); !errors.As(err, &le) || le.Type || !strings.HasPrefix(le.Msg, "The SVG ") &&
			!strings.HasPrefix(le.Msg, "The file isn't") {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, other := range map[string]string{"html": `<html><body onload="x"></body></html>`, "text": "just text",
		"gif": "GIF89a\x01\x00\x01\x00\x00\x00\x00;"} {
		var le *LogoError
		if _, err := CheckLogo([]byte(other)); !errors.As(err, &le) || !le.Type {
			t.Errorf("%s: %v", name, err)
		}
	}
}
