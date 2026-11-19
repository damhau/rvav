// Package brand checks the portal's branding (ADR-0059): an accent colour
// that keeps text readable in both themes, and logos that are pictures and
// nothing a browser runs.
package brand

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// MinContrast is WCAG 2.2's contrast for text (AA), which axe checks.
const MinContrast = 4.5

// Default is rvav's own accent, as web/src/styles.css has it.
const Default = "#0e6488"

// Palette is the accent's colours in one theme: text and controls in the
// accent, the text on an accent background, and a soft background tinted
// with it (a selected row).
type Palette struct {
	Accent, AccentFg, AccentSoft string
}

// theme is what the accent sits on in one theme, as web/src/styles.css has
// it: text in the accent on each background, and fg text on the soft one.
type theme struct {
	backgrounds        []string
	surface, fg, muted string
	// soft is the accent's share in its tinted background, most first.
	soft []float64
	// accentFg is the text on the accent, or fallback when it can't be read.
	accentFg          func(accent rgb) rgb
	fallback, towards rgb
}

var light = theme{backgrounds: []string{"#ffffff", "#f3f5f6", "#e9edef", "#eaeef0"},
	surface: "#ffffff", fg: "#14191c", muted: "#47525b", soft: []float64{0.14, 0.12, 0.1, 0.08, 0.06},
	accentFg: func(rgb) rgb { return white }, fallback: white, towards: black}

var dark = theme{backgrounds: []string{"#161b1e", "#0f1315", "#0b0e10", "#12171a"},
	surface: "#161b1e", fg: "#e4e9ec", muted: "#a3aeb6", soft: []float64{0.24, 0.2, 0.16, 0.12, 0.08},
	accentFg: func(a rgb) rgb { return mix(a, black, 0.85) }, fallback: black, towards: white}

type rgb struct{ r, g, b float64 }

var (
	white   = rgb{255, 255, 255}
	black   = rgb{0, 0, 0}
	hexRGB  = regexp.MustCompile(`^#([0-9a-fA-F]{6}|[0-9a-fA-F]{3})$`)
	hexText = func(c rgb) string {
		return fmt.Sprintf("#%02x%02x%02x", int(math.Round(c.r)), int(math.Round(c.g)), int(math.Round(c.b)))
	}
)

// Normalize gives a colour written #rgb or #rrggbb as #rrggbb in lower
// case; false when it is neither.
func Normalize(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if !hexRGB.MatchString(s) {
		return "", false
	}
	s = strings.ToLower(s[1:])
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	return "#" + s, true
}

func parse(s string) rgb {
	n, _ := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	return rgb{float64(n >> 16 & 0xff), float64(n >> 8 & 0xff), float64(n & 0xff)}
}

// round makes a colour what its hex says, so ratios are those a browser sees.
func round(c rgb) rgb { return parse(hexText(c)) }

func mix(a, b rgb, share float64) rgb {
	return round(rgb{a.r*(1-share) + b.r*share, a.g*(1-share) + b.g*share, a.b*(1-share) + b.b*share})
}

func luminance(c rgb) float64 {
	lin := func(v float64) float64 {
		v /= 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b)
}

func contrast(a, b rgb) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// Contrast is the WCAG contrast ratio of two #rrggbb colours.
func Contrast(a, b string) float64 { return contrast(parse(a), parse(b)) }

// palette is the accent's colours in a theme, or the lowest ratio and what
// it was against when text in them can't be read.
func (t theme) palette(accent rgb) (Palette, float64, string) {
	worst, against := math.Inf(1), ""
	for _, bg := range t.backgrounds {
		if r := contrast(accent, parse(bg)); r < worst {
			worst, against = r, bg
		}
	}
	fg := t.accentFg(accent)
	if contrast(fg, accent) < MinContrast {
		fg = t.fallback
	}
	if r := contrast(fg, accent); r < worst {
		worst, against = r, hexText(fg)
	}
	if worst < MinContrast {
		return Palette{}, worst, against
	}
	for _, share := range t.soft {
		soft := mix(parse(t.surface), accent, share)
		if contrast(accent, soft) >= MinContrast && contrast(parse(t.fg), soft) >= MinContrast &&
			contrast(parse(t.muted), soft) >= MinContrast {
			return Palette{Accent: hexText(accent), AccentFg: hexText(fg), AccentSoft: hexText(soft)}, worst, ""
		}
	}
	return Palette{}, MinContrast - 0.01, "its tinted background"
}

// nearest is the colour closest to accent, moved towards black or white,
// whose text reads in the theme.
func (t theme) nearest(accent rgb) (Palette, bool) {
	for step := 1; step <= 50; step++ {
		if p, _, against := t.palette(mix(accent, t.towards, float64(step)*0.02)); against == "" {
			return p, true
		}
	}
	return Palette{}, false
}

// ContrastError says why an accent can't be used in the light theme, and
// the closest darker colour that can.
type ContrastError struct {
	Color, Against, Suggestion string
	Ratio                      float64
}

func (e *ContrastError) Error() string {
	msg := fmt.Sprintf("%s has a contrast of %.2f:1 against %s in the light theme: text needs %.1f:1.", e.Color,
		math.Floor(e.Ratio*100)/100, e.Against, MinContrast)
	if e.Suggestion != "" {
		msg += " " + e.Suggestion + " is the closest darker colour that passes."
	}
	return msg
}

// Accents are an accent's palettes in the light and dark themes. The
// colour itself is used in the light theme, and refused when text in it
// can't be read there; the dark theme takes it lightened as much as its
// text needs.
func Accents(color string) (Palette, Palette, error) {
	hex, ok := Normalize(color)
	if !ok {
		return Palette{}, Palette{}, fmt.Errorf("%q is not a colour written #rrggbb", color)
	}
	c := parse(hex)
	l, ratio, against := light.palette(c)
	if against != "" {
		e := &ContrastError{Color: hex, Against: against, Ratio: ratio}
		if p, ok := light.nearest(c); ok {
			e.Suggestion = p.Accent
		}
		return Palette{}, Palette{}, e
	}
	d, _, against := dark.palette(c)
	if against != "" {
		var ok bool
		if d, ok = dark.nearest(c); !ok {
			return Palette{}, Palette{}, fmt.Errorf("%s has no lighter variant that reads in the dark theme", hex)
		}
	}
	return l, d, nil
}

// Rvav are rvav's own palettes, as the web app's stylesheet has them.
func Rvav() (Palette, Palette) {
	return Palette{Accent: Default, AccentFg: "#ffffff", AccentSoft: "#dcedf4"},
		Palette{Accent: "#4fb0d8", AccentFg: "#06222e", AccentSoft: "#123344"}
}

// Worst is the lowest contrast of a light theme's palette: its accent on
// the backgrounds, and text on it.
func Worst(p Palette) float64 {
	_, worst, _ := light.palette(parse(p.Accent))
	return worst
}
