import type { Me, ProjectRef } from "./api/types";

// The web app asks for permissions, never role names (ADR-0034); the server
// decides again.

/** Says whether the caller holds a permission for all of rvav, or in this project. */
export function can(me: Me, permission: string, projectId?: string): boolean {
  if (me.permissions.includes(permission)) return true;
  return (
    Boolean(projectId) && me.projects.some((p) => p.project.id === projectId && p.permissions.includes(permission))
  );
}

/** Says whether the caller holds a permission for all of rvav or in at least one project. */
export function canAnywhere(me: Me, permission: string): boolean {
  return me.permissions.includes(permission) || me.projects.some((p) => p.permissions.includes(permission));
}

/** The projects where the caller holds a permission, or "all" when it is held for all of rvav. */
export function projectsWith(me: Me, permission: string): ProjectRef[] | "all" {
  if (me.permissions.includes(permission)) return "all";
  return me.projects.filter((p) => p.permissions.includes(permission)).map((p) => p.project);
}

/** The permissions that open a project's administration page: reading its settings, or changing one of its parts. */
export const projectPagePermissions = [
  "project.settings.read",
  "placement.manage",
  "project.quotas.manage",
  "project.members.manage",
  "project.update",
];

/** The projects where the caller holds one of these permissions, or "all" when one is held for all of rvav. */
export function projectsWithAny(me: Me, permissions: string[]): ProjectRef[] | "all" {
  if (permissions.some((p) => me.permissions.includes(p))) return "all";
  return me.projects.filter((p) => permissions.some((x) => p.permissions.includes(x))).map((p) => p.project);
}
