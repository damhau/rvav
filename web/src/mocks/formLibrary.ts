// The form library in the mock backend (ADR-0043): versioned value sources
// and validations forms name by key and version. A form gives an entry's
// parameters as CEL over its inputs; the entry reads them as params, as
// the server's libraryBound has it.
import type { components } from "@/lib/api/schema";
import { toText } from "@/lib/utils";
import { celCheck, celEval } from "./cel";
import type { Db } from "./seed";

type S = components["schemas"];
type FieldError = S["FieldError"];
/** One version of an entry, without what is worked out when served. */
export type StoredEntry = Omit<S["FormLibraryEntry"], "versions" | "used_by" | "drafts">;
type Vars = Record<string, unknown>;

const now = () => new Date().toISOString();

export const storedEntry = (key: string, version: number, w: S["FormLibraryEntryWrite"]): StoredEntry => ({
  key,
  version,
  name: w.name,
  description: w.description ?? "",
  use: w.use,
  kind: w.kind,
  params: w.params ?? {},
  search: w.search ?? false,
  parameters: w.parameters ?? [],
  ...(w.message ? { message: w.message } : {}),
  updated_at: now(),
});

/** Every version of every entry, seeded on first use. */
export function libraryOf(db: Db): StoredEntry[] {
  const d = db as Db & { formLibrary?: StoredEntry[] };
  d.formLibrary ??= db.fresh
    ? []
    : [
        storedEntry("cmdbServices", 1, {
          name: "CMDB services",
          description: "The services the CMDB lists in an environment.",
          use: "source",
          kind: "rest",
          params: {
            url: "https://cmdb.corp.example/api/services?env=${params.environment}",
            items: "$.items[*]",
            value: "$.id",
            label: "$.name",
          },
          parameters: [{ name: "environment", label: "Environment", required: true }],
        }),
        storedEntry("costCentreOpen", 1, {
          name: "Cost centre open",
          description: "Refuses a cost centre finance has closed.",
          use: "validation",
          kind: "cel",
          params: {
            check:
              'params.cost_center.startsWith("CC-") ? {} : {"cost_center": "Cost centre " + params.cost_center + " is closed."}',
          },
          parameters: [{ name: "cost_center", label: "Cost centre", required: true }],
        }),
      ];
  return d.formLibrary;
}

/** An entry at a version, its latest when version is 0. */
export function entryAt(db: Db, key: string, version = 0): StoredEntry | undefined {
  const all = libraryOf(db)
    .filter((e) => e.key === key)
    .sort((a, b) => a.version - b.version);
  return version ? all.find((e) => e.version === version) : all.at(-1);
}

export const entryVersions = (db: Db, key: string) =>
  libraryOf(db)
    .filter((e) => e.key === key)
    .map((e) => e.version)
    .sort((a, b) => a - b);

export const libraryKey = /^[A-Za-z][A-Za-z0-9_]{0,62}$/;
const str = (p: Record<string, unknown>, k: string) => (typeof p[k] === "string" ? p[k] : "");

/** A REST call's URL as checked for a form (ns inputs) or an entry (ns params). */
function restProblems(params: Record<string, unknown>, at: string, ns: "inputs" | "params"): FieldError[] {
  const out: FieldError[] = [];
  const url = str(params, "url");
  if (!url) out.push({ field: `${at}/url`, code: "required", message: "Give the URL of the service." });
  else if (!/^https:\/\/cmdb\.corp\.example\//.test(url))
    out.push({
      field: `${at}/url`,
      code: "host",
      message:
        "rvav may not call this host: it is not one of the hosts REST value sources may call (Administration › Integrations › Allowed hosts; cmdb.corp.example here).",
    });
  const other = ns === "inputs" ? "params" : "inputs";
  for (const k of ["url", "body"])
    if (str(params, k).includes("${" + other + "."))
      out.push({
        field: `${at}/${k}`,
        code: "invalid",
        message:
          ns === "params"
            ? "An entry reads what forms give it, its parameters, as ${params.x}: not a form's inputs."
            : "Only a form library entry has parameters: read the form's inputs as ${inputs.x}.",
      });
  return out;
}

function celProblem(expr: string, field: string, ns: "inputs" | "params"): FieldError[] {
  const problem = celCheck(expr);
  if (problem) return [{ field, code: "invalid", message: `This expression is not valid CEL: ${problem}` }];
  if (ns === "params" && /\binputs\b/.test(expr))
    return [
      { field, code: "invalid", message: "An entry reads its parameters, as params.<name>: not a form's inputs." },
    ];
  return [];
}

/** A validation's parameters, as the server's checkCheckParams has them. */
export function checkProblems(
  kind: S["ValueSourceKind"],
  params: Record<string, unknown>,
  at: string,
  ns: "inputs" | "params",
): FieldError[] {
  if (kind === "rest") return restProblems(params, at, ns);
  if (kind !== "cel") return [];
  const check = str(params, "check");
  if (!check.trim())
    return [
      {
        field: `${at}/check`,
        code: "required",
        message: "Give the check's expression: true when the form passes, or a message.",
      },
    ];
  return celProblem(check, `${at}/check`, ns);
}

/** An entry's problems, as the server's checkEntry finds them. */
export function entryProblems(w: S["FormLibraryEntryWrite"], prefix = ""): FieldError[] {
  const out: FieldError[] = [];
  const add = (field: string, code: string, message: string) => out.push({ field: prefix + field, code, message });
  if (!w.name?.trim()) add("/name", "required", "Give the entry a name.");
  if (w.use !== "source" && w.use !== "validation") add("/use", "invalid", "An entry is a source or a validation.");
  if (w.kind !== "rest" && w.kind !== "cel") {
    add("/kind", "invalid", "An entry calls a REST service (rest) or works it out in CEL (cel).");
    return out;
  }
  const names = new Set<string>();
  (w.parameters ?? []).forEach((p, i) => {
    const at = `/parameters/${i}`;
    if (!libraryKey.test(p.name))
      add(
        `${at}/name`,
        "pattern",
        "Letters, digits and underscores, starting with a letter: the entry reads it as params.<name>.",
      );
    else if (names.has(p.name)) add(`${at}/name`, "duplicate", `The entry has ${p.name} twice.`);
    names.add(p.name);
    if (!p.label?.trim()) add(`${at}/label`, "required", "Give the parameter a label.");
  });
  const params = w.params ?? {};
  if (w.use === "validation") {
    if (w.search) add("/search", "invalid", "A validation searches nothing: leave search out.");
    return [...out, ...checkProblems(w.kind, params, `${prefix}/params`, "params")];
  }
  if (w.message) add("/message", "invalid", "Only a validation has a message.");
  if (w.kind === "rest") return [...out, ...restProblems(params, `${prefix}/params`, "params")];
  const options = str(params, "options");
  const value = str(params, "value");
  if (!options && !value) add("/params", "required", "Give an `options` or a `value` expression.");
  for (const [k, e] of [
    ["options", options],
    ["value", value],
  ] as const)
    if (e) out.push(...celProblem(e, `${prefix}/params/${k}`, "params"));
  return out;
}

/** The library references of a form's fields and validations: where, what, and as what it uses them. */
export function libraryRefs(ui: S["UiSchema"], prefix: string) {
  const out: {
    at: string;
    key: string;
    version: number;
    use: S["FormLibraryPurpose"];
    params: Record<string, unknown>;
  }[] = [];
  for (const [name, f] of Object.entries(ui.fields).sort(([a], [b]) => a.localeCompare(b))) {
    const src = f.value_source;
    if (src?.kind === "library")
      out.push({
        at: `${prefix}/ui_schema/fields/${name}/value_source`,
        key: src.name ?? "",
        version: src.version ?? 0,
        use: "source",
        params: src.params ?? {},
      });
  }
  (ui.validations ?? []).forEach((v, i) => {
    if (v.kind === "library")
      out.push({
        at: `${prefix}/ui_schema/validations/${i}`,
        key: v.name ?? "",
        version: v.version ?? 0,
        use: "validation",
        params: v.params ?? {},
      });
  });
  return out;
}

/** The library references a draft makes that the library can't answer, as the server's checkLibraryRefs. */
export function libraryRefProblems(db: Db, ui: S["UiSchema"], prefix: string, publish: boolean): FieldError[] {
  const out: FieldError[] = [];
  for (const r of libraryRefs(ui, prefix)) {
    if (!r.key) {
      out.push({ field: `${r.at}/name`, code: "required", message: "Name the form library entry." });
      continue;
    }
    const latest = entryAt(db, r.key);
    if (!latest) {
      out.push({ field: `${r.at}/name`, code: "unknown", message: `The form library has no entry "${r.key}".` });
      continue;
    }
    const e = entryAt(db, r.key, r.version);
    if (!e) {
      out.push({
        field: `${r.at}/version`,
        code: "unknown",
        message: `Entry ${r.key} has no version ${r.version}: its latest is ${latest.version}.`,
      });
      continue;
    }
    if (e.use !== r.use)
      out.push({ field: `${r.at}/name`, code: "invalid", message: `Entry ${r.key} is a ${e.use}, not a ${r.use}.` });
    for (const k of Object.keys(r.params).sort()) {
      if (!e.parameters.some((p) => p.name === k))
        out.push({ field: `${r.at}/params/${k}`, code: "unknown", message: `Entry ${r.key} has no parameter ${k}.` });
      else if (typeof r.params[k] === "string" && r.params[k] && celCheck(r.params[k]))
        out.push({
          field: `${r.at}/params/${k}`,
          code: "invalid",
          message: `This expression is not valid CEL: ${celCheck(r.params[k])}`,
        });
    }
    if (publish)
      for (const p of e.parameters)
        if (p.required && !(typeof r.params[p.name] === "string" && String(r.params[p.name]).trim()))
          out.push({
            field: `${r.at}/params/${p.name}`,
            code: "required",
            message: `Entry ${r.key} needs ${p.label} (${p.name}): give it an expression.`,
          });
  }
  return out;
}

/**
 * An entry and its parameters' values, from the form's bindings over vars; null when a required
 * parameter has none, so the entry doesn't run. A binding that fails leaves its parameter out.
 */
export function bound(
  db: Db,
  key: string,
  version: number,
  bindings: Record<string, unknown>,
  vars: Vars,
): { entry: StoredEntry; params: Record<string, unknown> } | null {
  const entry = entryAt(db, key, version);
  if (!entry) return null;
  const params: Record<string, unknown> = {};
  for (const p of entry.parameters) {
    const expr = bindings[p.name];
    let v: unknown;
    try {
      v = typeof expr === "string" && expr.trim() ? celEval(expr, vars) : undefined;
    } catch {
      v = undefined;
    }
    if (v !== undefined && v !== null && v !== "") params[p.name] = v;
    else if (p.required) return null;
  }
  return { entry, params };
}

/** What an entry's expressions read: the parameters, and what forms read but the inputs. */
export function entryVars(params: Record<string, unknown>, vars: Vars): Vars {
  const { inputs: _inputs, ...rest } = vars;
  return { ...rest, params };
}

/** A template's ${ns.x} filled from values. */
export const fillTemplate = (tpl: string, ns: string, values: Record<string, unknown>) =>
  tpl.replace(new RegExp(`\\$\\{${ns}\\.([A-Za-z0-9_]+)\\}`, "g"), (_, k: string) =>
    encodeURIComponent(toText(values[k] ?? "")),
  );

interface Message {
  field?: string;
  message: string;
}

/** A check's verdict: its messages, or why it couldn't run. */
type Verdict = { messages: Message[] } | { failure: string };

/** A CEL check's result, as the server's celCheck reads it. */
function celVerdict(expr: string, vars: Vars): Verdict {
  let v: unknown;
  try {
    v = celEval(expr, vars, true);
  } catch (e) {
    return { failure: String(e instanceof Error ? e.message : e) };
  }
  if (v === null || v === undefined || v === true || v === "") return { messages: [] };
  if (v === false) return { messages: [{ message: "" }] };
  if (typeof v === "string") return { messages: [{ message: v }] };
  if (Array.isArray(v))
    return {
      messages: (v as unknown[])
        .filter((m): m is string => typeof m === "string" && m !== "")
        .map((m) => ({ message: m })),
    };
  if (typeof v === "object")
    return {
      messages: Object.entries(v as Record<string, unknown>)
        .filter(([, m]) => typeof m === "string" && m.trim())
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([field, m]) => ({ field, message: String(m) })),
    };
  return { failure: `it gives ${typeof v}: give true, false, a message, or a map of messages` };
}

/**
 * The mocks' REST validation: the CMDB refuses a body or URL that says "refuse", and
 * answers nothing (as a host that is down) to one that says "down".
 */
function restVerdict(params: Record<string, unknown>, filled: string): Verdict {
  const url = str(params, "url");
  const host = /^https?:\/\/([^/]+)/.exec(url)?.[1] ?? "the service";
  if (/down/i.test(filled)) return { failure: `REST validation ${host}: the service did not answer` };
  if (/refuse/i.test(filled)) return { messages: [{ message: `${host} refused the request.` }] };
  return { messages: [] };
}

function runCheck(
  kind: S["ValueSourceKind"],
  params: Record<string, unknown>,
  vars: Vars,
  ns: "inputs" | "params",
): Verdict {
  if (kind === "cel") return celVerdict(str(params, "check"), vars);
  const values = (vars[ns] ?? {}) as Record<string, unknown>;
  return restVerdict(params, fillTemplate(str(params, "url") + " " + str(params, "body"), ns, values));
}

/** What an entry's test gives: a validation's verdict, or a source's options and value. */
export function testEntry(t: S["FormLibraryTest"], vars: Vars): S["FormLibraryTestResult"] | { failure: string } {
  const e = t.entry;
  const v = { ...vars, params: t.params, search: t.search ?? "" };
  if (e.use === "validation") {
    const verdict = runCheck(e.kind, e.params, v, "params");
    if ("failure" in verdict) return verdict;
    return {
      valid: verdict.messages.length === 0,
      messages: verdict.messages.map((m) => ({
        ...(m.field ? { field: m.field } : {}),
        message: m.message || e.message || `${e.name} refused the request.`,
      })),
    };
  }
  if (e.kind === "rest") {
    const host = /^https?:\/\/([^/]+)/.exec(str(e.params, "url"))?.[1] ?? "the service";
    return {
      options: [
        { value: "svc-01", label: "Service 01", meta: `from ${host}` },
        { value: "svc-02", label: "Service 02", meta: `from ${host}` },
      ],
    };
  }
  try {
    const list = str(e.params, "options") ? celEval(str(e.params, "options"), v) : undefined;
    const value = str(e.params, "value") ? celEval(str(e.params, "value"), v) : undefined;
    return {
      ...(Array.isArray(list)
        ? { options: list.map((x) => (typeof x === "string" ? { value: x, label: x } : (x as S["Option"]))) }
        : {}),
      ...(value !== undefined ? { value } : {}),
    };
  } catch (err) {
    return { failure: String(err instanceof Error ? err.message : err) };
  }
}

const inputAlone = /^\s*inputs(?:\.([A-Za-z_][A-Za-z0-9_]*)|\[\s*"([^"]+)"\s*\])\s*$/;

/** The input a parameter's binding reads alone, if any. */
function boundInput(bindings: Record<string, unknown>, param: string): string {
  const expr = bindings[param];
  const m = typeof expr === "string" ? inputAlone.exec(expr) : null;
  return m ? (m[1] ?? m[2] ?? "") : "";
}

/**
 * The form's validations' messages, as the server's evaluateValidations gives them: on the
 * inputs they name, else on the fields the validation names, else on the validation.
 */
export function validationErrors(db: Db, form: S["RequestForm"] | undefined, vars: Vars): FieldError[] {
  const list = form?.ui_schema.validations ?? [];
  const props = (form?.schema as { properties?: Record<string, unknown> } | undefined)?.properties ?? {};
  const out: FieldError[] = [];
  for (const v of list) {
    const title = v.title || v.key;
    let message = v.message ?? "";
    let verdict: Verdict;
    const bindings = v.params ?? {};
    if (v.kind === "library") {
      const b = bound(db, v.name ?? "", v.version ?? 0, bindings, vars);
      if (!b) continue;
      message ||= b.entry.message ?? "";
      verdict = runCheck(b.entry.kind, b.entry.params, entryVars(b.params, vars), "params");
      if ("messages" in verdict)
        verdict.messages = verdict.messages.map((m) => ({
          ...m,
          field: (m.field && boundInput(bindings, m.field)) || m.field,
        }));
    } else verdict = runCheck(v.kind, v.params ?? {}, vars, "inputs");
    const at = `/validations/${v.key}`;
    if ("failure" in verdict) {
      out.push({
        field: at,
        code: "validation_failed",
        message: `${title} couldn't check the request (${verdict.failure}), so it can't be submitted: try again, and if it keeps failing, tell the catalog's administrators.`,
      });
      continue;
    }
    for (const m of verdict.messages) {
      let text = m.message || message || `${title} refused the request.`;
      if (m.field && m.field in props) {
        out.push({ field: `/inputs/${m.field}`, code: "refused", message: text });
        continue;
      }
      if (m.field) text = `${m.field}: ${text}`;
      const on = (v.fields ?? []).filter((f) => f in props);
      if (on.length) for (const f of on) out.push({ field: `/inputs/${f}`, code: "refused", message: text });
      else out.push({ field: at, code: "refused", message: text });
    }
  }
  return out;
}
