// What a component's constraint tags chose or ruled out (ADR-0035), in the
// words the request form's summary uses.
import type { PlacementConstraint } from "@/lib/api/types";
import { i18n } from "@/lib/i18n";

/** One line for a constraint: "rvav-env/prod: cluster GVA-PRD-CL01, ruled out GVA-DEV-CL01". */
export function constraintText(k: PlacementConstraint): string {
  const t = i18n.getFixedT(null, "requests");
  const tag = k.negated ? t("constraint.not", { tag: k.tag }) : k.tag;
  const label = k.soft ? t("constraint.preferred", { label: tag }) : tag;
  const noun = k.disk !== undefined ? t("constraint.diskDatastore", { n: k.disk + 1 }) : t(`constraint.noun.${k.kind}`);
  if (!k.object) return t("constraint.noneMeets", { label, noun });
  const at = { label, noun, object: k.object };
  if (!k.met) return t(k.soft ? "constraint.letGo" : "constraint.notMet", at);
  if (k.excluded.length === 0) return t("constraint.met", at);
  return t(k.soft ? "constraint.passedOver" : "constraint.ruledOut", { ...at, excluded: k.excluded.join(", ") });
}
