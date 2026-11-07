// A composite blueprint's steps, read from the definition (ADR-0022): the
// groups they run in, what each runs and which earlier steps it uses, with
// the server's structural checks (CEL itself is checked when saved; a REST
// call's host and secret too, with the lists only the server has).
import type { FieldError } from "@/lib/api/types";

export interface CompositeStepView {
  key: string;
  title?: string;
  kind: "item" | "awx" | "workflow" | "rest" | "?";
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

/** A piece of a REST call's URL template: text as written, or a CEL expression (`${...}`). */
export type UrlPart = { text: string } | { expr: string };

/** Where the expression opening a text ends: the index of its `}`, past the braces and strings inside it. */
function exprEnd(s: string): number {
  let depth = 0;
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i]!;
    if (quote) {
      if (c === "\\") i++;
      else if (c === quote) quote = "";
    } else if (c === "'" || c === '"') quote = c;
    else if (c === "{") depth++;
    else if (c === "}") {
      if (depth === 0) return i;
      depth--;
    }
  }
  return -1;
}

/** A URL template's parts, as the server reads them (restcall.ParseURL), or what is wrong with it. */
export function parseUrl(template: string): { parts: UrlPart[]; error?: string } {
  const parts: UrlPart[] = [];
  let rest = template;
  for (;;) {
    const i = rest.indexOf("${");
    if (i < 0) {
      if (rest) parts.push({ text: rest });
      return { parts };
    }
    if (i > 0) parts.push({ text: rest.slice(0, i) });
    const end = exprEnd(rest.slice(i + 2));
    if (end < 0) return { parts: [], error: "a ${ isn't closed" };
    const expr = rest.slice(i + 2, i + 2 + end).trim();
    if (!expr) return { parts: [], error: "a ${} is empty" };
    parts.push({ expr });
    rest = rest.slice(i + 2 + end + 1);
  }
}

const urlExpressions = (template: string) => parseUrl(template).parts.flatMap((p) => ("expr" in p ? [p.expr] : []));

/** The CEL of a REST call: its URL's expressions and its body. */
function restExpressions(call: Raw | undefined): string[] {
  if (!call) return [];
  return [...urlExpressions(str(call.url)), ...(str(call.body) ? [str(call.body)] : [])];
}

/** What the server checks of a REST call on its own (core.checkRESTCall). */
function checkRest(call: Raw, ptr: string, err: (field: string, code: string, message: string) => void) {
  if (!["GET", "POST", "PUT", "PATCH", "DELETE"].includes(str(call.method)))
    err(`${ptr}/method`, "invalid", "GET, POST, PUT, PATCH or DELETE.");
  const url = str(call.url);
  const { parts, error } = parseUrl(url);
  // Its scheme and host, whatever the expressions give.
  const skeleton = parts.map((p) => ("expr" in p ? "x" : p.text)).join("");
  let parsed: URL | null = null;
  try {
    parsed = new URL(skeleton);
  } catch {
    // Said below.
  }
  const scheme = url.indexOf("://");
  const firstExpr = url.indexOf("${");
  const afterHost = url.slice(scheme + 3).search(/[/?#]/);
  if (!url) err(`${ptr}/url`, "required", "Give the URL to call.");
  else if (error) err(`${ptr}/url`, "invalid", `This URL can't be read: ${error}.`);
  else if (!parsed || !["http:", "https:"].includes(parsed.protocol) || !parsed.host)
    err(`${ptr}/url`, "invalid", "Give an http or https URL.");
  else if (firstExpr >= 0 && (afterHost < 0 || firstExpr < scheme + 3 + afterHost))
    err(`${ptr}/url`, "invalid", "Expressions may fill the URL's path and query, not its scheme, host or port.");
  else if (parsed.username || parsed.password)
    err(`${ptr}/url`, "invalid", "Put credentials in a stored secret, not in the URL.");
  if (str(call.body) && call.method === "GET") err(`${ptr}/body`, "invalid", "A GET has no body.");
  const accept = Array.isArray(call.accept) ? call.accept : [];
  accept.forEach((status, i) => {
    if (typeof status !== "number" || status < 100 || status > 599)
      err(`${ptr}/accept/${i}`, "invalid", "An HTTP status, 100 to 599.");
  });
}

/** A REST call on one line. */
const restLine = (call: Raw) => `${str(call.method)} ${str(call.url)}`.trim();

function expressions(s: Raw): string[] {
  const out: string[] = [];
  if (str(s.when)) out.push(str(s.when));
  for (const v of Object.values((s.with as Raw | undefined) ?? {})) if (typeof v === "string") out.push(v);
  out.push(...restExpressions(s.rest as Raw | undefined));
  return out;
}

/** The CEL of a step's undo: it runs after the step, so it may read its outputs too. */
function undoExpressions(s: Raw): string[] {
  const undo = s.undo as Raw | undefined;
  const out: string[] = [];
  for (const v of Object.values((undo?.with as Raw | undefined) ?? {})) if (typeof v === "string") out.push(v);
  out.push(...restExpressions(undo?.rest as Raw | undefined));
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
    if (parallel && (st.key || st.item || st.awx || st.workflow || st.rest))
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
      const rest = s.rest as Raw | undefined;
      const kinds = [Boolean(s.item), Boolean(awx), Boolean(s.workflow), Boolean(rest)].filter(Boolean).length;
      if (kinds !== 1) err(sp, "one_of", "A step is one of: item, awx, workflow or rest.");
      if (awx && !str(awx.job_template)) err(`${sp}/awx/job_template`, "required", "Name the AWX template.");
      if (rest) {
        checkRest(rest, `${sp}/rest`, err);
        if (s.with !== undefined)
          err(`${sp}/with`, "invalid", "A REST call has no inputs: its URL and body read inputs and steps themselves.");
      }
      const undo = s.undo as Raw | undefined;
      const undoRest = undo?.rest as Raw | undefined;
      if (undo && [str(undo.job_template), str(undo.workflow), undoRest].filter(Boolean).length !== 1)
        err(`${sp}/undo`, "one_of", "Undo with a job_template, a workflow or a rest call.");
      if (undoRest) {
        checkRest(undoRest, `${sp}/undo/rest`, err);
        if (undo?.with !== undefined)
          err(
            `${sp}/undo/with`,
            "invalid",
            "A REST call has no inputs: its URL and body read inputs and steps themselves.",
          );
      }
      const uses = new Set<string>();
      const read = (e: string, own: string | null) => {
        for (const m of e.matchAll(stepRef)) {
          const ref = m[1]!;
          if (ref === own) continue;
          uses.add(ref);
          const gr = seen.get(ref);
          if (gr === undefined || gr >= g) err(sp, "unknown_step", `steps.${ref} is not an earlier step.`);
        }
      };
      for (const e of expressions(s)) read(e, null);
      for (const e of undoExpressions(s)) read(e, key);
      group.push({
        key,
        title: str(s.title) || str(awx?.title) || undefined,
        kind: s.item ? "item" : awx ? "awx" : s.workflow ? "workflow" : rest ? "rest" : "?",
        target: str(s.item) || str(awx?.job_template) || str(s.workflow) || (rest ? restLine(rest) : ""),
        when: str(s.when) || undefined,
        uses: [...uses],
        undo: undo ? str(undo.job_template) || str(undo.workflow) || (undoRest ? restLine(undoRest) : "") : undefined,
        pointer: sp,
      });
      if (key) seen.set(key, g);
    });
    groups.push(group);
  });
  return { groups, problems };
}
