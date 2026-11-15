// What permissions allow, in the catalogue's words (ADR-0034).
import type { components } from "@/lib/api/schema";

type S = components["schemas"];
export type Catalogue = S["PermissionCatalogue"];

export const scopeText: Record<S["RoleScope"], string> = {
  project: "In a project",
  rvav: "For all of rvav",
  any: "In a project or for all of rvav",
};

/** The catalogue's permissions by area, in the catalogue's order. */
export function byArea(c: Catalogue): [string, S["PermissionInfo"][]][] {
  const out = new Map<string, S["PermissionInfo"][]>();
  for (const p of c.items) out.set(p.area, [...(out.get(p.area) ?? []), p]);
  return [...out.entries()];
}

const list = (names: string[]) =>
  names.length <= 1 ? (names[0] ?? "") : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;

/** What these permissions allow, a sentence each: the catalogue's, then a day-2 group's. */
export function allows(c: Catalogue, permissions: string[]): { id: string; text: string }[] {
  const held = new Set(permissions);
  const out = c.items.filter((p) => held.has(p.id)).map((p) => ({ id: p.id, text: p.summary }));
  for (const g of c.day2) {
    if (held.has(g.permission)) {
      out.push({ id: g.permission, text: `Run every ${g.group} day-2 action, those added later included.` });
      continue;
    }
    const prefix = `${g.permission}:`;
    const actions = permissions.filter((p) => p.startsWith(prefix));
    if (actions.length === 0) continue;
    const names = actions.map((p) => g.actions.find((a) => a.permission === p)?.name ?? p.slice(prefix.length));
    out.push({
      id: actions.join(" "),
      text: `Run the ${g.group} day-2 ${actions.length === 1 ? "action" : "actions"} ${list(names)}.`,
    });
  }
  return out;
}

/** Where a role with these permissions can be given, and why. */
export function scopeOf(c: Catalogue, permissions: string[]): { scope: S["RoleScope"]; why?: string } {
  const rvav = c.items.filter((p) => p.scope === "rvav" && permissions.includes(p.id)).map((p) => p.id);
  if (rvav.length === 0) return { scope: "any" };
  return {
    scope: "rvav",
    why: `${list(rvav)} ${rvav.length === 1 ? "is a permission" : "are permissions"} for all of rvav only.`,
  };
}

/** Sorts permissions as the catalogue lists them, day-2 ones last. */
export function inOrder(c: Catalogue, permissions: Iterable<string>): string[] {
  const rank = new Map(c.items.map((p, i) => [p.id, i]));
  return [...new Set(permissions)].sort(
    (a, b) => (rank.get(a) ?? 1e6) - (rank.get(b) ?? 1e6) || (a < b ? -1 : a > b ? 1 : 0),
  );
}
