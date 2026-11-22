import type { CatalogItem, CatalogItemType } from "@/lib/api/types";
import { i18n } from "@/lib/i18n";

export const catalogKinds: CatalogItemType[] = [
  "vm_blueprint",
  "composite",
  "awx_job_template",
  "awx_workflow",
  "workflow",
];

/** What a card says of the approval an item needs, kept short. */
export const approvalHint = (a: CatalogItem["approval"]) => i18n.t(`catalog:approval.hint.${a}`);

/** The same, as a sentence on the item's page. */
export const approvalText = (a: CatalogItem["approval"]) => i18n.t(`catalog:approval.text.${a}`);

/** `v3.2`, or AWX's own `AWX #47`. */
export const versionLabel = (item: Pick<CatalogItem, "version">) =>
  item.version.number.startsWith("AWX") ? item.version.number : `v${item.version.number}`;
