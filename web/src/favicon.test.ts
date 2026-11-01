// The page's icons: the browser tab's (SVG, and ICO for browsers without
// SVG icons) and iOS's home screen's. index.html links each, and each is in
// public/, which the build copies next to the page.
import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// Vitest runs in web/, where its configuration is (jsdom gives no file URL).
const web = (path: string) => resolve(process.cwd(), path);
const page = readFileSync(web("index.html"), "utf8");
const icons = [...page.matchAll(/<link rel="(?:icon|apple-touch-icon)" href="([^"]+)"/g)].map((m) => m[1]);

describe("the page's icons", () => {
  it("links the SVG icon, its ICO fallback and iOS's", () => {
    expect(icons).toEqual(["/favicon.ico", "/favicon.svg", "/apple-touch-icon.png"]);
  });

  it.each(icons)("%s is in public/", (href) => {
    expect(existsSync(web(`public${href}`))).toBe(true);
  });

  it("the SVG icon follows dark mode", () => {
    const svg = readFileSync(web("public/favicon.svg"), "utf8");
    expect(svg).toContain('viewBox="0 0 32 32"');
    expect(svg).toContain("@media (prefers-color-scheme: dark)");
  });
});
