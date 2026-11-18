import type { CatalogItem, CatalogItemType } from "@/lib/api/types";

export const catalogKinds: CatalogItemType[] = [
  "vm_blueprint",
  "composite",
  "awx_job_template",
  "awx_workflow",
  "workflow",
];

/** What a card says of the approval an item needs, kept short. */
export const approvalHint = {
  never: "No approval",
  conditional: "Approval in production",
  always: "Needs approval",
} as const;

/** The same, as a sentence on the item's page. */
export const approvalText = {
  never: "No approval needed",
  conditional: "Needs approval in production",
  always: "Always needs approval",
} as const;

/** `v3.2`, or AWX's own `AWX #47`. */
export const versionLabel = (item: Pick<CatalogItem, "version">) =>
  item.version.number.startsWith("AWX") ? item.version.number : `v${item.version.number}`;
