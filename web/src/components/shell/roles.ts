import type { Me, ProjectRole } from "@/lib/api/types";

export const roleLabel = { "project-admin": "Project admin", member: "Member", viewer: "Viewer" } as const;
const globalRoleLabel = { admin: "Administrator", "catalog-admin": "Catalog admin", auditor: "Auditor" } as const;

/**
 * What the account button says under the name: the role in the current
 * project, or for someone in none (an administrator setting up a new install,
 * an approver) their own role, if they have one. "Viewer" would name a role
 * they hold nowhere.
 */
export function accountRole(me: Me, projectId: string, role: ProjectRole): string | undefined {
  if (projectId) return roleLabel[role];
  const own = (["admin", "catalog-admin", "auditor"] as const).find((r) => me.global_roles.includes(r));
  return own && globalRoleLabel[own];
}
