import { i18n } from "@/lib/i18n";
import { day2Name } from "@/lib/i18n/names";

/** The built-in day-2 actions' keys, in the order the policies offer them. */
export const day2Actions = [
  "power_on", "power_off", "shutdown_guest", "restart_guest", "suspend", "reset", "console", "resize", "disk_add", "disk_extend", "disk_remove",
  "tags_edit", "nic_add", "nic_move", "nic_remove", "iso_mount", "iso_unmount", "snapshot_create", "snapshot_revert", "snapshot_delete",
  "nsx_group_add", "nsx_group_remove", "rename", "edit", "change_owner", "change_project", "extend_lease", "vm_unregister", "unregister",
  "delete",
]; // prettier-ignore

const builtIn = new Set(day2Actions);

/** A key of a built-in day-2 action, rather than an AWX or workflow action's, or a pattern. */
export const isBuiltIn = (key: string) => builtIn.has(key);

/** A built-in day-2 action's name in the language shown, from its key. */
export function actionName(key: string): string {
  const en: unknown = i18n.getResource("en", "names", `day2.${key}`);
  return typeof en === "string" ? day2Name({ key, name: en }) : key;
}
