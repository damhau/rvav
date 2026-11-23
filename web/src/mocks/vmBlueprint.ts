// A VM blueprint's checks on save, as the server's checkVMBlueprint
// (internal/core/blueprint_check.go): its CEL compiled against the variables
// a request has, its keys, counts, parts, groups and what it is built after.
// Of the checks only publish makes, the VMs' CPU and memory are here
// (vmSizeProblems); a template and an OS family are left out. A binding
// written as a number or a boolean is that constant, as the server's
// blueprint.Bindings reads it; a list or keys and values don't parse, and
// that is all the server says (parseProblem).

import type { components } from "@/lib/api/schema";
import { buildVariables, celRoots, formVariables, inputsRead } from "@/lib/celReads";
import { celCheck } from "./cel";

type FieldError = components["schemas"]["FieldError"];
type Rec = Record<string, unknown>;

const componentKey = /^[a-z][a-z0-9]{0,15}$/;
const nsxID = /^[A-Za-z0-9_.-]{1,255}$/;
const maxSegments = 4;
const maxSecurityGroups = 8;
const maxVMs = 20;
const singleKey = "vm";

const rec = (v: unknown): Rec => (v && typeof v === "object" && !Array.isArray(v) ? (v as Rec) : {});
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const text = (v: unknown): string => (typeof v === "string" ? v : "");
const bound = (v: unknown): string => (typeof v === "number" || typeof v === "boolean" ? String(v) : text(v));
const words = (v: unknown): string =>
  Array.isArray(v)
    ? "a list"
    : v === null
      ? "null"
      : typeof v === "object"
        ? "keys and values"
        : typeof v === "string"
          ? "text"
          : typeof v === "boolean"
            ? "true or false"
            : "a number";

/** The one problem the server's blueprint.Parse finds in a VM blueprint's bindings, if any. */
function bindingsShape(bp: Rec, ptr: string): FieldError | null {
  const owners: [string, unknown][] = [
    [ptr, bp.bindings],
    ...list(bp.components).map((c, i): [string, unknown] => [`${ptr}/components/${i}`, rec(c).bindings]),
  ];
  for (const [at, m] of owners) {
    if (m === undefined || m === null) continue;
    if (typeof m !== "object" || Array.isArray(m))
      return {
        field: `${at}/bindings`,
        code: "invalid",
        message: `This is ${words(m)}, where the blueprint takes bindings by name, as {memory_gb: "4"}.`,
      };
    for (const [k, v] of Object.entries(m as Rec))
      if (v && typeof v === "object")
        return {
          field: `${at}/bindings/${k}`,
          code: "invalid",
          message: `This is ${words(v)}, where the blueprint takes CEL in quotes ("inputs.network") or a number.`,
        };
  }
  return null;
}

/** What cel-go says of an expression against these variables, or null when it compiles. */
function compile(expr: string, known: ReadonlySet<string>): string | null {
  const syntax = celCheck(expr);
  if (syntax) return syntax;
  const name = celRoots(expr).find((n) => !known.has(n));
  if (!name) return null;
  const at = new RegExp(`(?<![\\w.])${name}(?!\\w)`).exec(expr)?.index ?? 0;
  return `compile ${JSON.stringify(expr)}: ERROR: <input>:1:${at + 1}: undeclared reference to '${name}' (in container '')`;
}

/** The problems of a VM blueprint (kind vm, or none) at ptr, as the server finds them on save. */
export function vmBlueprintProblems(bp: Rec, ptr: string): FieldError[] {
  const shape = bindingsShape(bp, ptr);
  if (shape) return [shape];
  const errs: FieldError[] = [];
  const add = (field: string, code: string, message: string) => errs.push({ field, code, message });
  const cel = (at: string, expr: string, what: string, hint = "") => {
    const problem = compile(expr, formVariables);
    if (problem) add(at, "invalid", `${what} is not valid CEL: ${problem}${hint}`);
  };
  const bindings = (at: string, m: unknown) => {
    for (const [k, v] of Object.entries(rec(m)).sort(([a], [b]) => a.localeCompare(b))) {
      if (v === null) continue;
      const expr = bound(v);
      const roots = celRoots(expr);
      const built = !celCheck(expr) && (roots.includes("self") || roots.includes("components"));
      if (!built) cel(`${at}/${k}`, expr, "This binding");
      else if (k !== "tags")
        add(
          `${at}/${k}`,
          "invalid",
          "Only the tags binding is worked out as each VM is built: this one can't read self or components.",
        );
      else {
        const problem = compile(expr, buildVariables);
        if (problem)
          add(
            `${at}/${k}`,
            "invalid",
            `This binding is not valid CEL: ${problem} (worked out as each VM is cloned, it reads inputs, self and components).`,
          );
      }
    }
  };
  const parts = (at: string, owner: Rec) => {
    const diskKeys = new Set<string>();
    list(owner.disks).forEach((raw, i) => {
      const d = rec(raw);
      const dat = `${at}/disks/${i}`;
      const key = text(d.key);
      if (key && !componentKey.test(key))
        add(`${dat}/key`, "pattern", "A key of up to 16 lower-case letters and digits, starting with a letter.");
      else if (key && diskKeys.has(key)) add(`${dat}/key`, "duplicate", `Another disk has the key ${key}.`);
      diskKeys.add(key);
      if (typeof d.size_gb === "number") {
        if (!Number.isInteger(d.size_gb) || d.size_gb < 1 || d.size_gb > 65536)
          add(`${dat}/size_gb`, "out_of_range", "A size from 1 to 65536 GB.");
      } else if (typeof d.size_gb === "string") cel(`${dat}/size_gb`, d.size_gb, "The size");
      else add(`${dat}/size_gb`, "required", "Give the disk's size in GB, or CEL over the inputs.");
      if (text(d.datastore))
        cel(`${dat}/datastore`, text(d.datastore), "The datastore", ` (a name is quoted: '"vsan-01"').`);
    });
    const nicKeys = new Set<string>();
    list(owner.nics).forEach((raw, i) => {
      const n = rec(raw);
      const nat = `${at}/nics/${i}`;
      const key = text(n.key) || `nic${i + 2}`;
      if (!componentKey.test(key))
        add(
          `${nat}/key`,
          "pattern",
          "A key of up to 16 lower-case letters and digits, starting with a letter: it goes into DNS names.",
        );
      else if (nicKeys.has(key)) add(`${nat}/key`, "duplicate", `Another adapter has the key ${key}.`);
      nicKeys.add(key);
      const network = text(n.network);
      if (!network.trim())
        add(
          `${nat}/network`,
          "required",
          "Give the adapter's network, as CEL (inputs.backup_network, or a quoted name).",
        );
      else cel(`${nat}/network`, network, "The network", ` (a name is quoted: '"dvpg-backup-vl900"').`);
    });
    list(owner.tags).forEach((t, i) => {
      const [cat = "", tag, extra] = text(t).split("/");
      if (!cat.trim() || !tag?.trim() || extra !== undefined)
        add(`${at}/tags/${i}`, "invalid", "A vSphere tag as category/tag, for example backup/daily.");
    });
  };

  bindings(`${ptr}/bindings`, bp.bindings);
  parts(ptr, bp);

  const segments = list(bp.segments).map(rec);
  if (segments.length > maxSegments)
    add(`${ptr}/segments`, "out_of_range", `At most ${maxSegments} segments for a deployment.`);
  const segKeys = new Set<string>();
  segments.forEach((s, i) => {
    const key = text(s.key);
    if (!componentKey.test(key))
      add(
        `${ptr}/segments/${i}/key`,
        "pattern",
        "A key of up to 16 lower-case letters and digits, starting with a letter: it goes into the segment's name.",
      );
    else if (segKeys.has(key)) add(`${ptr}/segments/${i}/key`, "duplicate", `Another segment has the key ${key}.`);
    segKeys.add(key);
  });

  const groups = list(bp.security_groups).map(rec);
  if (groups.length > maxSecurityGroups)
    add(`${ptr}/security_groups`, "out_of_range", `At most ${maxSecurityGroups} groups for a deployment.`);
  const groupKeys = new Set<string>();
  groups.forEach((g, i) => {
    const key = text(g.key);
    if (!componentKey.test(key))
      add(
        `${ptr}/security_groups/${i}/key`,
        "pattern",
        "A key of up to 16 lower-case letters and digits, starting with a letter: it goes into the group's name.",
      );
    else if (groupKeys.has(key))
      add(`${ptr}/security_groups/${i}/key`, "duplicate", `Another group has the key ${key}.`);
    groupKeys.add(key);
  });
  const group = (at: string, name: string, anyOK: boolean) => {
    if (name.startsWith("nsx:")) {
      const id = name.slice(4);
      if (!nsxID.test(id)) add(at, "pattern", `${JSON.stringify(id)} is not an NSX group ID.`);
      return;
    }
    if (anyOK && name === "any") return;
    if (!groupKeys.has(name))
      add(
        at,
        "unknown",
        `${JSON.stringify(name)} is none of the item's security groups (or nsx:<group ID> for an existing one).`,
      );
  };
  groups.forEach((g, i) =>
    list(g.rules).forEach((raw, j) => {
      const r = rec(raw);
      const at = `${ptr}/security_groups/${i}/rules/${j}`;
      const from = list(r.from).map(text);
      if (!from.length)
        add(`${at}/from`, "required", "Say where the traffic comes from: any, a group, or nsx:<group ID>.");
      from.forEach((f, k) => group(`${at}/from/${k}`, f, true));
      const services = list(r.services).map(text);
      if (!services.length) add(`${at}/services`, "required", "Name NSX services (HTTPS, SSH), or any.");
      services.forEach((s, k) => {
        if (s !== "any" && !nsxID.test(s))
          add(`${at}/services/${k}`, "pattern", `${JSON.stringify(s)} is not an NSX service ID.`);
      });
      if (!["", "allow", "drop", "reject"].includes(text(r.action)))
        add(`${at}/action`, "invalid", "allow, drop or reject.");
    }),
  );
  list(bp.groups).forEach((g, i) => group(`${ptr}/groups/${i}`, text(g), false));

  const comps = list(bp.components).map(rec);
  comps.forEach((c, i) =>
    list(c.groups).forEach((g, j) => group(`${ptr}/components/${i}/groups/${j}`, text(g), false)),
  );
  const keys = new Set<string>();
  let total = 0;
  comps.forEach((c, i) => {
    const at = `${ptr}/components/${i}`;
    const key = text(c.key);
    if (!componentKey.test(key))
      add(
        `${at}/key`,
        "pattern",
        "A key of up to 16 lower-case letters and digits, starting with a letter: it goes into VM names.",
      );
    else if (keys.has(key)) add(`${at}/key`, "duplicate", `Another component has the key ${key}.`);
    else if (key === singleKey)
      add(`${at}/key`, "reserved", `${singleKey} is a single VM's: give the component another key.`);
    keys.add(key);
    let literal = -1;
    if (c.count === undefined || c.count === null) literal = 1;
    else if (typeof c.count === "number") {
      if (!Number.isInteger(c.count) || c.count < 0 || c.count > maxVMs)
        add(`${at}/count`, "out_of_range", `A count from 0 to ${maxVMs}.`);
      else literal = c.count;
    } else if (typeof c.count === "string") cel(`${at}/count`, c.count, "The count");
    else add(`${at}/count`, "invalid", "A number, or CEL over the inputs (inputs.web_nodes).");
    if (literal >= 0) total += literal;
    if (c.anti_affinity === true && literal >= 0 && literal < 2)
      add(
        `${at}/anti_affinity`,
        "invalid",
        "Anti-affinity keeps several VMs apart: give the component a count of two or more.",
      );
    bindings(`${at}/bindings`, c.bindings);
    parts(at, c);
  });
  if (total > maxVMs)
    add(`${ptr}/components`, "out_of_range", `A request builds at most ${maxVMs} VMs; these components have ${total}.`);

  comps.forEach((c, i) =>
    list(c.depends_on).forEach((d, j) => {
      const at = `${ptr}/components/${i}/depends_on/${j}`;
      if (d === c.key) add(at, "invalid", "A component isn't built after itself.");
      else if (!keys.has(text(d))) add(at, "unknown_component", `There is no component ${text(d)}.`);
    }),
  );
  const cycle = dependsCycle(comps);
  if (cycle) {
    const i = comps.findIndex((c) => c.key === cycle[0]);
    const j = list(comps[i]!.depends_on).indexOf(cycle[1]);
    add(
      `${ptr}/components/${i}/depends_on/${j}`,
      "cycle",
      `These components would each be built after the other: ${cycle.join(" after ")}. Remove a depends_on or a reference.`,
    );
  }
  return errs;
}

/** The first cycle of depends_on, as keys from one back to itself, or null. */
function dependsCycle(comps: Rec[]): string[] | null {
  const after = new Map<string, string[]>();
  for (const c of comps) if (!after.has(text(c.key))) after.set(text(c.key), list(c.depends_on).map(text));
  const done = new Set<string>();
  const walk = (key: string, path: string[]): string[] | null => {
    const at = path.indexOf(key);
    if (at >= 0) return [...path.slice(at), key];
    if (done.has(key)) return null;
    for (const d of after.get(key) ?? []) {
      if (d === key || !after.has(d)) continue;
      const found = walk(d, [...path, key]);
      if (found) return found;
    }
    done.add(key);
    return null;
  };
  for (const c of comps) {
    const found = walk(text(c.key), []);
    if (found) return found;
  }
  return null;
}

const sizeBindings = [
  {
    key: "vcpu",
    missing:
      "Give the VMs their number of vCPUs: a number, or CEL over the inputs, for example sizes[inputs.size].vcpu.",
    small: "A VM has at least 1 vCPU: give a whole number, or CEL over the inputs.",
  },
  {
    key: "memory_gb",
    missing:
      "Give the VMs their memory in GB: a number, or CEL over the inputs, for example sizes[inputs.size].memory_gb.",
    small: "A VM has at least 1 GB of memory: give a whole number, or CEL over the inputs.",
  },
];

/** As the server's checkSize, to publish: each VM's CPU and memory are bound, a plain number to 1 or more. */
export function vmSizeProblems(bp: Rec, ptr: string): FieldError[] {
  if (text(bp.kind) !== "vm") return [];
  const comps = list(bp.components).map(rec);
  const owners = comps.length
    ? comps.map((c, i) => ({ at: `${ptr}/components/${i}`, bindings: { ...rec(bp.bindings), ...rec(c.bindings) } }))
    : [{ at: ptr, bindings: rec(bp.bindings) }];
  const errs: FieldError[] = [];
  for (const { at, bindings } of owners)
    for (const b of sizeBindings) {
      const expr = bound(bindings[b.key]).trim();
      const field = `${at}/bindings/${b.key}`;
      if (!expr) errs.push({ field, code: "required", message: b.missing });
      else if (/^-?\d+(\.\d+)?$/.test(expr) && (!Number.isInteger(Number(expr)) || Number(expr) < 1))
        errs.push({ field, code: "out_of_range", message: b.small });
    }
  return errs;
}

/**
 * The VMs a VM blueprint gives no naming, as the server's components take
 * the blueprint's: its one VM, else each such component, by index and title.
 */
export function unnamedVMs(bp: Rec | undefined): { at?: number; title: string }[] {
  if (!bp || text(bp.kind) !== "vm" || text(bp.naming)) return [];
  const comps = list(bp.components).map(rec);
  if (comps.length === 0) return [{ title: "" }];
  return comps.flatMap((c, at) => (text(c.naming) ? [] : [{ at, title: text(c.title) || text(c.key) }]));
}

/** As the server's namingWarnings: VMs only a naming policy names, unless one applies everywhere. */
export function namingWarnings(bp: Rec | undefined, ptr: string, everywhere: boolean): FieldError[] {
  const unnamed = unnamedVMs(bp);
  if (unnamed.length === 0 || everywhere) return [];
  const needs = "its requests need a naming policy, and are refused in a project and environment no policy applies to.";
  if (unnamed.length === Math.max(1, list(bp?.components).length))
    return [{ field: `${ptr}/naming`, code: "no_naming", message: `The item gives its VMs no naming: ${needs}` }];
  return unnamed.map(({ at, title }) => ({
    field: `${ptr}/components/${at}/naming`,
    code: "no_naming",
    message: `${title} has no naming, and the item gives its VMs none: ${needs}`,
  }));
}

/**
 * As the server's checkInputReads: a VM blueprint's CEL reads only the
 * form's inputs, its property groups' among them; a request would fail on
 * another.
 */
export function vmInputProblems(bp: Rec, ptr: string, inputs: ReadonlySet<string>): FieldError[] {
  if (text(bp.kind) !== "vm" || bindingsShape(bp, ptr)) return [];
  const errs: FieldError[] = [];
  const read = (at: string, expr: string) => {
    if (!expr || celCheck(expr)) return;
    const name = inputsRead(expr).find((n) => !inputs.has(n));
    if (name)
      errs.push({
        field: at,
        code: "unknown_input",
        message: `This reads inputs.${name}, and "${name}" is not an input of the form's schema.`,
      });
  };
  const sorted = (m: unknown) => Object.entries(rec(m)).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
  const parts = (at: string, owner: Rec) => {
    for (const [k, v] of sorted(owner.bindings)) read(`${at}/bindings/${k}`, bound(v));
    list(owner.disks).forEach((raw, i) => {
      const d = rec(raw);
      read(`${at}/disks/${i}/size_gb`, text(d.size_gb));
      read(`${at}/disks/${i}/datastore`, text(d.datastore));
    });
    list(owner.nics).forEach((raw, i) => read(`${at}/nics/${i}/network`, text(rec(raw).network)));
  };
  parts(ptr, bp);
  list(bp.components).forEach((raw, i) => {
    const c = rec(raw);
    read(`${ptr}/components/${i}/count`, text(c.count));
    parts(`${ptr}/components/${i}`, c);
  });
  list(bp.resources).forEach((raw, i) => {
    for (const [k, v] of sorted(rec(raw).with)) read(`${ptr}/resources/${i}/with/${k}`, text(v));
  });
  return errs;
}
