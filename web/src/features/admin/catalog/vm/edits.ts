// The VM designer's edits (ADR-0032, ADR-0066): each takes the definition's
// YAML and gives it back with one change, made on the parsed document
// (../yamlDoc), so the comments and the style of everything else stay as
// written.
//
// A node is named by its ID on the canvas (vmGraph, designed):
// `component:<key>` (`component:vm` for an item without components, whose
// one VM is the blueprint's own settings), `network:<expression>`,
// `network:auto`, `segment:<key>`, `sg:<key>`, `nsx:<id>`, `groups:<expr>`,
// `resource:<key>`, `disk:<component>:<index>` for a component's own disk,
// `disk@<index>` for one of the blueprint's that components without their
// own take, and `any`.
//
// A component without its own disks, adapters or groups takes the
// blueprint's (blueprint.ComponentList): an edit to those of one component
// first gives it its own copy, so the others keep theirs. A connection or
// an edit the definition can't hold is refused with why (EditError) before
// anything is written.
import { type Document, isMap, isScalar, isSeq, type YAMLMap, type YAMLSeq } from "yaml";
import { undeclared } from "@/lib/celReads";
import { dependsOn } from "@/lib/dependsOn";
import { i18n } from "@/lib/i18n";
import { parseText } from "../definitionYaml";
import { copyOf, EditError, load, own, ownItem, put, save, scalarText } from "../yamlDoc";

export { EditError } from "../yamlDoc";

const tr = i18n.getFixedT(null, "admin-catalog");

type Raw = Record<string, unknown>;
const str = (v: unknown) => (typeof v === "string" ? v : "");
const obj = (v: unknown): Raw => (v && typeof v === "object" && !Array.isArray(v) ? (v as Raw) : {});
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const strings = (v: unknown): string[] => (Array.isArray(v) ? v.map(String) : []);

export const prefix = "/definition/blueprint";
/** The key a single VM's node has: the server's blueprint.SingleKey. */
export const singleKey = "vm";
/** A component's, segment's, group's, resource's, disk's or adapter's key, as the server's componentKey. */
export const keyPattern = /^[a-z][a-z0-9]{0,15}$/;
const nsxID = /^[A-Za-z0-9_.-]{1,255}$/;
export const maxVMs = 20;
export const maxSegments = 4;
export const maxSecurityGroups = 8;

// The order keys are written in: the server's (blueprint.Blueprint, Component, Disk, NIC).
const bpOrder = [
  "kind", "naming", "template", "library", "guest", "sizes", "bindings", "awx_templates", "disks", "nics", "tags",
  "constraints", "storage_policy", "provisioning", "linked_clone", "properties", "folder", "prevent_delete", "ipam",
  "ipv6", "components", "segments", "deployment_name", "security_groups", "groups", "resources",
]; // prettier-ignore
const componentOrder = [
  "key", "title", "count", "naming", "template", "library", "guest", "bindings", "disks", "nics", "tags",
  "constraints", "storage_policy", "provisioning", "linked_clone", "properties", "folder", "prevent_delete", "ipam",
  "ipv6", "anti_affinity", "groups", "depends_on",
]; // prettier-ignore
const diskOrder = ["key", "size_gb", "datastore", "storage_policy", "constraints", "provisioning"];
const keyedOrder = ["key", "type", "title", "with", "rules"];
const guestOrder = ["os_family", "domain", "time_zone", "customization_spec"];

export const componentId = (key: string) => `component:${key}`;

/** The kind of node an ID names. */
export type NodeKind =
  "component" | "network" | "auto" | "segment" | "sg" | "nsx" | "chosen" | "resource" | "disk" | "any" | "unknown";

export function kindOf(id: string): NodeKind {
  if (id === "any") return "any";
  if (id === "network:auto") return "auto";
  if (id.startsWith("disk:") || id.startsWith("disk@")) return "disk";
  const at = id.indexOf(":");
  const k = at < 0 ? id : id.slice(0, at);
  switch (k) {
    case "component":
    case "network":
    case "segment":
    case "sg":
    case "nsx":
    case "resource":
      return k;
    case "groups":
      return "chosen";
  }
  return "unknown";
}

const rest = (id: string) => id.slice(id.indexOf(":") + 1);

/** A disk's node: the component whose own disk it is (none for the blueprint's), and its place in the list. */
export function diskOf(id: string): { component?: string; index: number } {
  if (id.startsWith("disk@")) return { index: Number(id.slice(5)) };
  const [, component = "", index = ""] = id.split(":");
  return { component, index: Number(index) };
}

// ---- Reading -------------------------------------------------------------

/** The blueprint of a definition's text, parsed; {} when it doesn't parse. */
export function blueprintOfText(text: string): Raw {
  return obj(obj(parseText(text).value).blueprint);
}

const isMulti = (bp: Raw) => arr(bp.components).length > 0;

/** A component's settings as its VMs get them: its own, else the blueprint's. */
export function resolved(bp: Raw, key: string): Raw | undefined {
  if (!isMulti(bp)) return key === singleKey ? bp : undefined;
  const c = arr(bp.components)
    .map(obj)
    .find((x) => str(x.key) === key);
  if (!c) return undefined;
  return {
    ...c,
    template: str(c.template) || (c.library ? "" : str(bp.template)),
    naming: str(c.naming) || str(bp.naming),
    bindings: { ...obj(bp.bindings), ...obj(c.bindings) },
    disks: c.disks ?? bp.disks,
    nics: c.nics ?? bp.nics,
    groups: c.groups ?? bp.groups,
  };
}

/** A component's adapters: the first (bindings.network), then its nics. */
export function adaptersOf(c: Raw): { key: string; network: string; index: number }[] {
  return [
    { key: "nic1", network: str(obj(c.bindings).network).trim(), index: -1 },
    ...arr(c.nics).map((n, i) => ({
      key: str(obj(n).key) || `nic${i + 2}`,
      network: str(obj(n).network).trim(),
      index: i,
    })),
  ];
}

/** The data a node is drawn from: a component's own (the blueprint for a single VM), a disk, a segment… */
export function nodeData(bp: Raw, id: string): Raw | undefined {
  const keyed = (list: unknown, key: string) =>
    arr(list)
      .map(obj)
      .find((x) => str(x.key) === key);
  switch (kindOf(id)) {
    case "component": {
      const key = rest(id);
      return isMulti(bp) ? keyed(bp.components, key) : key === singleKey ? bp : undefined;
    }
    case "segment":
      return keyed(bp.segments, rest(id));
    case "sg":
      return keyed(bp.security_groups, rest(id));
    case "resource":
      return keyed(bp.resources, rest(id));
    case "disk": {
      const d = diskOf(id);
      const holder = d.component === undefined ? bp : nodeData(bp, componentId(d.component));
      const disk = arr(holder?.disks)[d.index];
      return disk === undefined ? undefined : obj(disk);
    }
  }
  return undefined;
}

/** What a node is called in a message: its title, else its key or what it names. */
export function nameOf(bp: Raw, id: string): string {
  const data = nodeData(bp, id);
  switch (kindOf(id)) {
    case "component":
      return isMulti(bp) ? str(data?.title) || rest(id) : tr("vmDesign.theVm");
    case "disk":
      return str(data?.key) || tr("vmDesign.diskNumber", { number: diskOf(id).index + 1 });
    case "segment":
    case "sg":
    case "resource":
      return str(data?.title) || rest(id);
    case "network":
    case "nsx":
    case "chosen":
      return rest(id);
    case "auto":
      return tr("vmDesign.placement");
  }
  return id;
}

/**
 * Why the canvas can't edit a text, or null when it can: it doesn't parse,
 * it isn't a VM item's, or a part the canvas names by key isn't one it can
 * tell apart (not a list, a key missing or taken twice).
 */
export function designable(text: string): string | null {
  const parsed = parseText(text);
  if (parsed.error) return parsed.error;
  const bp = obj(obj(parsed.value).blueprint);
  if (bp.kind !== "vm") return tr("vmDesign.readOnly.notVm");
  const lists = (holder: Raw, at: string, keys: string[]) => {
    for (const k of keys) {
      const v = holder[k];
      if (v === undefined || v === null) continue;
      if (!Array.isArray(v)) return tr("vmDesign.readOnly.notList", { at: `${at}${k}` });
      if (k === "groups" || k === "depends_on") {
        if (v.some((x) => typeof x !== "string")) return tr("vmDesign.readOnly.notNames", { at: `${at}${k}` });
      } else if (v.some((x) => !x || typeof x !== "object" || Array.isArray(x)))
        return tr("vmDesign.readOnly.notMaps", { at: `${at}${k}` });
    }
    for (const k of ["bindings", "guest"]) {
      const v = holder[k];
      if (v !== undefined && v !== null && (typeof v !== "object" || Array.isArray(v)))
        return tr("vmDesign.readOnly.notMap", { at: `${at}${k}` });
    }
    return null;
  };
  const top = lists(bp, "", ["components", "segments", "security_groups", "resources", "disks", "nics", "groups"]);
  if (top) return top;
  const unique = (list: unknown, at: string) => {
    const seen = new Set<string>();
    for (const x of arr(list).map(obj)) {
      const key = x.key;
      if (typeof key !== "string" || !key) return tr("vmDesign.readOnly.noKey", { at });
      if (seen.has(key)) return tr("vmDesign.readOnly.keyTwice", { at, key });
      seen.add(key);
    }
    return null;
  };
  for (const [list, at] of [
    [bp.components, "components"],
    [bp.segments, "segments"],
    [bp.security_groups, "security_groups"],
    [bp.resources, "resources"],
  ] as const) {
    const why = unique(list, at);
    if (why) return why;
  }
  for (const c of arr(bp.components).map(obj)) {
    const why = lists(c, `components.${str(c.key)}.`, ["disks", "nics", "groups", "depends_on"]);
    if (why) return why;
  }
  for (const sg of arr(bp.security_groups).map(obj)) {
    const rules = sg.rules;
    if (rules !== undefined && rules !== null && !Array.isArray(rules))
      return tr("vmDesign.readOnly.notList", { at: `security_groups.${str(sg.key)}.rules` });
  }
  return null;
}

// ---- The document --------------------------------------------------------

function blueprintOf(doc: Document): YAMLMap {
  const bp = isMap(doc.contents) ? own(doc, doc.contents, "blueprint") : undefined;
  if (!isMap(bp)) throw new EditError(tr("composite.edit.noBlueprint"));
  bp.flow = false;
  return bp;
}

/** The blueprint as data, as the edit finds it. */
const dataOf = (doc: Document, bp: YAMLMap): Raw => obj(bp.toJS(doc));

interface Comp {
  key: string;
  /** Its map: the blueprint's for a single VM. */
  map: YAMLMap;
  single: boolean;
}

function componentsSeq(doc: Document, bp: YAMLMap): YAMLSeq | undefined {
  const list = own(doc, bp, "components");
  return isSeq(list) && list.items.length > 0 ? list : undefined;
}

function componentOf(doc: Document, bp: YAMLMap, id: string): Comp {
  if (kindOf(id) !== "component") throw new EditError(tr("vmDesign.edit.gone"));
  const key = rest(id);
  const list = componentsSeq(doc, bp);
  if (!list) {
    if (key !== singleKey) throw new EditError(tr("vmDesign.edit.gone"));
    return { key, map: bp, single: true };
  }
  for (let i = 0; i < list.items.length; i++) {
    const m = ownItem(doc, list, i);
    if (isMap(m) && scalarText(m.get("key", true)) === key) {
      m.flow = false;
      return { key, map: m, single: false };
    }
  }
  throw new EditError(tr("vmDesign.edit.gone"));
}

function mapAt(doc: Document, holder: YAMLMap, key: string, order: string[]): YAMLMap {
  const v = own(doc, holder, key);
  if (isMap(v)) {
    if (v.items.length === 0) v.flow = false;
    return v;
  }
  const made = doc.createNode({}) as YAMLMap;
  put(doc, holder, key, made, order);
  return made;
}

function seqAt(doc: Document, holder: YAMLMap, key: string, order: string[], flow = false): YAMLSeq {
  const v = own(doc, holder, key);
  if (isSeq(v)) {
    if (v.items.length === 0) v.flow = flow;
    return v;
  }
  const made = doc.createNode([]) as YAMLSeq;
  made.flow = flow;
  put(doc, holder, key, made, order);
  return made;
}

/**
 * A component's own list (disks, nics, groups), to edit: one it takes from
 * the blueprint is copied to it first, so the others keep theirs. A single
 * VM's are the blueprint's.
 */
function ownList(doc: Document, bp: YAMLMap, c: Comp, key: "disks" | "nics" | "groups"): YAMLSeq {
  const flow = key === "groups";
  if (c.single || c.map.has(key)) return seqAt(doc, c.map, key, c.single ? bpOrder : componentOrder, flow);
  const from = own(doc, bp, key);
  const copy = (isSeq(from) ? copyOf(doc, from) : doc.createNode([])) as YAMLSeq;
  if (!isSeq(from)) copy.flow = flow;
  put(doc, c.map, key, copy, componentOrder);
  return copy;
}

/** Whether the blueprint has a list components without their own take. */
function sharedHas(bp: YAMLMap, key: string): boolean {
  const v = bp.get(key, true);
  return isSeq(v) && v.items.length > 0;
}

/** After an item left a component's own list: an empty one goes, unless the blueprint has one it would take instead. */
function tidyList(bp: YAMLMap, c: Comp, key: string, list: YAMLSeq) {
  if (list.items.length > 0) return;
  if (!c.single && sharedHas(bp, key)) list.flow = true;
  else c.map.delete(key);
}

/** After an item left a list of the blueprint's: an empty one goes. */
function tidy(holder: YAMLMap, key: string) {
  const v = holder.get(key, true);
  if (isSeq(v) && v.items.length === 0) holder.delete(key);
  if (isMap(v) && v.items.length === 0) holder.delete(key);
}

const scalarIs = (n: unknown, text: string) => isScalar(n) && String(n.value).trim() === text;

function keyedIndex(list: unknown, key: string): number {
  if (!isSeq(list)) return -1;
  return list.items.findIndex((x) => isMap(x) && scalarText(x.get("key", true)) === key);
}

/** Each map a component's settings are in: the blueprint, then each component. */
function holders(doc: Document, bp: YAMLMap): YAMLMap[] {
  const list = componentsSeq(doc, bp);
  const out = [bp];
  if (list)
    list.items.forEach((_, i) => {
      const m = ownItem(doc, list, i);
      if (isMap(m)) out.push(m);
    });
  return out;
}

function checkKey(key: string) {
  if (!keyPattern.test(key)) throw new EditError(tr("vmDesign.edit.keyFormat"));
}

function takenKeys(data: Raw): Set<string> {
  return new Set([
    ...arr(data.components).map((c) => str(obj(c).key)),
    ...arr(data.resources).map((r) => str(obj(r).key)),
  ]);
}

/** Whether a component's count is a number under two (none is one): anti-affinity keeps two or more apart. */
export const fewerThanTwo = (count: unknown) =>
  count === undefined || count === "" || (typeof count === "number" && count < 2);

/** A value written in a field: a whole number as a number, other text as CEL; "" leaves it out. */
export function numberOrCel(text: string): number | string | undefined {
  const s = text.trim();
  if (!s) return undefined;
  return /^\d+$/.test(s) ? Number(s) : s;
}

// ---- Adding --------------------------------------------------------------

export interface NewComponent {
  key: string;
  title?: string;
  count?: number | string;
  /** The key the item's one VM gets, when it has no components yet: it becomes the first. */
  first?: string;
}

/** Adds a component. An item without components first gets one for the VM it has (`first`), with the blueprint's settings. */
export function addComponent(text: string, c: NewComponent): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const data = dataOf(doc, bp);
  const taken = takenKeys(data);
  checkKey(c.key);
  if (c.key === singleKey) throw new EditError(tr("vmDesign.edit.reserved"));
  if (taken.has(c.key)) throw new EditError(tr("vmDesign.edit.keyTaken", { key: c.key }));
  let list = componentsSeq(doc, bp);
  if (!list) {
    const first = (c.first ?? "").trim();
    checkKey(first);
    if (first === singleKey) throw new EditError(tr("vmDesign.edit.reserved"));
    if (first === c.key || taken.has(first)) throw new EditError(tr("vmDesign.edit.keyTaken", { key: first }));
    list = seqAt(doc, bp, "components", bpOrder);
    list.items.push(doc.createNode({ key: first }));
  }
  const count = c.count ?? 1;
  if (typeof count === "number") {
    if (!Number.isInteger(count) || count < 0 || count > maxVMs)
      throw new EditError(tr("vmDesign.edit.countRange", { max: maxVMs }));
    const total = arr(data.components).reduce<number>(
      (n, x) => {
        const k = obj(x).count ?? 1;
        return n + (typeof k === "number" ? k : 0);
      },
      isMulti(data) ? 0 : 1,
    );
    if (total + count > maxVMs) throw new EditError(tr("vmDesign.edit.tooMany", { max: maxVMs, count: total + count }));
  }
  const made: Raw = { key: c.key };
  if (c.title?.trim()) made.title = c.title.trim();
  if (count !== 1) made.count = count;
  list.items.push(doc.createNode(made));
  return save(doc);
}

/** Gives a component an adapter on a network: its first, when that one names none, else a new one. */
export function addNetwork(text: string, network: string, to: string): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const expr = network.trim();
  checkNetwork(expr);
  adapter(doc, bp, componentOf(doc, bp, to), expr);
  return save(doc);
}

export interface NewKeyed {
  key: string;
  title?: string;
  /** The component it is connected to at once. */
  to?: string;
}

/** Adds an NSX segment made for each deployment, and an adapter on it to a component. */
export function addSegment(text: string, s: NewKeyed): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const data = dataOf(doc, bp);
  checkKey(s.key);
  if (arr(data.segments).some((x) => str(obj(x).key) === s.key))
    throw new EditError(tr("vmDesign.edit.segmentTaken", { key: s.key }));
  if (arr(data.segments).length >= maxSegments)
    throw new EditError(tr("vmDesign.edit.maxSegments", { max: maxSegments }));
  const to = s.to ? componentOf(doc, bp, s.to) : undefined;
  seqAt(doc, bp, "segments", bpOrder).items.push(doc.createNode(titled(s)));
  if (to) adapter(doc, bp, to, `segments.${s.key}`);
  return save(doc);
}

/** Adds an NSX group made for each deployment, with a component's VMs in it. */
export function addSecurityGroup(text: string, g: NewKeyed): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const data = dataOf(doc, bp);
  checkKey(g.key);
  if (arr(data.security_groups).some((x) => str(obj(x).key) === g.key))
    throw new EditError(tr("vmDesign.edit.groupTaken", { key: g.key }));
  if (arr(data.security_groups).length >= maxSecurityGroups)
    throw new EditError(tr("vmDesign.edit.maxGroups", { max: maxSecurityGroups }));
  const to = g.to ? componentOf(doc, bp, g.to) : undefined;
  seqAt(doc, bp, "security_groups", bpOrder).items.push(doc.createNode(titled(g)));
  if (to) member(doc, bp, to, g.key);
  return save(doc);
}

/** Puts a component's VMs in an existing NSX group, by its ID. */
export function addNsxGroup(text: string, id: string, to: string): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const g = id.trim();
  if (!nsxID.test(g)) throw new EditError(tr("vmDesign.edit.nsxFormat"));
  member(doc, bp, componentOf(doc, bp, to), `nsx:${g}`);
  return save(doc);
}

export interface NewDisk {
  key?: string;
  size: number | string;
  to: string;
}

/** Adds a disk to a component's VMs. */
export function addDisk(text: string, d: NewDisk): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const c = componentOf(doc, bp, d.to);
  checkSize(d.size);
  const key = d.key?.trim();
  if (key) checkKey(key);
  const list = ownList(doc, bp, c, "disks");
  if (key && keyedIndex(list, key) >= 0) throw new EditError(tr("vmDesign.edit.diskTaken", { key }));
  list.items.push(doc.createNode(key ? { key, size_gb: d.size } : { size_gb: d.size }));
  return save(doc);
}

export interface NewResource {
  key: string;
  type: string;
  title?: string;
}

/** Adds a custom resource, made once the VMs are built. */
export function addResource(text: string, r: NewResource): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const data = dataOf(doc, bp);
  if (!isMulti(data)) throw new EditError(tr("vmDesign.edit.resourceSingle"));
  checkKey(r.key);
  if (takenKeys(data).has(r.key)) throw new EditError(tr("vmDesign.edit.keyTaken", { key: r.key }));
  if (!r.type.trim()) throw new EditError(tr("vmDesign.edit.noType"));
  const made: Raw = { key: r.key, type: r.type.trim() };
  if (r.title?.trim()) made.title = r.title.trim();
  seqAt(doc, bp, "resources", bpOrder).items.push(doc.createNode(made));
  return save(doc);
}

function titled(k: NewKeyed): Raw {
  return k.title?.trim() ? { key: k.key, title: k.title.trim() } : { key: k.key };
}

/**
 * A network is CEL over the request (policy.Check): a name CEL would read
 * as a variable, as dvpg-web, goes in quotes.
 */
function checkNetwork(expr: string) {
  if (!expr) throw new EditError(tr("vmDesign.edit.noNetwork"));
  const unknown = undeclared(expr);
  if (unknown === undefined) return;
  if (/^[\w.-]+$/.test(expr)) throw new EditError(tr("vmDesign.edit.quoteNetwork", { quoted: JSON.stringify(expr) }));
  throw new EditError(tr("vmDesign.edit.networkReads", { name: unknown }));
}

function checkSize(size: number | string) {
  if (typeof size === "number") {
    if (!Number.isInteger(size) || size < 1 || size > 65536) throw new EditError(tr("vmDesign.edit.sizeRange"));
  } else if (!size.trim()) throw new EditError(tr("vmDesign.edit.noSize"));
}

// ---- Connecting ----------------------------------------------------------

function adapter(doc: Document, bp: YAMLMap, c: Comp, expr: string): string {
  const data = dataOf(doc, bp);
  const r = resolved(data, c.key) ?? {};
  const name = nameOf(data, componentId(c.key));
  if (adaptersOf(r).some((a) => a.network === expr))
    throw new EditError(tr("vmDesign.edit.alreadyOn", { name, network: expr }));
  if (!str(obj(r.bindings).network).trim()) {
    put(doc, mapAt(doc, c.map, "bindings", c.single ? bpOrder : componentOrder), "network", expr);
    return tr("vmDesign.says.firstAdapter", { name, network: expr });
  }
  const nics = ownList(doc, bp, c, "nics");
  const keys = new Set(adaptersOf({ ...r, nics: nics.toJS(doc) }).map((a) => a.key));
  let n = nics.items.length + 2;
  while (keys.has(`nic${n}`)) n++;
  nics.items.push(doc.createNode({ key: `nic${n}`, network: expr }));
  return tr("vmDesign.says.newAdapter", { name, key: `nic${n}`, network: expr });
}

function member(doc: Document, bp: YAMLMap, c: Comp, ref: string): string {
  const data = dataOf(doc, bp);
  const r = resolved(data, c.key) ?? {};
  const name = nameOf(data, componentId(c.key));
  const group = nameOf(data, ref.startsWith("nsx:") ? ref : `sg:${ref}`);
  if (strings(r.groups).includes(ref)) throw new EditError(tr("vmDesign.edit.alreadyIn", { name, group }));
  const list = ownList(doc, bp, c, "groups");
  list.items.push(doc.createNode(ref));
  return tr("vmDesign.says.member", { name, group });
}

/** Whether `key` is built after `other`, by depends_on or what its tags or user data read, as far down as it goes. */
function after(data: Raw, key: string, other: string): boolean {
  const deps = dependsOn(data);
  const seen = new Set<string>();
  const walk = (k: string): boolean => {
    if (seen.has(k)) return false;
    seen.add(k);
    return (deps.get(k) ?? []).some((d) => d === other || walk(d));
  };
  return walk(key);
}

function buildAfter(doc: Document, bp: YAMLMap, first: Comp, then: Comp): string {
  const data = dataOf(doc, bp);
  const a = nameOf(data, componentId(first.key));
  const b = nameOf(data, componentId(then.key));
  if (first.key === then.key) throw new EditError(tr("vmDesign.edit.selfAfter"));
  if ((dependsOn(data).get(then.key) ?? []).includes(first.key))
    throw new EditError(tr("vmDesign.edit.alreadyAfter", { first: a, then: b }));
  if (after(data, first.key, then.key)) throw new EditError(tr("vmDesign.edit.loop", { first: a, then: b }));
  seqAt(doc, then.map, "depends_on", componentOrder, true).items.push(doc.createNode(first.key));
  return tr("vmDesign.says.after", { first: a, then: b });
}

function attach(doc: Document, bp: YAMLMap, diskId: string, c: Comp): string {
  const data = dataOf(doc, bp);
  const d = diskOf(diskId);
  const name = nameOf(data, componentId(c.key));
  const disk = nameOf(data, diskId);
  const target = nodeData(data, componentId(c.key)) ?? {};
  if (c.single || d.component === c.key || (d.component === undefined && target.disks === undefined))
    throw new EditError(tr("vmDesign.edit.alreadyHas", { name, disk }));
  const from = d.component === undefined ? undefined : componentOf(doc, bp, componentId(d.component));
  const fromList = from ? own(doc, from.map, "disks") : own(doc, bp, "disks");
  const node = isSeq(fromList) ? fromList.items[d.index] : undefined;
  if (!isSeq(fromList) || !node) throw new EditError(tr("vmDesign.edit.gone"));
  const key = str(obj(nodeData(data, diskId)).key);
  const list = ownList(doc, bp, c, "disks");
  if (key && keyedIndex(list, key) >= 0) throw new EditError(tr("vmDesign.edit.diskTaken", { key }));
  list.items.push(copyOf(doc, node));
  if (!from) return tr("vmDesign.says.copyDisk", { name, disk });
  fromList.items.splice(d.index, 1);
  tidyList(bp, from, "disks", fromList);
  return tr("vmDesign.says.moveDisk", { disk, from: nameOf(data, componentId(from.key)), name });
}

/** Why two nodes can't be connected, for the pairs the definition has no field for. */
function refusal(a: NodeKind, b: NodeKind): string {
  const has = (k: NodeKind) => a === k || b === k;
  if (has("component")) {
    if (has("resource")) return tr("vmDesign.edit.toResource");
    if (has("auto")) return tr("vmDesign.edit.toPlacement");
    if (has("chosen")) return tr("vmDesign.edit.toChosen");
    if (has("any")) return tr("vmDesign.edit.toAny");
  }
  if (has("disk")) return tr("vmDesign.edit.diskToVm");
  if ((has("sg") || has("nsx") || has("any")) && (a === "sg" || b === "sg")) return tr("vmDesign.edit.rule");
  if (has("network") || has("segment") || has("auto")) return tr("vmDesign.edit.networkToVm");
  if (has("resource")) return tr("vmDesign.edit.resourceReads");
  return tr("vmDesign.edit.cantConnect");
}

function connectDoc(doc: Document, bp: YAMLMap, source: string, target: string): string {
  const a = kindOf(source);
  const b = kindOf(target);
  if (source === target) throw new EditError(tr("vmDesign.edit.selfConnect"));
  if (a === "component" && b === "component")
    return buildAfter(doc, bp, componentOf(doc, bp, source), componentOf(doc, bp, target));
  const [compId, otherId] = a === "component" ? [source, target] : b === "component" ? [target, source] : [];
  if (!compId || !otherId) throw new EditError(refusal(a, b));
  const c = componentOf(doc, bp, compId);
  const data = dataOf(doc, bp);
  switch (kindOf(otherId)) {
    case "network":
      return adapter(doc, bp, c, rest(otherId));
    case "segment": {
      const key = rest(otherId);
      if (!arr(data.segments).some((s) => str(obj(s).key) === key))
        throw new EditError(tr("vmDesign.edit.noSegment", { key }));
      return adapter(doc, bp, c, `segments.${key}`);
    }
    case "sg": {
      const key = rest(otherId);
      if (!arr(data.security_groups).some((s) => str(obj(s).key) === key))
        throw new EditError(tr("vmDesign.edit.noGroup", { key }));
      return member(doc, bp, c, key);
    }
    case "nsx":
      return member(doc, bp, c, otherId);
    case "disk":
      return attach(doc, bp, otherId, c);
  }
  throw new EditError(refusal(a, b));
}

/**
 * What connecting two nodes writes, and says: a component to a network or
 * segment gives it an adapter there, to a group puts its VMs in it, to a
 * disk attaches it (moved from the component that had it, or copied from
 * the blueprint's); from one component to another builds the second after
 * the first (depends_on). Anything else is refused, with why.
 */
export function connection(text: string, source: string, target: string): { text: string; says: string } {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const says = connectDoc(doc, bp, source, target);
  return { text: save(doc), says };
}

export const connect = (text: string, source: string, target: string) => connection(text, source, target).text;

// ---- Removing ------------------------------------------------------------

/** Removes the adapters on a network, wherever they are: a first adapter's network is left to placement. */
function dropAdapters(doc: Document, bp: YAMLMap, expr: string) {
  for (const h of holders(doc, bp)) {
    const bindings = h.get("bindings", true);
    if (isMap(bindings) && scalarIs(bindings.get("network", true), expr)) {
      const owned = own(doc, h, "bindings");
      if (isMap(owned)) owned.delete("network");
      tidy(h, "bindings");
    }
    const nics = own(doc, h, "nics");
    if (isSeq(nics)) {
      const before = nics.items.length;
      nics.items = nics.items.filter((n) => !(isMap(n) && scalarIs(n.get("network", true), expr)));
      if (nics.items.length !== before && nics.items.length === 0) {
        if (h !== bp && sharedHas(bp, "nics")) nics.flow = true;
        else h.delete("nics");
      }
    }
  }
}

/** Takes a group out of every list of groups and every rule's sources; a rule left with none goes. */
function dropGroupRefs(doc: Document, bp: YAMLMap, ref: string) {
  for (const h of holders(doc, bp)) {
    const groups = own(doc, h, "groups");
    if (!isSeq(groups)) continue;
    const before = groups.items.length;
    groups.items = groups.items.filter((g) => !scalarIs(g, ref));
    if (groups.items.length !== before && groups.items.length === 0) {
      if (h !== bp && sharedHas(bp, "groups")) groups.flow = true;
      else h.delete("groups");
    }
  }
  const sgs = own(doc, bp, "security_groups");
  if (!isSeq(sgs)) return;
  for (const sg of sgs.items) {
    if (!isMap(sg)) continue;
    const rules = own(doc, sg, "rules");
    if (!isSeq(rules)) continue;
    rules.items = rules.items.filter((r) => {
      if (!isMap(r)) return true;
      const from = r.get("from", true);
      if (!isSeq(from)) return true;
      const had = from.items.length;
      from.items = from.items.filter((f) => !scalarIs(f, ref));
      return !(had > 0 && from.items.length === 0);
    });
    tidy(sg, "rules");
  }
}

/** Removes a node: what refers to it goes with it (the adapters on a network, a group's members and rules, a component's place in others' depends_on). */
export function removeNode(text: string, id: string): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const data = dataOf(doc, bp);
  switch (kindOf(id)) {
    case "component": {
      const c = componentOf(doc, bp, id);
      const list = componentsSeq(doc, bp);
      if (c.single || !list || list.items.length < 2) throw new EditError(tr("vmDesign.edit.lastVm"));
      list.items.splice(keyedIndex(list, c.key), 1);
      for (const h of holders(doc, bp).slice(1)) {
        const deps = own(doc, h, "depends_on");
        if (!isSeq(deps)) continue;
        deps.items = deps.items.filter((d) => !scalarIs(d, c.key));
        tidy(h, "depends_on");
      }
      break;
    }
    case "network":
      dropAdapters(doc, bp, rest(id));
      break;
    case "segment": {
      const key = rest(id);
      removeKeyed(doc, bp, "segments", key);
      dropAdapters(doc, bp, `segments.${key}`);
      break;
    }
    case "sg":
      removeKeyed(doc, bp, "security_groups", rest(id));
      dropGroupRefs(doc, bp, rest(id));
      break;
    case "nsx":
      dropGroupRefs(doc, bp, id);
      break;
    case "chosen":
      for (const h of holders(doc, bp)) {
        const bindings = h.get("bindings", true);
        if (!isMap(bindings) || !scalarIs(bindings.get("groups", true), rest(id))) continue;
        (own(doc, h, "bindings") as YAMLMap).delete("groups");
        tidy(h, "bindings");
      }
      break;
    case "resource":
      removeKeyed(doc, bp, "resources", rest(id));
      break;
    case "disk": {
      const d = diskOf(id);
      if (!nodeData(data, id)) throw new EditError(tr("vmDesign.edit.gone"));
      if (d.component === undefined || d.component === singleKey) {
        const list = own(doc, bp, "disks") as YAMLSeq;
        list.items.splice(d.index, 1);
        tidy(bp, "disks");
      } else {
        const c = componentOf(doc, bp, componentId(d.component));
        const list = own(doc, c.map, "disks") as YAMLSeq;
        list.items.splice(d.index, 1);
        tidyList(bp, c, "disks", list);
      }
      break;
    }
    case "auto":
      throw new EditError(tr("vmDesign.edit.removePlacement"));
    case "any":
      throw new EditError(tr("vmDesign.edit.removeAny"));
    default:
      throw new EditError(tr("vmDesign.edit.gone"));
  }
  return save(doc);
}

function removeKeyed(doc: Document, bp: YAMLMap, key: string, item: string) {
  const list = own(doc, bp, key);
  const at = keyedIndex(list, item);
  if (!isSeq(list) || at < 0) throw new EditError(tr("vmDesign.edit.gone"));
  list.items.splice(at, 1);
  tidy(bp, key);
}

/** Whether an edge can be removed on the canvas, or why not: an implicit one (tags or user data read a component), a first adapter's, or a resource's. */
export function edgeRemovable(text: string, edgeId: string): string | null {
  try {
    removeEdge(text, edgeId);
    return null;
  } catch (e) {
    return e instanceof Error ? e.message : String(e);
  }
}

/** Removes what an edge stands for: an adapter, a membership, a depends_on entry, a rule's source, a disk. */
export function removeEdge(text: string, edgeId: string): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const data = dataOf(doc, bp);
  const [kind] = edgeId.split(":", 1);
  const body = edgeId.slice(kind!.length + 1);
  switch (kind) {
    case "nic": {
      const at = body.lastIndexOf(":");
      const c = componentOf(doc, bp, body.slice(0, at));
      const nic = body.slice(at + 1);
      const r = resolved(data, c.key) ?? {};
      const name = nameOf(data, componentId(c.key));
      if (nic === "nic1") {
        const ownNetwork = obj(nodeData(data, componentId(c.key))?.bindings).network;
        if (!str(obj(r.bindings).network).trim()) throw new EditError(tr("vmDesign.edit.firstAdapter", { name }));
        if (ownNetwork === undefined) throw new EditError(tr("vmDesign.edit.sharedNetwork", { name }));
        const bindings = own(doc, c.map, "bindings") as YAMLMap;
        bindings.delete("network");
        tidy(c.map, "bindings");
        break;
      }
      const index = adaptersOf(r).find((a) => a.key === nic)?.index ?? -1;
      if (index < 0) throw new EditError(tr("vmDesign.edit.gone"));
      const list = ownList(doc, bp, c, "nics");
      list.items.splice(index, 1);
      if (c.single) tidy(bp, "nics");
      else tidyList(bp, c, "nics", list);
      break;
    }
    case "member": {
      const [compKey = "", ...groupParts] = body.slice("component:".length).split(":");
      const group = groupParts.join(":");
      const c = componentOf(doc, bp, componentId(compKey));
      const groupId = group;
      if (kindOf(groupId) === "chosen") {
        const ownChosen = obj(nodeData(data, componentId(c.key))?.bindings).groups;
        if (ownChosen === undefined) throw new EditError(tr("vmDesign.edit.sharedChosen"));
        (own(doc, c.map, "bindings") as YAMLMap).delete("groups");
        tidy(c.map, "bindings");
        break;
      }
      const ref = kindOf(groupId) === "nsx" ? groupId : rest(groupId);
      const list = ownList(doc, bp, c, "groups");
      const at = list.items.findIndex((g) => scalarIs(g, ref));
      if (at < 0) throw new EditError(tr("vmDesign.edit.gone"));
      list.items.splice(at, 1);
      if (c.single) tidy(bp, "groups");
      else tidyList(bp, c, "groups", list);
      break;
    }
    case "after": {
      const [from = "", to = ""] = body.split(">");
      if (from.startsWith("component:") || to.startsWith("resource:"))
        throw new EditError(tr("vmDesign.edit.resourceReads"));
      const c = componentOf(doc, bp, componentId(to));
      const deps = own(doc, c.map, "depends_on");
      const at = isSeq(deps) ? deps.items.findIndex((d) => scalarIs(d, from)) : -1;
      if (!isSeq(deps) || at < 0)
        throw new EditError(
          tr("vmDesign.edit.reads", {
            name: nameOf(data, componentId(to)),
            other: nameOf(data, componentId(from)),
            key: from,
          }),
        );
      deps.items.splice(at, 1);
      tidy(c.map, "depends_on");
      break;
    }
    case "rule": {
      const [sgKey = "", k = "", m = ""] = body.split(":");
      const sgs = own(doc, bp, "security_groups");
      const sg = isSeq(sgs) ? sgs.items[keyedIndex(sgs, sgKey)] : undefined;
      const rules = isMap(sg) ? own(doc, sg, "rules") : undefined;
      const rule = isSeq(rules) ? rules.items[Number(k)] : undefined;
      if (!isMap(sg) || !isSeq(rules) || !isMap(rule)) throw new EditError(tr("vmDesign.edit.gone"));
      const from = rule.get("from", true);
      if (isSeq(from) && from.items.length > 1) from.items.splice(Number(m), 1);
      else rules.items.splice(Number(k), 1);
      tidy(sg, "rules");
      break;
    }
    case "attach": {
      const [diskId = "", compId = ""] = body.split(">");
      const d = diskOf(diskId);
      const c = componentOf(doc, bp, compId);
      if (d.component !== undefined) return removeNode(text, diskId);
      const list = ownList(doc, bp, c, "disks");
      list.items.splice(d.index, 1);
      tidyList(bp, c, "disks", list);
      break;
    }
    default:
      throw new EditError(tr("vmDesign.edit.gone"));
  }
  return save(doc);
}

// ---- Properties ----------------------------------------------------------

const componentRef = (key: string) => new RegExp(`(?<![\\w.])components\\.${key}(?![a-z0-9_])`, "g");
const userDataRef = (key: string) => new RegExp(`(?<!\\w)\\.components\\.${key}(?![a-z0-9_])`, "g");
const resourceRef = (key: string) => new RegExp(`(?<![\\w.])resources\\.${key}(?![a-z0-9_])`, "g");

/** Rewrites the string scalars of a map's values (bindings, a resource's with), as deep as they go. */
function rewrite(node: unknown, f: (s: string) => string) {
  if (isScalar(node) && typeof node.value === "string") node.value = f(node.value);
  else if (isMap(node)) for (const p of node.items) rewrite(p.value, f);
  else if (isSeq(node)) for (const x of node.items) rewrite(x, f);
}

/** Gives a node another key; what names it by key follows (depends_on, adapters, groups, rules, what CEL reads). */
export function rename(text: string, id: string, next: string): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const data = dataOf(doc, bp);
  const key = next.trim();
  const old = rest(id);
  checkKey(key);
  if (key === old) return text;
  switch (kindOf(id)) {
    case "component": {
      const c = componentOf(doc, bp, id);
      if (c.single) throw new EditError(tr("vmDesign.edit.gone"));
      if (key === singleKey) throw new EditError(tr("vmDesign.edit.reserved"));
      if (takenKeys(data).has(key)) throw new EditError(tr("vmDesign.edit.keyTaken", { key }));
      put(doc, c.map, "key", key, componentOrder);
      for (const h of holders(doc, bp)) {
        const deps = own(doc, h, "depends_on");
        if (isSeq(deps)) for (const d of deps.items) if (scalarIs(d, old) && isScalar(d)) d.value = key;
        const bindings = own(doc, h, "bindings");
        if (isMap(bindings)) rewrite(bindings, (s) => s.replace(componentRef(old), `components.${key}`));
        const guest = h.get("guest", true);
        const ci = isMap(guest) ? guest.get("cloud_init", true) : undefined;
        if (isMap(guest) && isMap(ci)) {
          const ownGuest = own(doc, h, "guest") as YAMLMap;
          const ownCi = own(doc, ownGuest, "cloud_init") as YAMLMap;
          rewrite(ownCi.get("user_data", true), (s) => s.replace(userDataRef(old), `.components.${key}`));
        }
      }
      for (const r of seqItems(doc, bp, "resources"))
        rewrite(own(doc, r, "with"), (s) => s.replace(componentRef(old), `components.${key}`));
      break;
    }
    case "segment": {
      const s = keyedMap(doc, bp, "segments", old);
      if (seqItems(doc, bp, "segments").some((x) => scalarText(x.get("key", true)) === key))
        throw new EditError(tr("vmDesign.edit.segmentTaken", { key }));
      put(doc, s, "key", key, keyedOrder);
      for (const h of holders(doc, bp)) {
        const bindings = h.get("bindings", true);
        if (isMap(bindings) && scalarIs(bindings.get("network", true), `segments.${old}`))
          put(doc, own(doc, h, "bindings") as YAMLMap, "network", `segments.${key}`);
        const nics = own(doc, h, "nics");
        if (isSeq(nics))
          for (const n of nics.items)
            if (isMap(n) && scalarIs(n.get("network", true), `segments.${old}`))
              put(doc, n, "network", `segments.${key}`);
      }
      break;
    }
    case "sg": {
      const g = keyedMap(doc, bp, "security_groups", old);
      if (seqItems(doc, bp, "security_groups").some((x) => scalarText(x.get("key", true)) === key))
        throw new EditError(tr("vmDesign.edit.groupTaken", { key }));
      put(doc, g, "key", key, keyedOrder);
      renameGroupRefs(doc, bp, old, key);
      break;
    }
    case "resource": {
      const r = keyedMap(doc, bp, "resources", old);
      if (takenKeys(data).has(key)) throw new EditError(tr("vmDesign.edit.keyTaken", { key }));
      put(doc, r, "key", key, keyedOrder);
      for (const x of seqItems(doc, bp, "resources"))
        rewrite(own(doc, x, "with"), (s) => s.replace(resourceRef(old), `resources.${key}`));
      break;
    }
    case "disk": {
      const d = diskMap(doc, bp, id);
      put(doc, d.map, "key", key, diskOrder);
      if (d.list.items.filter((x) => isMap(x) && scalarText(x.get("key", true)) === key).length > 1)
        throw new EditError(tr("vmDesign.edit.diskTaken", { key }));
      break;
    }
    default:
      throw new EditError(tr("vmDesign.edit.gone"));
  }
  return save(doc);
}

function renameGroupRefs(doc: Document, bp: YAMLMap, old: string, key: string) {
  for (const h of holders(doc, bp)) {
    const groups = own(doc, h, "groups");
    if (isSeq(groups)) for (const g of groups.items) if (isScalar(g) && scalarIs(g, old)) g.value = key;
  }
  for (const sg of seqItems(doc, bp, "security_groups")) {
    const rules = own(doc, sg, "rules");
    if (!isSeq(rules)) continue;
    for (const r of rules.items) {
      const from = isMap(r) ? r.get("from", true) : undefined;
      if (isSeq(from)) for (const f of from.items) if (isScalar(f) && scalarIs(f, old)) f.value = key;
    }
  }
}

function seqItems(doc: Document, bp: YAMLMap, key: string): YAMLMap[] {
  const list = own(doc, bp, key);
  if (!isSeq(list)) return [];
  return list.items.map((_, i) => ownItem(doc, list, i)).filter((x): x is YAMLMap => isMap(x));
}

function keyedMap(doc: Document, bp: YAMLMap, key: string, item: string): YAMLMap {
  const found = seqItems(doc, bp, key).find((x) => scalarText(x.get("key", true)) === item);
  if (!found) throw new EditError(tr("vmDesign.edit.gone"));
  return found;
}

function diskMap(doc: Document, bp: YAMLMap, id: string): { map: YAMLMap; list: YAMLSeq } {
  const d = diskOf(id);
  const holder =
    d.component === undefined || (d.component === singleKey && !componentsSeq(doc, bp))
      ? bp
      : componentOf(doc, bp, componentId(d.component)).map;
  const list = own(doc, holder, "disks");
  const map = isSeq(list) ? ownItem(doc, list, d.index) : undefined;
  if (!isSeq(list) || !isMap(map)) throw new EditError(tr("vmDesign.edit.gone"));
  return { map, list };
}

/** A field of a node's own: a component's (the blueprint's for a single VM), a disk's, a segment's, a group's, a resource's. "" or undefined removes it. */
export function setField(
  text: string,
  id: string,
  field: string,
  value: string | number | boolean | undefined,
): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const empty = value === undefined || value === "" || value === false;
  let holder: YAMLMap;
  let order: string[];
  switch (kindOf(id)) {
    case "component": {
      const c = componentOf(doc, bp, id);
      holder = c.map;
      order = c.single ? bpOrder : componentOrder;
      if (field === "count" && typeof value === "number" && (!Number.isInteger(value) || value < 0 || value > maxVMs))
        throw new EditError(tr("vmDesign.edit.countRange", { max: maxVMs }));
      if (!c.single && (field === "count" || field === "anti_affinity")) {
        const now = obj(c.map.toJS(doc));
        const count = field === "count" ? value : now.count;
        const apart = field === "anti_affinity" ? value === true : now.anti_affinity === true;
        if (apart && fewerThanTwo(count))
          throw new EditError(tr(field === "count" ? "vmDesign.edit.apartCount" : "vmDesign.edit.apartOne"));
      }
      break;
    }
    case "disk":
      holder = diskMap(doc, bp, id).map;
      order = diskOrder;
      if (field === "size_gb") {
        if (empty) throw new EditError(tr("vmDesign.edit.noSize"));
        checkSize(value as number | string);
      }
      break;
    case "segment":
      holder = keyedMap(doc, bp, "segments", rest(id));
      order = keyedOrder;
      break;
    case "sg":
      holder = keyedMap(doc, bp, "security_groups", rest(id));
      order = keyedOrder;
      break;
    case "resource":
      holder = keyedMap(doc, bp, "resources", rest(id));
      order = keyedOrder;
      if (field === "type" && empty) throw new EditError(tr("vmDesign.edit.noType"));
      break;
    default:
      throw new EditError(tr("vmDesign.edit.gone"));
  }
  const [first = "", second] = field.split(".");
  if (second === undefined) {
    if (empty) holder.delete(first);
    else put(doc, holder, first, value, order);
  } else if (empty) {
    const sub = own(doc, holder, first);
    if (isMap(sub)) {
      sub.delete(second);
      tidy(holder, first);
    }
  } else put(doc, mapAt(doc, holder, first, order), second, value, first === "guest" ? guestOrder : []);
  return save(doc);
}

/** Points every adapter on a network at another. */
export function setNetwork(text: string, id: string, next: string): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const old = rest(id);
  const expr = next.trim();
  checkNetwork(expr);
  for (const h of holders(doc, bp)) {
    const bindings = h.get("bindings", true);
    if (isMap(bindings) && scalarIs(bindings.get("network", true), old))
      put(doc, own(doc, h, "bindings") as YAMLMap, "network", expr);
    const nics = own(doc, h, "nics");
    if (isSeq(nics))
      for (const n of nics.items) if (isMap(n) && scalarIs(n.get("network", true), old)) put(doc, n, "network", expr);
  }
  return save(doc);
}

/** Names another existing NSX group wherever one is named. */
export function setNsxGroup(text: string, id: string, next: string): string {
  const doc = load(text);
  const bp = blueprintOf(doc);
  const g = next.trim();
  if (!nsxID.test(g)) throw new EditError(tr("vmDesign.edit.nsxFormat"));
  renameGroupRefs(doc, bp, id, `nsx:${g}`);
  return save(doc);
}

/** The keys a new component or resource can take, `stem` then `stem2`…; the first free one. */
export function freeKey(
  text: string,
  stem: string,
  kind: "component" | "segment" | "sg" | "resource" | "disk",
  to?: string,
): string {
  const bp = blueprintOfText(text);
  const taken =
    kind === "component" || kind === "resource"
      ? takenKeys(bp)
      : kind === "segment"
        ? new Set(arr(bp.segments).map((s) => str(obj(s).key)))
        : kind === "sg"
          ? new Set(arr(bp.security_groups).map((s) => str(obj(s).key)))
          : new Set(arr(resolved(bp, to ? rest(to) : singleKey)?.disks).map((d) => str(obj(d).key)));
  taken.add(singleKey);
  for (let i = 1; ; i++) if (!taken.has(`${stem}${i}`)) return `${stem}${i}`;
}
