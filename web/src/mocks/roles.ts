// Roles and permissions as the server has them (ADR-0034): the catalogue and
// the built-in roles are a copy of internal/auth/testdata/roles.json, which
// TestRolesFixture keeps current; roles.test.ts checks the two are the same.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { catalogue as day2Actions } from "./day2";
import { audited } from "./governance";
import fixture from "./roles.json";
import type { Db } from "./seed";

type S = components["schemas"];

export interface MockRole {
  key: string;
  name: string;
  description: string;
  permissions: string[];
  built_in: boolean;
}

export const permissionCatalogue = fixture.catalogue as S["PermissionInfo"][];
export const day2Groups = fixture.day2_groups;
export const builtinRoles: (MockRole & { scope: "project" | "rvav" })[] = fixture.roles.map((r) => ({
  key: r.key,
  name: r.name,
  description: "",
  permissions: r.permissions,
  built_in: true,
  scope: r.scope as "project" | "rvav",
}));

/** The custom roles made in this session. */
export function customRoles(db: Db): MockRole[] {
  const d = db as Db & { customRoles?: MockRole[] };
  return (d.customRoles ??= []);
}

export function roleOf(db: Db | null, key: string): MockRole | undefined {
  return builtinRoles.find((r) => r.key === key) ?? (db ? customRoles(db).find((r) => r.key === key) : undefined);
}

/** Says whether a permission is one rvav knows: in the catalogue, or a day-2 group's or action's. */
export function knownPermission(p: string): boolean {
  if (permissionCatalogue.some((c) => c.id === p)) return true;
  const m = /^day2\.([a-z]+)(?::([a-z][a-z0-9_.-]*))?$/.exec(p);
  return Boolean(m && day2Groups.includes(m[1]!));
}

const rvavOnly = (p: string) => permissionCatalogue.some((c) => c.id === p && c.scope === "rvav");

export function roleScope(r: MockRole): S["RoleScope"] {
  if (r.built_in) return builtinRoles.find((b) => b.key === r.key)!.scope;
  return r.permissions.some(rvavOnly) ? "rvav" : "any";
}

/** Says whether a role may be given in a project (or for all of rvav without one). */
export function assignable(r: MockRole, inProject: boolean): boolean {
  const scope = roleScope(r);
  return scope === "any" || (scope === "project") === inProject;
}

/**
 * The permissions held in a project by these roles, those given for all of
 * rvav included, sorted as the server sorts them. approval.project counts
 * from a role given in the project only.
 */
export function heldIn(db: Db | null, rvav: string[], given: string[]): string[] {
  const out = new Set<string>();
  for (const k of rvav) for (const p of roleOf(db, k)?.permissions ?? []) if (p !== "approval.project") out.add(p);
  for (const k of given) for (const p of roleOf(db, k)?.permissions ?? []) out.add(p);
  return [...out].sort();
}

export function roleRefs(db: Db | null, keys: string[]): S["RoleRef"][] {
  return keys.flatMap((k) => {
    const r = roleOf(db, k);
    return r ? [{ key: r.key, name: r.name }] : [];
  });
}

const rank: Record<string, number> = { viewer: 1, member: 2, "project-admin": 3 };

/** The strongest built-in project role among these, or null. */
export function strongest(keys: string[]): S["ProjectRole"] | null {
  let out: S["ProjectRole"] | null = null;
  for (const k of keys) if ((rank[k] ?? 0) > (out ? rank[out]! : 0)) out = k as S["ProjectRole"];
  return out;
}

/** A project in /me, for someone holding these roles for all of rvav and these in it. */
export function meProject(db: Db | null, project: S["ProjectRef"], rvav: string[], given: string[]): S["MeProject"] {
  return { project, roles: roleRefs(db, [...given].sort()), permissions: heldIn(db, rvav, given) };
}

/** GET /permissions: the catalogue and the day-2 actions the mocks offer. */
export function permissionsAnswer(): S["PermissionCatalogue"] {
  return {
    items: permissionCatalogue,
    day2: day2Groups.map((g) => ({
      group: g,
      permission: `day2.${g}`,
      actions: day2Actions
        .filter((a) => a.group === g)
        .map((a) => ({ key: a.key, name: a.name, permission: `day2.${g}:${a.key}` })),
    })),
  };
}

function problem(status: number, title: string, detail?: string, errors?: S["FieldError"][]) {
  return HttpResponse.json(
    {
      type: "https://rvav.dev/problems/x",
      title,
      status,
      request_id: "req_mock",
      ...(detail ? { detail } : {}),
      ...(errors ? { errors } : {}),
    },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}
const invalid = (field: string, code: string, message: string) =>
  problem(422, "Some inputs need attention", message, [{ field, code, message }]);

/** What the roles API reads of the rest of the mocks' administration. */
export interface RoleUses {
  /** The projects where a role is given, once per assignment or mapping. */
  given: (key: string) => string[];
  holders: (key: string) => number;
  policies: (key: string) => string[];
  assignments: () => S["RoleAssignment"][];
  /** The roles mapped to groups for all of rvav, once per mapping. */
  rvavMappings: () => string[];
  /** The ways a project has project.members.manage given in it, leaving out a role. */
  membersManagers: (projectId: string, exceptRole: string) => number;
  projectName: (id: string) => string;
}

const KEY = /^[a-z][a-z0-9-]{1,40}$/;

/** Checks a role's permissions as the server does: known, at least one, project.see with one given in a project. */
function permissionErrors(perms: string[]): Response | null {
  const unknown = perms.findIndex((p) => !knownPermission(p));
  if (unknown >= 0) return invalid(`/permissions/${unknown}`, "unknown", `"${perms[unknown]}" is no permission.`);
  if (perms.length === 0) return invalid("/permissions", "required", "Give the role at least one permission.");
  if (perms.some((p) => !rvavOnly(p)) && !perms.includes("project.see"))
    return invalid(
      "/permissions",
      "project_see",
      "Add project.see: it allows something in a project, and every check there starts by seeing it.",
    );
  return null;
}

export function rolesHandlers(db: Db, base: string, wait: () => Promise<void>, uses: RoleUses) {
  const present = (r: MockRole): S["Role"] => ({
    key: r.key,
    name: r.name,
    description: r.description,
    permissions: r.permissions,
    built_in: r.built_in,
    scope: roleScope(r),
    holders: uses.holders(r.key),
    policies: uses.policies(r.key),
  });
  const managesAccess = (key: string) => roleOf(db, key)?.permissions.includes("access.manage") ?? false;
  /** As the server's RvavHolders: the ways anyone holds access.manage for all of rvav, but these. */
  const accessManagers = (except: { role?: string; assignment?: S["RoleAssignment"] }) => {
    const counts = (key: string) => key !== except.role && managesAccess(key);
    return (
      uses.assignments().filter((a) => a !== except.assignment && counts(a.role.key)).length +
      uses.rvavMappings().filter(counts).length
    );
  };
  return [
    http.get(`${base}/permissions`, async () => {
      await wait();
      return HttpResponse.json(permissionsAnswer());
    }),
    http.get(`${base}/admin/roles`, async () => {
      await wait();
      return HttpResponse.json({ items: [...builtinRoles, ...customRoles(db)].map(present) });
    }),
    http.post(`${base}/admin/roles`, async ({ request }) => {
      await wait();
      const c = (await request.json()) as S["RoleCreate"];
      if (!KEY.test(c.key) || builtinRoles.some((r) => r.key === c.key) || ["owner", "new"].includes(c.key))
        return invalid(
          "/key",
          "pattern",
          "2 to 41 lower-case letters, digits and hyphens, starting with a letter; not a built-in role's key, nor owner or new.",
        );
      if (!c.name.trim()) return invalid("/name", "required", "Give the role a name.");
      const perms = [...new Set(c.permissions)];
      const bad = permissionErrors(perms);
      if (bad) return bad;
      if (customRoles(db).some((r) => r.key === c.key)) return problem(409, "Conflict", `A role has the key ${c.key}.`);
      const r: MockRole = {
        key: c.key,
        name: c.name.trim(),
        description: c.description?.trim() ?? "",
        permissions: perms,
        built_in: false,
      };
      customRoles(db).push(r);
      audited(db, "role.create", "role", r.key, { name: r.name, permissions: perms });
      return HttpResponse.json(present(r), { status: 201 });
    }),
    http.get(`${base}/admin/roles/:key`, async ({ params }) => {
      await wait();
      const r = roleOf(db, String(params.key));
      return r ? HttpResponse.json(present(r)) : problem(404, "Not found");
    }),
    http.patch(`${base}/admin/roles/:key`, async ({ params, request }) => {
      await wait();
      const r = roleOf(db, String(params.key));
      if (!r) return problem(404, "Not found");
      if (r.built_in)
        return problem(409, "Conflict", `${r.name} is built in: it can't be changed. Make a role of your own instead.`);
      const u = (await request.json()) as S["RoleUpdate"];
      if (u.name !== undefined && !u.name.trim()) return invalid("/name", "required", "Give the role a name.");
      const perms = u.permissions ? [...new Set(u.permissions)] : r.permissions;
      const bad = u.permissions ? permissionErrors(perms) : null;
      if (bad) return bad;
      const where = [...new Set(uses.given(r.key))];
      if (perms.some(rvavOnly) && where.length > 0)
        return invalid(
          "/permissions",
          "scope",
          `${r.name} is given in ${where.map(uses.projectName).join(", ")}: a permission for all of rvav only can't be added while it is. Take it away there first.`,
        );
      const added = perms.filter((p) => !r.permissions.includes(p));
      const removed = r.permissions.filter((p) => !perms.includes(p));
      if (removed.includes("access.manage") && accessManagers({ role: r.key }) === 0)
        return problem(
          409,
          "Conflict",
          "Nobody would be an administrator any more: give access.manage to someone else first.",
        );
      if (removed.includes("project.members.manage"))
        for (const projectId of where)
          if (uses.membersManagers(projectId, r.key) === 0)
            return problem(
              409,
              "Conflict",
              `${uses.projectName(projectId)} would have no project admin: make someone else one first.`,
            );
      Object.assign(r, {
        ...(u.name !== undefined ? { name: u.name.trim() } : {}),
        ...(u.description !== undefined ? { description: u.description.trim() } : {}),
        permissions: perms,
      });
      audited(db, "role.update", "role", r.key, u.permissions ? { added, removed } : {});
      return HttpResponse.json(present(r));
    }),
    http.delete(`${base}/admin/roles/:key`, async ({ params }) => {
      await wait();
      const r = roleOf(db, String(params.key));
      if (!r) return problem(404, "Not found");
      if (r.built_in) return problem(409, "Conflict", `${r.name} is built in: it can't be deleted.`);
      const policies = uses.policies(r.key);
      if (policies.length > 0)
        return problem(409, "Conflict", `The policies ${policies.join(", ")} name ${r.name}: change them first.`);
      const holders = uses.holders(r.key);
      if (holders > 0)
        return problem(409, "Conflict", `${r.name} is given ${holders} times, to users or groups: take it away first.`);
      customRoles(db).splice(customRoles(db).indexOf(r), 1);
      audited(db, "role.delete", "role", r.key, { name: r.name });
      return new HttpResponse(null, { status: 204 });
    }),
    http.get(`${base}/admin/role-assignments`, async ({ request }) => {
      await wait();
      const url = new URL(request.url);
      const role = url.searchParams.get("role");
      const userId = url.searchParams.get("user_id");
      return HttpResponse.json({
        items: uses.assignments().filter((a) => (!role || a.role.key === role) && (!userId || a.user.id === userId)),
      });
    }),
    http.post(`${base}/admin/role-assignments`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["RoleAssignmentWrite"];
      const who = db.users.find((u) => u.id === w.user_id);
      if (!who) return invalid("/user_id", "unknown", "There is no such user.");
      const r = roleOf(db, w.role);
      if (!r) return invalid("/role", "unknown", "There is no such role.");
      if (r.key === "user" || !assignable(r, false))
        return invalid("/role", "scope", `${r.name} is given in a project, not for all of rvav.`);
      if (uses.assignments().some((a) => a.user.id === who.id && a.role.key === r.key))
        return problem(409, "Conflict", `${who.name} already holds ${r.name}.`);
      const a: S["RoleAssignment"] = {
        id: `ras_${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`,
        user: who,
        role: { key: r.key, name: r.name },
        fixed: false,
        created_at: new Date().toISOString(),
      };
      uses.assignments().push(a);
      audited(db, "role_assignment.create", "role_assignment", a.id!, { user_id: who.id, role: r.key });
      return HttpResponse.json(a, { status: 201 });
    }),
    http.delete(`${base}/admin/role-assignments/:id`, async ({ params }) => {
      await wait();
      const list = uses.assignments();
      const a = list.find((x) => x.id === params.id);
      if (!a) return problem(404, "Not found");
      if (managesAccess(a.role.key) && accessManagers({ assignment: a }) === 0)
        return problem(409, "Conflict", "Nobody would be an administrator any more: make someone else one first.");
      list.splice(list.indexOf(a), 1);
      audited(db, "role_assignment.delete", "role_assignment", a.id!, { user_id: a.user.id, role: a.role.key });
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
