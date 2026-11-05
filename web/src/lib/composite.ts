// A composite blueprint's steps, read from the definition (ADR-0022): the
// groups they run in, what each runs and which earlier steps it uses, with
// the server's structural checks (CEL itself is checked when saved).
import type { FieldError } from "@/lib/api/types";

export interface CompositeStepView {
  key: string;
  title?: string;
  kind: "item" | "awx" | "workflow" | "?";
  target: string;
  when?: string;
  uses: string[];
  undo?: string;
  pointer: string;
}

export interface CompositeView {
  groups: CompositeStepView[][];
  problems: FieldError[];
}

type Raw = Record<string, unknown>;

const keyPattern = /^[a-z][a-z0-9_]{0,40}$/;
const stepRef = /\bsteps\.([a-z][a-z0-9_]*)/g;

const str = (v: unknown) => (typeof v === "string" ? v : "");

function expressions(s: Raw): string[] {
  const out: string[] = [];
  if (str(s.when)) out.push(str(s.when));
  for (const v of Object.values((s.with as Raw | undefined) ?? {})) if (typeof v === "string") out.push(v);
  const undo = s.undo as Raw | undefined;
  for (const v of Object.values((undo?.with as Raw | undefined) ?? {})) if (typeof v === "string") out.push(v);
  return out;
}

/** The steps of a composite blueprint and what is wrong with them (pointers under prefix). */
export function readComposite(blueprint: Raw, prefix = "/definition/blueprint"): CompositeView {
  const problems: FieldError[] = [];
  const err = (field: string, code: string, message: string) => problems.push({ field, code, message });
  const steps = Array.isArray(blueprint.steps) ? (blueprint.steps as Raw[]) : [];
  const seen = new Map<string, number>();
  const groups: CompositeStepView[][] = [];
  steps.forEach((st, g) => {
    const at = `${prefix}/steps/${g}`;
    const parallel = Array.isArray(st.parallel) ? (st.parallel as Raw[]) : null;
    if (parallel && (st.key || st.item || st.awx || st.workflow))
      err(at, "invalid", "A parallel group only lists steps.");
    const list = parallel ?? [st];
    const group: CompositeStepView[] = [];
    list.forEach((s, j) => {
      const sp = parallel ? `${at}/parallel/${j}` : at;
      if (parallel && Array.isArray(s.parallel)) {
        err(sp, "invalid", "Parallel groups don't nest.");
        return;
      }
      const key = str(s.key);
      if (!keyPattern.test(key)) err(`${sp}/key`, "pattern", "A key of lower-case letters, digits and underscores.");
      else if (seen.has(key)) err(`${sp}/key`, "duplicate", `Another step has the key ${key}.`);
      const awx = s.awx as Raw | undefined;
      const kinds = [Boolean(s.item), Boolean(awx), Boolean(s.workflow)].filter(Boolean).length;
      if (kinds !== 1) err(sp, "one_of", "A step is one of: item, awx or workflow.");
      if (awx && !str(awx.job_template)) err(`${sp}/awx/job_template`, "required", "Name the AWX template.");
      const undo = s.undo as Raw | undefined;
      if (undo && Boolean(str(undo.job_template)) === Boolean(str(undo.workflow)))
        err(`${sp}/undo`, "one_of", "Undo with a job_template or a workflow.");
      const uses = new Set<string>();
      for (const e of expressions(s)) {
        for (const m of e.matchAll(stepRef)) {
          const ref = m[1]!;
          uses.add(ref);
          const gr = seen.get(ref);
          if (gr === undefined || gr >= g) err(sp, "unknown_step", `steps.${ref} is not an earlier step.`);
        }
      }
      group.push({
        key,
        title: str(s.title) || str(awx?.title) || undefined,
        kind: s.item ? "item" : awx ? "awx" : s.workflow ? "workflow" : "?",
        target: str(s.item) || str(awx?.job_template) || str(s.workflow),
        when: str(s.when) || undefined,
        uses: [...uses],
        undo: undo ? str(undo.job_template) || str(undo.workflow) : undefined,
        pointer: sp,
      });
      if (key) seen.set(key, g);
    });
    groups.push(group);
  });
  return { groups, problems };
}
