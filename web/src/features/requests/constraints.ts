// What a component's constraint tags chose or ruled out (ADR-0035), in the
// words the request form's summary uses.
import type { PlacementConstraint } from "@/lib/api/types";

const nouns = { compute: "cluster", storage: "datastore", network: "network" } as const;

const noun = (k: PlacementConstraint) => (k.disk !== undefined ? `disk ${k.disk + 1}'s datastore` : nouns[k.kind]);

/** One line for a constraint: "rvav-env/prod: cluster GVA-PRD-CL01, ruled out GVA-DEV-CL01". */
export function constraintText(k: PlacementConstraint): string {
  const label = `${k.negated ? "not " : ""}${k.tag}${k.soft ? " (preferred)" : ""}`;
  if (!k.object) return `${label}: no ${noun(k)} allowed meets it`;
  if (!k.met) return `${label}: ${k.soft ? "let go, " : ""}${noun(k)} ${k.object} doesn't meet it`;
  const others = k.excluded.length > 0 ? `, ${k.soft ? "passed over" : "ruled out"} ${k.excluded.join(", ")}` : "";
  return `${label}: ${noun(k)} ${k.object}${others}`;
}
