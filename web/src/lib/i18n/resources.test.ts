// Nothing is left untranslated (ADR-0061): every text is in English and in
// French, under the same key, with the same variables and markup.
import { describe, expect, it } from "vitest";
import { locales, namespaces, type Locale } from ".";

const files = import.meta.glob<unknown>("../../locales/*/*.json", { eager: true, import: "default" });

function bundle(lng: Locale): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [path, value] of Object.entries(files)) {
    const m = /\/locales\/([^/]+)\/([^/]+)\.json$/.exec(path);
    if (m?.[1] === lng) out[m[2]!] = value;
  }
  return out;
}

/** Every text of a namespace by its dotted key, and what isn't a text. */
function flatten(value: unknown, prefix = "", out: Record<string, string> = {}, wrong: string[] = []) {
  if (typeof value === "string") out[prefix] = value;
  else if (value && typeof value === "object" && !Array.isArray(value)) {
    for (const [k, v] of Object.entries(value)) flatten(v, prefix ? `${prefix}.${k}` : k, out, wrong);
  } else wrong.push(prefix);
  return { texts: out, wrong };
}

const pluralSuffix = /_(zero|one|two|few|many|other)$/;
const categories = (lng: Locale) => new Intl.PluralRules(lng).resolvedOptions().pluralCategories;

/** A key's texts, its plural forms gathered under the key without the suffix. */
function keyed(texts: Record<string, string>) {
  const out = new Map<string, { forms: Set<string>; texts: string[] }>();
  for (const [k, v] of Object.entries(texts)) {
    const m = pluralSuffix.exec(k);
    const base = m ? k.slice(0, -m[0].length) : k;
    const e = out.get(base) ?? { forms: new Set(), texts: [] };
    if (m) e.forms.add(m[1]!);
    e.texts.push(v);
    out.set(base, e);
  }
  return out;
}

const variables = (s: string) =>
  [...s.matchAll(/\{\{-?\s*([\w.]+)\s*(?:,[^}]*)?\}\}/g)]
    .map((m) => m[1]!)
    .concat([...s.matchAll(/\$t\(([^),]+)/g)].map((m) => `$t(${m[1]!.trim()})`));
const tags = (s: string) => [...s.matchAll(/<\/?([\w-]+)\s*\/?>/g)].map((m) => m[1]!);
const set = (xs: string[]) => [...new Set(xs)].sort();

const en = bundle("en");
const fr = bundle("fr");

describe("the texts", () => {
  it("has a file per namespace in each language, and none other", () => {
    for (const lng of locales) {
      expect(Object.keys(bundle(lng)).sort(), lng).toEqual([...namespaces].sort());
    }
  });

  for (const ns of namespaces) {
    describe(ns, () => {
      const e = flatten(en[ns]);
      const f = flatten(fr[ns]);
      const ek = keyed(e.texts);
      const fk = keyed(f.texts);

      it("holds only texts", () => {
        expect(e.wrong, "en").toEqual([]);
        expect(f.wrong, "fr").toEqual([]);
        const empty = (texts: Record<string, string>) =>
          Object.entries(texts)
            .filter(([, v]) => v.trim() === "")
            .map(([k]) => k);
        expect(empty(e.texts), "empty in en").toEqual([]);
        expect(empty(f.texts), "empty in fr").toEqual([]);
      });

      it("has the same keys in English and French", () => {
        expect(
          [...fk.keys()].filter((k) => !ek.has(k)),
          "only in fr",
        ).toEqual([]);
        expect(
          [...ek.keys()].filter((k) => !fk.has(k)),
          "only in en",
        ).toEqual([]);
      });

      it("has each plural form the language needs", () => {
        for (const [lng, keys] of [
          ["en", ek],
          ["fr", fk],
        ] as const) {
          const want = [...categories(lng)].sort();
          const other = lng === "en" ? fk : ek;
          for (const [k, v] of keys) {
            if (v.forms.size === 0 && (other.get(k)?.forms.size ?? 0) === 0) continue;
            expect([...v.forms].sort(), `${lng} ${ns}:${k}: ${want.map((c) => `_${c}`).join(", ")}`).toEqual(want);
          }
        }
      });

      it("uses the same variables and markup in both", () => {
        for (const [k, v] of ek) {
          const w = fk.get(k);
          if (!w) continue;
          expect(set(w.texts.flatMap(variables)), `variables of ${ns}:${k}`).toEqual(set(v.texts.flatMap(variables)));
          expect(set(w.texts.flatMap(tags)), `markup of ${ns}:${k}`).toEqual(set(v.texts.flatMap(tags)));
        }
      });

      it("writes French with its spaces and apostrophes", () => {
        const wrong = Object.entries(f.texts).filter(
          ([, v]) => /[ \t][:;!?»]/.test(v) || /«[ \t]/.test(v) || v.includes("’"),
        );
        expect(
          wrong.map(([k]) => k),
          "a no-break space (U+00A0) before : ; ! ? » and after «, and the straight apostrophe '",
        ).toEqual([]);
      });
    });
  }
});
