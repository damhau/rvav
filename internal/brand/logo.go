package brand

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // logos may be JPEG
	_ "image/png"  // or PNG
	"io"
	"net/http"
	"regexp"
	"strings"
)

const (
	// MaxLogoSize is the largest logo kept: it is sent with every email.
	MaxLogoSize = 256 << 10
	// MaxLogoSide is the widest and tallest a raster logo may be.
	MaxLogoSide = 2048
)

// Logo is a checked logo.
type Logo struct {
	ContentType   string
	Width, Height int
}

// LogoError says why a logo isn't taken.
type LogoError struct {
	Msg string
	// Type: the content isn't a PNG, JPEG or SVG picture at all.
	Type bool
}

func (e *LogoError) Error() string { return e.Msg }

// CheckLogo checks a logo: a PNG or JPEG picture of a sensible size, or an
// SVG drawing with nothing in it a browser runs or fetches (refused, not
// cleaned: what is kept is what was sent).
func CheckLogo(content []byte) (Logo, error) {
	switch {
	case len(content) == 0:
		return Logo{}, &LogoError{Msg: "The file is empty."}
	case len(content) > MaxLogoSize:
		return Logo{}, &LogoError{Msg: fmt.Sprintf("A logo is %d KB at most.", MaxLogoSize>>10)}
	}
	switch ct := http.DetectContentType(content); ct {
	case "image/png", "image/jpeg":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(content))
		if err != nil {
			return Logo{}, &LogoError{Msg: "The picture can't be read: " + err.Error() + "."}
		}
		if cfg.Width > MaxLogoSide || cfg.Height > MaxLogoSide {
			return Logo{}, &LogoError{Msg: fmt.Sprintf("The picture is %d×%d pixels: a logo is %d pixels wide and tall at most.",
				cfg.Width, cfg.Height, MaxLogoSide)}
		}
		return Logo{ContentType: ct, Width: cfg.Width, Height: cfg.Height}, nil
	}
	if !looksLikeXML(content) || looksLikeHTML(content) {
		return Logo{}, &LogoError{Msg: "A logo is a PNG, JPEG or SVG picture.", Type: true}
	}
	if err := CheckSVG(content); err != nil {
		return Logo{}, err
	}
	return Logo{ContentType: "image/svg+xml"}, nil
}

func trimmed(b []byte) []byte {
	return bytes.TrimLeft(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), " \t\r\n")
}

func looksLikeXML(b []byte) bool { return bytes.HasPrefix(trimmed(b), []byte("<")) }

func looksLikeHTML(b []byte) bool {
	b = trimmed(b)
	start := bytes.ToLower(b[:min(len(b), 14)])
	return bytes.HasPrefix(start, []byte("<!doctype html")) || bytes.HasPrefix(start, []byte("<html"))
}

const (
	nsSVG   = "http://www.w3.org/2000/svg"
	nsXLink = "http://www.w3.org/1999/xlink"
	nsXML   = "http://www.w3.org/XML/1998/namespace"
)

// Drawing tools write their own metadata in these: browsers ignore it.
var metadataSpaces = map[string]bool{
	"http://www.inkscape.org/namespaces/inkscape":        true,
	"http://sodipodi.sourceforge.net/DTD/sodipodi-0.dtd": true,
	"http://www.w3.org/1999/02/22-rdf-syntax-ns#":        true,
	"http://creativecommons.org/ns#":                     true,
	"http://purl.org/dc/elements/1.1/":                   true,
	"http://ns.adobe.com/AdobeIllustrator/10.0/":         true,
	"http://ns.adobe.com/AdobeSVGViewerExtensions/3.0/":  true,
}

// The elements a logo may have: shapes, text, paint and filters, without
// scripts, links, animations, foreign content or external pictures.
var svgElements = map[string]bool{}

func init() {
	for _, e := range strings.Fields(`svg g defs symbol use title desc metadata style switch
		path rect circle ellipse line polyline polygon text tspan textPath
		linearGradient radialGradient stop pattern clipPath mask marker image
		filter feBlend feColorMatrix feComponentTransfer feComposite feDiffuseLighting feDisplacementMap
		feDistantLight feDropShadow feFlood feFuncA feFuncB feFuncG feFuncR feGaussianBlur feMerge
		feMergeNode feMorphology feOffset fePointLight feSpecularLighting feSpotLight feTile feTurbulence`) {
		svgElements[e] = true
	}
}

var (
	rasterData = regexp.MustCompile(`^data:image/(png|jpeg|gif|webp);base64,[A-Za-z0-9+/=\s]*$`)
	// CSS that fetches or runs something; url(#id) names a paint in the
	// drawing itself. image-set() and src() fetch from a string, without
	// url().
	cssFetch = regexp.MustCompile(`(?i)@import|expression\s*\(|behavior\s*:|-moz-binding|javascript:|url\s*\(\s*['"]?\s*[^#'"\s)]|` +
		`image-set\s*\(|\bimage\s*\(|cross-fade\s*\(|\bsrc\s*\(|\belement\s*\(`)
	scheme = regexp.MustCompile(`(?i)(java|vb)script:|data:text/html`)
)

// CheckSVG refuses an SVG drawing that could run or fetch anything when
// shown: scripts, event handlers, links, foreign content, external
// references and DOCTYPEs are refused.
func CheckSVG(content []byte) error {
	refuse := func(format string, a ...any) error {
		return &LogoError{Msg: "The SVG " + fmt.Sprintf(format, a...) + "."}
	}
	d := xml.NewDecoder(bytes.NewReader(content))
	d.Strict = true
	depth, root, inStyle := 0, false, false
	// A style's CSS as the browser reads it: its text joined across the
	// comments that split it, so @imp<!---->ort is @import.
	var css []byte
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return &LogoError{Msg: "The file isn't an SVG drawing that can be read: " + err.Error() + ".", Type: !root}
		}
		switch t := tok.(type) {
		case xml.Directive:
			return refuse("has a DOCTYPE or an entity: export it again as plain SVG")
		case xml.ProcInst:
			if t.Target != "xml" {
				return refuse("has a <?%s?> instruction", t.Target)
			}
		case xml.StartElement:
			if inStyle {
				return refuse("has a <%s> element inside its <style>", t.Name.Local)
			}
			if depth == 0 {
				if root {
					return refuse("has more than one root element")
				}
				if t.Name.Local != "svg" || t.Name.Space != nsSVG && t.Name.Space != "" {
					return &LogoError{Msg: "A logo is a PNG, JPEG or SVG picture.", Type: true}
				}
				root = true
			}
			depth++
			if err := checkElement(t, refuse); err != nil {
				return err
			}
			inStyle, css = t.Name.Local == "style", css[:0]
		case xml.EndElement:
			depth--
			if inStyle {
				switch {
				case bytes.IndexByte(css, '\\') >= 0:
					return refuse("has a CSS escape in its style, which could spell what it fetches")
				case cssFetch.Match(css):
					return refuse("has a style that fetches or runs something")
				}
			}
			inStyle = false
		case xml.CharData:
			if inStyle {
				css = append(css, t...)
			}
		}
	}
	if !root {
		return &LogoError{Msg: "A logo is a PNG, JPEG or SVG picture.", Type: true}
	}
	return nil
}

func checkElement(t xml.StartElement, refuse func(string, ...any) error) error {
	name := t.Name.Local
	switch {
	case metadataSpaces[t.Name.Space]:
		return nil
	case t.Name.Space != nsSVG && t.Name.Space != "":
		return refuse("has a <%s> element from %s", name, t.Name.Space)
	case !svgElements[name]:
		return refuse("has a <%s> element, which a logo can't have", name)
	}
	for _, a := range t.Attr {
		local := strings.ToLower(a.Name.Local)
		switch {
		case a.Name.Space == "xmlns" || a.Name.Space == "" && local == "xmlns":
			continue
		case metadataSpaces[a.Name.Space]:
			continue
		case a.Name.Space == nsXML && local != "space" && local != "lang":
			return refuse("has an xml:%s attribute", a.Name.Local)
		case a.Name.Space != "" && a.Name.Space != nsSVG && a.Name.Space != nsXML && a.Name.Space != nsXLink:
			return refuse("has an attribute from %s", a.Name.Space)
		case strings.HasPrefix(local, "on"):
			return refuse("has an event handler (%s)", a.Name.Local)
		case local == "href":
			v := strings.TrimSpace(a.Value)
			if !strings.HasPrefix(v, "#") && !(name == "image" && rasterData.MatchString(v)) {
				return refuse("links to %q: a logo can only refer to its own parts, and embed PNG, JPEG, GIF or WebP pictures", truncate(v))
			}
		case a.Name.Space == nsXLink:
			return refuse("has an xlink:%s attribute", a.Name.Local)
		case scheme.MatchString(strings.Join(strings.Fields(a.Value), "")):
			return refuse("has a script in its %s attribute", a.Name.Local)
		case strings.ContainsRune(a.Value, '\\'):
			return refuse("has a CSS escape in its %s attribute, which could spell what it fetches", a.Name.Local)
		case cssFetch.MatchString(a.Value):
			return refuse("has a %s attribute that fetches or runs something", a.Name.Local)
		}
	}
	return nil
}

func truncate(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:39]) + "…"
	}
	return s
}
