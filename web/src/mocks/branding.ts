// The portal's branding in the mock backend (ADR-0059): its name, an accent
// whose contrast is checked as internal/brand does, and logos checked to be
// pictures and nothing a browser runs, against the server's allow-list.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { rvavBranding } from "@/lib/branding";
import { audited } from "./governance";
import type { Db } from "./seed";
import { unknownField } from "./strict";

type S = components["schemas"];
type Palette = S["AccentPalette"];
type Variant = S["LogoVariant"];

interface Logo {
  content: Uint8Array;
  type: string;
  sha: string;
}
interface Stored {
  name: string;
  accent: string;
  logos: Partial<Record<Variant, Logo>>;
}

export function brandingState(db: Db): Stored {
  const d = db as Db & { branding?: Stored };
  d.branding ??= { name: "", accent: "", logos: {} };
  return d.branding;
}

type Rgb = [number, number, number];
const MIN = 4.5;
const white: Rgb = [255, 255, 255];
const black: Rgb = [0, 0, 0];

const parse = (hex: string): Rgb => {
  const n = parseInt(hex.slice(1), 16);
  return [(n >> 16) & 0xff, (n >> 8) & 0xff, n & 0xff];
};
const hex = (c: Rgb) => "#" + c.map((v) => Math.round(v).toString(16).padStart(2, "0")).join("");
const mix = (a: Rgb, b: Rgb, share: number) => parse(hex(a.map((v, i) => v * (1 - share) + b[i]! * share) as Rgb));
const luminance = (c: Rgb) => {
  const lin = (v: number) => (v / 255 <= 0.04045 ? v / 255 / 12.92 : ((v / 255 + 0.055) / 1.055) ** 2.4);
  return 0.2126 * lin(c[0]) + 0.7152 * lin(c[1]) + 0.0722 * lin(c[2]);
};
const contrast = (a: Rgb, b: Rgb) => {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
};

interface Theme {
  backgrounds: string[];
  surface: string;
  fg: string;
  muted: string;
  soft: number[];
  accentFg: (a: Rgb) => Rgb;
  fallback: Rgb;
  towards: Rgb;
}
const light: Theme = {
  backgrounds: ["#ffffff", "#f3f5f6", "#e9edef", "#eaeef0"],
  surface: "#ffffff",
  fg: "#14191c",
  muted: "#47525b",
  soft: [0.14, 0.12, 0.1, 0.08, 0.06],
  accentFg: () => white,
  fallback: white,
  towards: black,
};
const dark: Theme = {
  backgrounds: ["#161b1e", "#0f1315", "#0b0e10", "#12171a"],
  surface: "#161b1e",
  fg: "#e4e9ec",
  muted: "#a3aeb6",
  soft: [0.24, 0.2, 0.16, 0.12, 0.08],
  accentFg: (a) => mix(a, black, 0.85),
  fallback: black,
  towards: white,
};

function palette(t: Theme, accent: Rgb): { p?: Palette; worst: number; against: string } {
  let worst = Infinity;
  let against = "";
  for (const bg of t.backgrounds) {
    const r = contrast(accent, parse(bg));
    if (r < worst) [worst, against] = [r, bg];
  }
  let fg = t.accentFg(accent);
  if (contrast(fg, accent) < MIN) fg = t.fallback;
  if (contrast(fg, accent) < worst) [worst, against] = [contrast(fg, accent), hex(fg)];
  if (worst < MIN) return { worst, against };
  for (const share of t.soft) {
    const soft = mix(parse(t.surface), accent, share);
    if (contrast(accent, soft) >= MIN && contrast(parse(t.fg), soft) >= MIN && contrast(parse(t.muted), soft) >= MIN)
      return { p: { accent: hex(accent), accent_fg: hex(fg), accent_soft: hex(soft) }, worst, against: "" };
  }
  return { worst: MIN - 0.01, against: "its tinted background" };
}

function nearest(t: Theme, accent: Rgb): Palette | undefined {
  for (let step = 1; step <= 50; step++) {
    const { p } = palette(t, mix(accent, t.towards, step * 0.02));
    if (p) return p;
  }
  return undefined;
}

const floor2 = (r: number) => Math.floor(r * 100) / 100;

export function normalize(color: string): string | undefined {
  const s = color.trim();
  if (!/^#([0-9a-fA-F]{6}|[0-9a-fA-F]{3})$/.test(s)) return undefined;
  const h = s.slice(1).toLowerCase();
  return "#" + (h.length === 3 ? [...h].map((c) => c + c).join("") : h);
}

/** As the server's accentCheck: an accent's palettes in both themes, or why text in it doesn't read. */
export function accentCheck(color: string): S["AccentCheck"] {
  const h = normalize(color);
  if (!h) return { ok: false, ratio: 0, message: `"${color.trim()}" is not a colour written #rrggbb` };
  const c = parse(h);
  const l = palette(light, c);
  if (!l.p) {
    const suggestion = nearest(light, c)?.accent;
    let message = `${h} has a contrast of ${floor2(l.worst).toFixed(2)}:1 against ${l.against} in the light theme: text needs ${MIN.toFixed(1)}:1.`;
    if (suggestion) message += ` ${suggestion} is the closest darker colour that passes.`;
    return { ok: false, ratio: floor2(l.worst), message, ...(suggestion ? { suggestion } : {}) };
  }
  const d = palette(dark, c).p ?? nearest(dark, c);
  if (!d) return { ok: false, ratio: 0, message: `${h} has no lighter variant that reads in the dark theme` };
  return { ok: true, ratio: floor2(l.worst), light: l.p, dark: d };
}

async function sha(content: Uint8Array): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", content as BufferSource);
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export function brandingOf(db: Db): S["Branding"] {
  const st = brandingState(db);
  const out: S["Branding"] = { ...rvavBranding, name: st.name || rvavBranding.name };
  if (st.accent) {
    const c = accentCheck(st.accent);
    Object.assign(out, { accent: st.accent, light: c.light, dark: c.dark });
  }
  for (const v of ["light", "dark"] as const) {
    const logo = st.logos[v];
    if (logo) out[`logo_${v}_url`] = `/api/v1/branding/logos/${v}?v=${logo.sha.slice(0, 16)}`;
  }
  return out;
}

function problem(status: number, title: string, errors?: S["FieldError"][], detail?: string) {
  return HttpResponse.json(
    {
      type: "https://rvav.dev/problems/x",
      title,
      status,
      request_id: "req_mock",
      ...(errors ? { errors } : {}),
      ...(detail ? { detail } : {}),
    },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

const MAX_LOGO = 256 << 10;
const MAX_SIDE = 2048;

const startsWith = (b: Uint8Array, sig: number[]) => sig.every((x, i) => b[i] === x);
const u16 = (b: Uint8Array, i: number) => (b[i]! << 8) | b[i + 1]!;

/** A PNG's size as Go's png.DecodeConfig reads it, its checksum aside, or why it can't be read. */
function pngSize(b: Uint8Array): [number, number] | string {
  if (b.length < 24) return "unexpected EOF";
  if (String.fromCharCode(...b.subarray(12, 16)) !== "IHDR") return "png: invalid format: chunk out of order";
  const view = new DataView(b.buffer, b.byteOffset, b.byteLength);
  const [w, h] = [view.getUint32(16), view.getUint32(20)];
  if (w === 0 || h === 0 || w > 0x7fffffff || h > 0x7fffffff) return "png: invalid format: non-positive dimension";
  return [w, h];
}

/** A JPEG's size as Go's jpeg.DecodeConfig reads it, from its frame header, or why it can't be read. */
function jpegSize(b: Uint8Array): [number, number] | string {
  let i = 2;
  for (;;) {
    if (i + 4 > b.length) return "unexpected EOF";
    if (b[i] !== 0xff) return "invalid JPEG format: missing 0xff marker start";
    const marker = b[i + 1]!;
    if (marker === 0xff) {
      i++;
      continue;
    }
    if (marker === 0x01 || (marker >= 0xd0 && marker <= 0xd8)) {
      i += 2;
      continue;
    }
    if (marker >= 0xc0 && marker <= 0xc2) {
      if (i + 9 > b.length) return "unexpected EOF";
      return [u16(b, i + 7), u16(b, i + 5)];
    }
    if ([0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf].includes(marker))
      return "unsupported JPEG feature: SOF type";
    if (marker === 0xda || marker === 0xd9) return "invalid JPEG format: missing SOF marker";
    i += 2 + u16(b, i + 2);
  }
}

const nsSVG = "http://www.w3.org/2000/svg";
const nsXLink = "http://www.w3.org/1999/xlink";
const nsXML = "http://www.w3.org/XML/1998/namespace";
const nsXMLNS = "http://www.w3.org/2000/xmlns/";
const metadataSpaces = new Set([
  "http://www.inkscape.org/namespaces/inkscape",
  "http://sodipodi.sourceforge.net/DTD/sodipodi-0.dtd",
  "http://www.w3.org/1999/02/22-rdf-syntax-ns#",
  "http://creativecommons.org/ns#",
  "http://purl.org/dc/elements/1.1/",
  "http://ns.adobe.com/AdobeIllustrator/10.0/",
  "http://ns.adobe.com/AdobeSVGViewerExtensions/3.0/",
]);
const svgElements = new Set(
  `svg g defs symbol use title desc metadata style switch
  path rect circle ellipse line polyline polygon text tspan textPath
  linearGradient radialGradient stop pattern clipPath mask marker image
  filter feBlend feColorMatrix feComponentTransfer feComposite feDiffuseLighting feDisplacementMap
  feDistantLight feDropShadow feFlood feFuncA feFuncB feFuncG feFuncR feGaussianBlur feMerge
  feMergeNode feMorphology feOffset fePointLight feSpecularLighting feSpotLight feTile feTurbulence`.split(/\s+/),
);
const rasterData = /^data:image\/(png|jpeg|gif|webp);base64,[A-Za-z0-9+/=\s]*$/;
// image-set() and src() fetch from a string, without url().
const cssFetch =
  /@import|expression\s*\(|behavior\s*:|-moz-binding|javascript:|url\s*\(\s*['"]?\s*[^#'"\s)]|image-set\s*\(|\bimage\s*\(|cross-fade\s*\(|\bsrc\s*\(|\belement\s*\(/i;
const scheme = /(java|vb)script:|data:text\/html/i;
const notAPicture = { status: 415, message: "A logo is a PNG, JPEG or SVG picture." };

const quoted = (v: string) => JSON.stringify([...v].length > 40 ? [...v].slice(0, 39).join("") + "…" : v);

/** Why an SVG element can't be in a logo, as internal/brand's allow-list says. */
function refusedElement(el: Element): string | undefined {
  const name = el.localName;
  const ns = el.namespaceURI;
  if (ns && metadataSpaces.has(ns)) return undefined;
  if (ns && ns !== nsSVG) return `has a <${name}> element from ${ns}`;
  if (!svgElements.has(name)) return `has a <${name}> element, which a logo can't have`;
  for (const a of el.attributes) {
    const local = a.localName.toLowerCase();
    const space = a.namespaceURI;
    if (space === nsXMLNS || (!space && local === "xmlns")) continue;
    if (space && metadataSpaces.has(space)) continue;
    if (space === nsXML && local !== "space" && local !== "lang") return `has an xml:${a.localName} attribute`;
    if (space && space !== nsSVG && space !== nsXML && space !== nsXLink) return `has an attribute from ${space}`;
    if (local.startsWith("on")) return `has an event handler (${a.localName})`;
    if (local === "href") {
      const v = a.value.trim();
      if (!v.startsWith("#") && !(name === "image" && rasterData.test(v)))
        return `links to ${quoted(v)}: a logo can only refer to its own parts, and embed PNG, JPEG, GIF or WebP pictures`;
      continue;
    }
    if (space === nsXLink) return `has an xlink:${a.localName} attribute`;
    if (scheme.test(a.value.replace(/\s+/g, ""))) return `has a script in its ${a.localName} attribute`;
    if (a.value.includes("\\"))
      return `has a CSS escape in its ${a.localName} attribute, which could spell what it fetches`;
    if (cssFetch.test(a.value)) return `has a ${a.localName} attribute that fetches or runs something`;
  }
  if (name !== "style") return undefined;
  const inside = el.firstElementChild;
  if (inside) return `has a <${inside.localName}> element inside its <style>`;
  // Its text as the browser reads it, joined across the comments that split it.
  const css = el.textContent ?? "";
  if (css.includes("\\")) return "has a CSS escape in its style, which could spell what it fetches";
  if (cssFetch.test(css)) return "has a style that fetches or runs something";
  return undefined;
}

/** Why an SVG drawing could run or fetch something when shown, in the order the server reads it. */
function refusedSVG(node: Node): string | undefined {
  for (const child of node.childNodes) {
    if (child.nodeType === Node.PROCESSING_INSTRUCTION_NODE) {
      const target = (child as ProcessingInstruction).target;
      if (target !== "xml") return `has a <?${target}?> instruction`;
    } else if (child.nodeType === Node.ELEMENT_NODE) {
      const why = refusedElement(child as Element) ?? refusedSVG(child);
      if (why) return why;
    }
  }
  return undefined;
}

/** The logo's type and why it can't be one, as internal/brand checks it. */
function checkLogo(b: Uint8Array): { type?: string; status?: number; message?: string } {
  if (b.length === 0) return { status: 422, message: "The file is empty." };
  const raster = startsWith(b, [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
    ? { type: "image/png", size: pngSize(b) }
    : startsWith(b, [0xff, 0xd8, 0xff])
      ? { type: "image/jpeg", size: jpegSize(b) }
      : undefined;
  if (raster) {
    if (typeof raster.size === "string") return { status: 422, message: `The picture can't be read: ${raster.size}.` };
    const [w, h] = raster.size;
    if (w > MAX_SIDE || h > MAX_SIDE)
      return {
        status: 422,
        message: `The picture is ${w}×${h} pixels: a logo is ${MAX_SIDE} pixels wide and tall at most.`,
      };
    return { type: raster.type };
  }
  const text = new TextDecoder()
    .decode(b)
    .replace(/^\uFEFF/, "")
    .trimStart();
  const start = text.slice(0, 14).toLowerCase();
  if (!text.startsWith("<") || start.startsWith("<!doctype html") || start.startsWith("<html")) return notAPicture;
  let doc: Document;
  try {
    doc = new DOMParser().parseFromString(text, "image/svg+xml");
  } catch {
    return notAPicture;
  }
  if (doc.getElementsByTagName("parsererror").length > 0) {
    const read = /<svg[\s>]/.test(text);
    return read
      ? { status: 422, message: "The file isn't an SVG drawing that can be read." }
      : { status: 415, message: notAPicture.message };
  }
  if (doc.doctype) return { status: 422, message: "The SVG has a DOCTYPE or an entity: export it again as plain SVG." };
  const root = doc.documentElement;
  if (root.localName !== "svg" || (root.namespaceURI !== null && root.namespaceURI !== nsSVG)) return notAPicture;
  const why = refusedSVG(doc);
  if (why) return { status: 422, message: `The SVG ${why}.` };
  return { type: "image/svg+xml" };
}

const brandingFields = ["name", "accent"] as const;
const isVariant = (v: unknown): v is Variant => v === "light" || v === "dark";
const notAVariant = () =>
  problem(400, "The request is not valid", undefined, "A logo is for the light or the dark theme.");

export function brandingHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const manage = () => can(db.me, "system.manage");
  return [
    http.get(`${base}/branding`, async () => {
      await wait();
      return HttpResponse.json(brandingOf(db), { headers: { "Cache-Control": "no-cache" } });
    }),
    http.get(`${base}/branding/logos/:variant`, ({ params }) => {
      const logo = isVariant(params.variant) ? brandingState(db).logos[params.variant] : undefined;
      if (!logo) return problem(404, "Not found");
      return new HttpResponse(logo.content as BodyInit, {
        headers: { "Content-Type": logo.type, "Cache-Control": "public, max-age=31536000, immutable" },
      });
    }),
    http.put(`${base}/admin/branding`, async ({ request }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const body: unknown = await request.json();
      const strict = unknownField(body, brandingFields);
      if (strict) return strict;
      const w = body as S["BrandingWrite"];
      // Go's strings.Fields: Unicode's spaces, tabs and line breaks among them, but not U+FEFF as \s has it.
      const name = (w.name ?? "").split(/[ \u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\t\n\v\f\r\u0085]+/).filter(Boolean).join(" "); // prettier-ignore
      let accent = (w.accent ?? "").trim();
      const errors: S["FieldError"][] = [];
      if ([...name].length > 40)
        errors.push({
          field: "/name",
          code: "too_long",
          message: "A portal's name is 40 characters at most: the menu shows it.",
        });
      else if (/\p{Cc}/u.test(name))
        errors.push({ field: "/name", code: "invalid", message: "A portal's name has no control characters." });
      if (accent) {
        const c = accentCheck(accent);
        if (!normalize(accent))
          errors.push({ field: "/accent", code: "invalid", message: "A colour written #rrggbb or #rgb, such as #5b2a86." }); // prettier-ignore
        else if (!c.ok) errors.push({ field: "/accent", code: "contrast", message: c.message ?? "" });
        else accent = normalize(accent)!;
      }
      if (errors.length) return problem(422, "Validation failed", errors);
      const st = brandingState(db);
      audited(db, "settings.branding.update", "branding", "branding", {
        name,
        accent,
        was: { name: st.name, accent: st.accent },
      });
      Object.assign(st, { name, accent });
      return HttpResponse.json(brandingOf(db));
    }),
    http.post(`${base}/admin/branding/accent-check`, async ({ request }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const body: unknown = await request.json();
      const strict = unknownField(body, ["accent"]);
      if (strict) return strict;
      return HttpResponse.json(accentCheck((body as S["AccentCheckQuery"]).accent ?? ""));
    }),
    http.put(`${base}/admin/branding/logos/:variant`, async ({ request, params }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const variant = params.variant;
      if (!isVariant(variant)) return notAVariant();
      const content = new Uint8Array(await request.arrayBuffer());
      if (content.length > MAX_LOGO)
        return problem(413, "Payload too large", undefined, "A logo is 256 KB at most: emails carry it.");
      const check = checkLogo(content);
      if (check.status === 415) return problem(415, "Unsupported media type", undefined, check.message);
      if (check.status)
        return problem(422, "Validation failed", [{ field: "", code: "invalid", message: check.message ?? "" }]);
      const logo: Logo = { content, type: check.type!, sha: await sha(content) };
      brandingState(db).logos[variant] = logo;
      audited(db, "settings.branding_logo.set", "branding", `logo_${variant}`, {
        variant,
        content_type: logo.type,
        size: content.length,
        sha256: logo.sha,
      });
      return HttpResponse.json(brandingOf(db));
    }),
    http.delete(`${base}/admin/branding/logos/:variant`, async ({ params }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const variant = params.variant;
      if (!isVariant(variant)) return notAVariant();
      delete brandingState(db).logos[variant];
      audited(db, "settings.branding_logo.remove", "branding", `logo_${variant}`, { variant });
      return HttpResponse.json(brandingOf(db));
    }),
  ];
}
