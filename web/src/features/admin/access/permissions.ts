// What permissions allow, in the catalogue's words (ADR-0034).
import type { components } from "@/lib/api/schema";
import { formatList } from "@/lib/format";
import { i18n } from "@/lib/i18n";

type S = components["schemas"];
export type Catalogue = S["PermissionCatalogue"];

export const scopeText = (scope: S["RoleScope"]) => i18n.t(`admin-access:scope.${scope}`);

/** The catalogue's permissions by area, in the catalogue's order. */
export function byArea(c: Catalogue): [string, S["PermissionInfo"][]][] {
  const out = new Map<string, S["PermissionInfo"][]>();
  for (const p of c.items) out.set(p.area, [...(out.get(p.area) ?? []), p]);
  return [...out.entries()];
}

/** What these permissions allow, a sentence each: the catalogue's, then a day-2 group's. */
export function allows(c: Catalogue, permissions: string[]): { id: string; text: string }[] {
  const held = new Set(permissions);
  const out = c.items.filter((p) => held.has(p.id)).map((p) => ({ id: p.id, text: p.summary }));
  for (const g of c.day2) {
    if (held.has(g.permission)) {
      out.push({ id: g.permission, text: i18n.t("admin-access:allows.group", { group: g.group }) });
      continue;
    }
    const prefix = `${g.permission}:`;
    const actions = permissions.filter((p) => p.startsWith(prefix));
    if (actions.length === 0) continue;
    const names = actions.map((p) => g.actions.find((a) => a.permission === p)?.name ?? p.slice(prefix.length));
    out.push({
      id: actions.join(" "),
      text: i18n.t("admin-access:allows.actions", { group: g.group, names: formatList(names), count: names.length }),
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
    why: i18n.t("admin-access:allows.rvavOnly", { names: formatList(rvav), count: rvav.length }),
  };
}

/** Sorts permissions as the catalogue lists them, day-2 ones last. */
export function inOrder(c: Catalogue, permissions: Iterable<string>): string[] {
  const rank = new Map(c.items.map((p, i) => [p.id, i]));
  return [...new Set(permissions)].sort(
    (a, b) => (rank.get(a) ?? 1e6) - (rank.get(b) ?? 1e6) || (a < b ? -1 : a > b ? 1 : 0),
  );
}
