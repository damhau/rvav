// What the server names in English for what rvav builds in (ADR-0061):
// roles, permissions, a request's own steps, day-2 actions and notification
// kinds, in the language shown. locales/en/names.json holds the server's
// words exactly; anything else (a custom role, an AWX action, a kind newer
// than this page) shows as the server wrote it.
import { i18n, type Namespace } from ".";

/**
 * A text the server wrote in English, in the language shown when it is one
 * of the texts at `path` in `ns`'s English (the text itself, or one of an
 * object's); else as the server wrote it.
 */
export function serverText(ns: Namespace, path: string, text: string): string {
  const shown = i18n.getFixedT(null, ns) as unknown as (key: string) => string;
  const en: unknown = i18n.getResource("en", ns, path);
  if (en === text) return shown(path);
  if (en && typeof en === "object") {
    const variant = Object.entries(en).find(([, v]) => v === text)?.[0];
    if (variant) return shown(`${path}.${variant}`);
  }
  return text;
}

const builtIn = (path: string, text: string) => serverText("names", path, text);

const keyed = (group: string, key: string, text: string) =>
  /^[\w-]+$/.test(key) ? builtIn(`${group}.${key}`, text) : text;

/** A role's name: a built-in role's in the language shown. */
export const roleName = (role: { key: string; name: string }) => keyed("role", role.key, role.name);

/** The role editor's area for a permission, such as Projects. */
export const areaName = (area: string) => builtIn("area", area);

/** What a permission allows, in the catalogue's words. */
export const permissionSummary = (p: { id: string; summary: string }) =>
  keyed("permission", p.id.replaceAll(".", "_"), p.summary);

/** A day-2 action's name: a built-in action's in the language shown, an AWX one's as named. */
export const day2Name = (action: { key: string; name: string }) => keyed("day2", action.key, action.name);

/** A day-2 group, such as power, in lower case for a sentence. */
export const day2Group = (group: string) => keyed("day2Group", group, group);

const stepGroups = new Map([
  ["submit", "submit"],
  ["request.submit", "submit"],
  ["policy", "policy"],
  ["policy.approval", "policy"],
  ["ready", "ready"],
  ["request.ready", "ready"],
  ["placement", "placement"],
]);

/**
 * A run step's title: a request's own steps (submitted, its approval, ready)
 * in the language shown. Without a key, as a request's current step comes,
 * any of their titles. The policy step's outcome once decided, and every
 * other step's title, stay as the server wrote them.
 */
export function stepTitle(step: { key?: string; title: string }): string {
  if (step.key !== undefined) {
    const group = stepGroups.get(step.key);
    return group ? builtIn(`step.${group}`, step.title) : step.title;
  }
  for (const group of new Set(stepGroups.values())) {
    const out = builtIn(`step.${group}`, step.title);
    if (out !== step.title) return out;
  }
  return step.title;
}

const kindPart = (kind: string, part: "name" | "when", text: string) =>
  /^\w+$/.test(kind) ? builtIn(`kind.${kind}.${part}`, text) : text;

/** A notification kind's name. */
export const kindName = (k: { kind: string; name: string }) => kindPart(k.kind, "name", k.name);

/** Whom a notification kind goes to, and when. */
export const kindWhen = (k: { kind: string; when: string }) => kindPart(k.kind, "when", k.when);
