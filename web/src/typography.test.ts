import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

// The type scale in styles.css (docs/ux.md): Tailwind's default sizes are
// cleared, and an arbitrary size would step outside the scale. Vitest runs in
// web/ (jsdom gives no file URL).
const root = resolve(process.cwd(), "src");
const outside =
  /(^|[\s"'`{(:!])(text-(?:xs|sm|base|lg|[2-9]?xl)|text-\[\d+(?:\.\d+)?(?:px|rem|em)\])(?=[\s"'`}),!]|$)/gm;

function sources(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) sources(p, out);
    else if (/\.tsx?$/.test(name) && !name.endsWith(".d.ts") && !name.endsWith(".test.ts")) out.push(p);
  }
  return out;
}

describe("type scale", () => {
  it("pages use the scale's sizes only", () => {
    const found: string[] = [];
    for (const file of sources(root)) {
      const text = readFileSync(file, "utf8");
      for (const m of text.matchAll(outside)) found.push(`${relative(root, file)}: ${m[2]}`);
    }
    expect(found).toEqual([]);
  });
});
