// Who an item is shared with in a project (G1.2, ADR-0019): the mock
// user's catalog, item, form and submit agree with it, and the sharing
// sheet asks who could request.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { rolesGiven } from "./administration";
import {
  allMembers,
  awxOwner,
  everyProjectShare,
  inheritedOf,
  type ItemSharing,
  itemSharing,
  ownerSharing,
} from "./catalogAdmin";
import { userGroups } from "./logic";
import { heldIn, roleRefs } from "./roles";
import type { Db, StoredItem } from "./seed";

type S = components["schemas"];

const covers = (db: Db, a: S["CatalogAudience"], projectId: string, userId: string) =>
  allMembers(a) ||
  a.user_ids.includes(userId) ||
  a.groups.some((g) => (userGroups[userId] ?? []).includes(g)) ||
  a.roles.some((r) => rolesGiven(db, projectId, userId).includes(r));

const sharingCovers = (db: Db, sh: ItemSharing, projectId: string, userId: string) =>
  (sh.all && covers(db, sh.everywhere, projectId, userId)) ||
  (sh.projects[projectId] !== undefined && covers(db, sh.projects[projectId], projectId, userId));

function ownShareCovers(db: Db, item: StoredItem, projectId: string, userId: string) {
  if (!item.projectIds.includes(projectId)) return false;
  const sh = itemSharing(db, item.id);
  return !sh || sharingCovers(db, sh, projectId, userId);
}

/** Whether a project's catalog has the item: its own shares or its category's or source's name the project. */
export const offeredIn = (db: Db, item: StoredItem, projectId: string) =>
  item.projectIds.includes(projectId) ||
  inheritedOf(db, item).some(({ sharing }) => sharing.all || projectId in sharing.projects);

/** Whether someone finds the item in a project: a share of its own, its category's or its source's covers them. */
export function sharedWith(db: Db, item: StoredItem, projectId: string, userId = db.me.user.id): boolean {
  return (
    ownShareCovers(db, item, projectId, userId) ||
    inheritedOf(db, item).some(({ sharing }) => sharingCovers(db, sharing, projectId, userId))
  );
}

type Origin = S["CatalogShareOrigin"];

/**
 * Who could request in a project with an audience: the sharing's every-project
 * share and the shares inherited from elsewhere add to it.
 */
function requesters(
  db: Db,
  p: S["Project"],
  a: S["CatalogProjectAudience"],
  every: S["CatalogAudience"] | undefined,
  inherited: { from: Origin; sharing: ItemSharing }[],
): S["CatalogRequesters"] {
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
    const through = inherited.filter(({ sharing }) => sharingCovers(db, sharing, p.id, u.id)).map(({ from }) => from);
    if (allMembers(a) || named || groups.length > 0 || held.length > 0 || everywhere || through.length > 0)
      out.people.push({ user: u, named, everywhere, groups, roles: roleRefs(db, held), inherited: through });
    else out.left_out.push(u);
  }
  return out;
}

const notFound = () => HttpResponse.json({ title: "Not found", status: 404 }, { status: 404 });

function unknownProject(id: string) {
  return HttpResponse.json(
    {
      title: "Some values need fixing",
      status: 422,
      errors: [{ field: "/project_id", code: "unknown", message: `No such project: ${id}.` }],
    },
    { status: 422 },
  );
}

/** Who a category or AWX would let request: its own every-project share adds to the audience. */
function ownerRequesters(db: Db, owner: Parameters<typeof ownerSharing>[1], a: S["CatalogProjectAudience"]) {
  const p = db.projects.find((x) => x.id === a.project_id);
  if (!p) return unknownProject(a.project_id);
  const sh = ownerSharing(db, owner);
  return HttpResponse.json(requesters(db, p, a, sh.all ? sh.everywhere : undefined, []));
}

export function sharingHandlers(db: Db, base: string, wait: () => Promise<void>) {
  return [
    http.post<{ id: string }>(
      `${base}/admin/catalog/items/:id/entitlements/requesters`,
      async ({ params, request }) => {
        await wait();
        const item = db.items.find((i) => i.id === params.id || i.slug === params.id);
        if (!item) return notFound();
        const a = (await request.json()) as S["CatalogProjectAudience"];
        const p = db.projects.find((x) => x.id === a.project_id);
        if (!p) return unknownProject(a.project_id);
        // Shares add up: the item's every-project share and those it inherits cover people too.
        return HttpResponse.json(requesters(db, p, a, everyProjectShare(db, item.id), inheritedOf(db, item)));
      },
    ),
    http.post<{ key: string }>(
      `${base}/admin/catalog/categories/:key/entitlements/requesters`,
      async ({ params, request }) => {
        await wait();
        if (!db.categories.some((c) => c.key === params.key)) return notFound();
        const a = (await request.json()) as S["CatalogProjectAudience"];
        return ownerRequesters(db, `category:${params.key}`, a);
      },
    ),
    http.post(`${base}/admin/awx/entitlements/requesters`, async ({ request }) => {
      await wait();
      return ownerRequesters(db, awxOwner, (await request.json()) as S["CatalogProjectAudience"]);
    }),
  ];
}
