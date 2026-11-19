// Who an item is shared with in a project (G1.2, ADR-0019): the mock
// user's catalog, item, form and submit agree with it, and the sharing
// sheet asks who could request.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { rolesGiven } from "./administration";
import { allMembers, everyProjectShare, itemSharing } from "./catalogAdmin";
import { userGroups } from "./logic";
import { heldIn, roleRefs } from "./roles";
import type { Db, StoredItem } from "./seed";

type S = components["schemas"];

const covers = (db: Db, a: S["CatalogAudience"], projectId: string, userId: string) =>
  allMembers(a) ||
  a.user_ids.includes(userId) ||
  a.groups.some((g) => (userGroups[userId] ?? []).includes(g)) ||
  a.roles.some((r) => rolesGiven(db, projectId, userId).includes(r));

/** Whether someone finds the item in a project: the project's catalog has it, and a share covers them. */
export function sharedWith(db: Db, item: StoredItem, projectId: string, userId = db.me.user.id): boolean {
  if (!item.projectIds.includes(projectId)) return false;
  const sh = itemSharing(db, item.id);
  if (!sh) return true;
  if (sh.all && covers(db, sh.everywhere, projectId, userId)) return true;
  const a = sh.projects[projectId];
  return a !== undefined && covers(db, a, projectId, userId);
}

export function sharingHandlers(db: Db, base: string, wait: () => Promise<void>) {
  return [
    http.post<{ id: string }>(
      `${base}/admin/catalog/items/:id/entitlements/requesters`,
      async ({ params, request }) => {
        await wait();
        const item = db.items.find((i) => i.id === params.id || i.slug === params.id);
        if (!item) return HttpResponse.json({ title: "Not found", status: 404 }, { status: 404 });
        const a = (await request.json()) as S["CatalogProjectAudience"];
        const p = db.projects.find((x) => x.id === a.project_id);
        if (!p)
          return HttpResponse.json(
            {
              title: "Some values need fixing",
              status: 422,
              errors: [{ field: "/project_id", code: "unknown", message: `No such project: ${a.project_id}.` }],
            },
            { status: 422 },
          );
        const out: S["CatalogRequesters"] = {
          project: { id: p.id, code: p.code, name: p.name },
          people: [],
          left_out: [],
          not_requesters: [],
          groups: a.groups.map((name) => ({
            name,
            seen: db.users.filter((u) => (userGroups[u.id] ?? []).includes(name)).length,
          })),
        };
        // Shares add up: the item's every-project share covers people too.
        const every = everyProjectShare(db, item.id);
        if (every) out.everywhere = every;
        for (const u of db.users) {
          const roles = rolesGiven(db, p.id, u.id);
          const named = a.user_ids.includes(u.id);
          if (!named && roles.length === 0) continue;
          if (!heldIn(db, [], roles).includes("request.submit")) {
            if (named) out.not_requesters.push(u);
            continue;
          }
          const groups = (userGroups[u.id] ?? []).filter((g) => a.groups.includes(g));
          const held = roles.filter((r) => a.roles.includes(r));
          const everywhere = !!every && covers(db, every, p.id, u.id);
          if (allMembers(a) || named || groups.length > 0 || held.length > 0 || everywhere)
            out.people.push({ user: u, named, everywhere, groups, roles: roleRefs(db, held) });
          else out.left_out.push(u);
        }
        return HttpResponse.json(out);
      },
    ),
  ];
}
