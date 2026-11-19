// The portal's branding (ADR-0059): its name, accent and logos, read before
// the app renders, the sign-in page included, and applied to the whole page.
import { useSyncExternalStore } from "react";
import { api, call } from "./api/client";
import type { AccentPalette, Branding } from "./api/types";

/** rvav's own, as styles.css has it: what shows until the branding is read, or when it can't be. */
export const rvavBranding: Branding = {
  name: "rvav",
  light: { accent: "#0e6488", accent_fg: "#ffffff", accent_soft: "#dcedf4" },
  dark: { accent: "#4fb0d8", accent_fg: "#06222e", accent_soft: "#123344" },
};

const STYLE_ID = "rvav-branding";
const listeners = new Set<() => void>();
let current: Branding = rvavBranding;

const tokens = (p: AccentPalette) =>
  `--accent: ${p.accent}; --accent-fg: ${p.accent_fg}; --accent-soft: ${p.accent_soft}; --focus: ${p.accent};`;

/**
 * The stylesheet that puts the accent in both themes, as styles.css sets
 * them: html:root outranks its :root rules wherever either comes first.
 * Empty for rvav's own accent.
 */
export function brandingCss(b: Branding): string {
  if (!b.accent) return "";
  return [
    `html:root { ${tokens(b.light)} }`,
    `@media (prefers-color-scheme: dark) { html:root:not([data-theme="light"]) { ${tokens(b.dark)} } }`,
    `html:root[data-theme="dark"] { ${tokens(b.dark)} }`,
  ].join("\n");
}

/** Shows a branding: the page's accent, the browser's title, and the name and logos where the app shows them. */
export function applyBranding(b: Branding) {
  current = b;
  const doc = globalThis.document;
  if (doc) {
    doc.title = b.name;
    let style = doc.getElementById(STYLE_ID);
    const css = brandingCss(b);
    if (css && !style) {
      style = doc.createElement("style");
      style.id = STYLE_ID;
      doc.head.append(style);
    }
    if (style) style.textContent = css;
  }
  listeners.forEach((fn) => fn());
}

/** Reads the branding anyone may read; rvav's own stays when it can't be read. */
export async function loadBranding(): Promise<void> {
  try {
    applyBranding(await call(api.GET("/branding")));
  } catch {
    // The page still works in rvav's colours; the sign-in page says if the API is down.
  }
}

const subscribe = (fn: () => void) => {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
};

export function useBranding(): Branding {
  return useSyncExternalStore(subscribe, () => current);
}
