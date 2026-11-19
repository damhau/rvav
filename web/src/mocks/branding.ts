// The portal's branding in the mock backend (ADR-0059): its name, an accent
// whose contrast is checked as internal/brand does, and logos checked to be
// pictures and nothing a browser runs.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { rvavBranding } from "@/lib/branding";
import { audited } from "./governance";
import type { Db } from "./seed";

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

/** The logo's type and why it can't be one, as internal/brand checks it. */
function checkLogo(b: Uint8Array): { type?: string; status?: number; message?: string } {
  if (b.length === 0) return { status: 422, message: "The file is empty." };
  if (startsWith(b, [0x89, 0x50, 0x4e, 0x47])) {
    const view = new DataView(b.buffer, b.byteOffset, b.byteLength);
    const [w, h] = b.length >= 24 ? [view.getUint32(16), view.getUint32(20)] : [0, 0];
    if (w > MAX_SIDE || h > MAX_SIDE)
      return {
        status: 422,
        message: `The picture is ${w}×${h} pixels: a logo is ${MAX_SIDE} pixels wide and tall at most.`,
      };
    return { type: "image/png" };
  }
  if (startsWith(b, [0xff, 0xd8, 0xff])) return { type: "image/jpeg" };
  const text = new TextDecoder()
    .decode(b)
    .replace(/^\uFEFF/, "")
    .trimStart();
  const start = text.slice(0, 14).toLowerCase();
  if (!text.startsWith("<") || start.startsWith("<!doctype html") || start.startsWith("<html"))
    return { status: 415, message: "A logo is a PNG, JPEG or SVG picture." };
  if (!/<svg[\s>]/.test(text)) return { status: 415, message: "A logo is a PNG, JPEG or SVG picture." };
  const refused: [RegExp, string][] = [
    [/<!doctype|<!entity/i, "has a DOCTYPE or an entity: export it again as plain SVG"],
    [/<script/i, "has a <script> element, which a logo can't have"],
    [/<foreignObject/i, "has a <foreignObject> element, which a logo can't have"],
    [/<a[\s>]/i, "has a <a> element, which a logo can't have"],
    [/\son[a-z]+\s*=/i, "has an event handler"],
    [/(?:href|src)\s*=\s*["'](?!#|data:image\/)/i, "refers to something outside itself"],
    [/url\(\s*["']?(?!#)/i, "has a style that fetches or runs something"],
  ];
  for (const [re, what] of refused) if (re.test(text)) return { status: 422, message: `The SVG ${what}.` };
  return { type: "image/svg+xml" };
}

export function brandingHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const manage = () => can(db.me, "system.manage");
  return [
    http.get(`${base}/branding`, async () => {
      await wait();
      return HttpResponse.json(brandingOf(db), { headers: { "Cache-Control": "no-cache" } });
    }),
    http.get(`${base}/branding/logos/:variant`, ({ params }) => {
      const logo = brandingState(db).logos[params.variant as Variant];
      if (!logo) return problem(404, "Not found");
      return new HttpResponse(logo.content as BodyInit, {
        headers: { "Content-Type": logo.type, "Cache-Control": "public, max-age=31536000, immutable" },
      });
    }),
    http.put(`${base}/admin/branding`, async ({ request }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const w = (await request.json()) as S["BrandingWrite"];
      const name = (w.name ?? "").split(/\s+/).filter(Boolean).join(" ");
      let accent = (w.accent ?? "").trim();
      const errors: S["FieldError"][] = [];
      if ([...name].length > 40)
        errors.push({
          field: "/name",
          code: "too_long",
          message: "A portal's name is 40 characters at most: the menu shows it.",
        });
      if (accent) {
        const c = accentCheck(accent);
        if (!c.ok) errors.push({ field: "/accent", code: "contrast", message: c.message ?? "" });
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
      const q = (await request.json()) as S["AccentCheckQuery"];
      return HttpResponse.json(accentCheck(q.accent ?? ""));
    }),
    http.put(`${base}/admin/branding/logos/:variant`, async ({ request, params }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const variant = params.variant as Variant;
      const content = new Uint8Array(await request.arrayBuffer());
      if (content.length > MAX_LOGO)
        return problem(413, "Payload too large", undefined, "A logo is 256 KB at most: emails carry it.");
      const check = checkLogo(content);
      if (check.status === 415) return problem(415, "Unsupported media type", undefined, check.message);
      if (check.status)
        return problem(422, "Validation failed", [{ field: "", code: "invalid", message: check.message ?? "" }]);
      const logo: Logo = { content, type: check.type!, sha: await sha(content) };
      brandingState(db).logos[variant] = logo;
      audited(db, "settings.branding_logo.set", "branding_logo", variant, {
        type: logo.type,
        bytes: content.length,
        sha256: logo.sha,
      });
      return HttpResponse.json(brandingOf(db));
    }),
    http.delete(`${base}/admin/branding/logos/:variant`, async ({ params }) => {
      await wait();
      if (!manage()) return problem(403, "Forbidden");
      const variant = params.variant as Variant;
      const st = brandingState(db);
      if (st.logos[variant]) {
        delete st.logos[variant];
        audited(db, "settings.branding_logo.remove", "branding_logo", variant);
      }
      return HttpResponse.json(brandingOf(db));
    }),
  ];
}
