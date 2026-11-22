import type { Me } from "@/lib/api/types";
import { roleName } from "@/lib/i18n/names";

/**
 * What the account button and the project menu say under a name: the names
 * of the roles given in the project, or of those held for all of rvav where
 * it gives none (an administrator in a project they were not added to,
 * someone in no project). User, which everyone holds, says nothing.
 */
export function rolesLabel(me: Me, projectId: string): string | undefined {
  const given = me.projects.find((p) => p.project.id === projectId)?.roles ?? [];
  const held = given.length > 0 ? given : me.roles.filter((r) => r.key !== "user");
  return held.length > 0 ? held.map(roleName).join(", ") : undefined;
}
