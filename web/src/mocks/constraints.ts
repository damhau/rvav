// Constraint tags in the mock (ADR-0035): a seeded blueprint's constraints,
// checked on the cluster, datastore and network a request's form gives, as
// the server does; and those naming a tag nothing in vCenter carries, the
// warning catalog administration shows.
import type { FieldError, PlacementConstraint } from "@/lib/api/types";
import { seededBlueprint } from "./blueprints";
import { clusters, datastores, networks, tags } from "./inventory";

type Kind = PlacementConstraint["kind"];
type Lists = Partial<Record<Kind, string[]>>;

const kinds: Kind[] = ["compute", "storage", "network"];

const obj = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, unknown>) : {};

const list = (v: unknown) => (Array.isArray(v) ? v.filter((s): s is string => typeof s === "string") : undefined);

function parse(entry: string) {
  let text = entry.trim();
  const negated = text.startsWith("!");
  if (negated) text = text.slice(1);
  const soft = text.endsWith(":soft");
  text = text.replace(/:(soft|hard)$/, "");
  return { tag: text, negated, soft };
}

/** A component's constraints: its own of a kind, else the blueprint's. */
function constraintsOf(bp: Record<string, unknown>, key: string): Lists {
  const own = obj((Array.isArray(bp.components) ? bp.components : []).map(obj).find((c) => c.key === key)?.constraints);
  const top = obj(bp.constraints);
  return Object.fromEntries(kinds.map((k) => [k, list(own[k]) ?? list(top[k]) ?? []]));
}

/** What a seeded item's constraints say of where a component's VMs go. */
export function placedBy(
  slug: string,
  key: string,
  on: Partial<Record<Kind, string>>,
): PlacementConstraint[] | undefined {
  const bp = seededBlueprint(slug);
  if (!bp) return undefined;
  const lists = constraintsOf(bp, key);
  const out: PlacementConstraint[] = [];
  for (const kind of kinds) {
    const object = on[kind];
    for (const entry of lists[kind] ?? []) {
      if (entry.includes("${") || !object) continue;
      const k = parse(entry);
      const has = (tags[object] ?? []).includes(k.tag);
      out.push({ kind, ...k, object, met: k.negated ? !has : has, excluded: [] });
    }
  }
  return out.length > 0 ? out : undefined;
}

/** The tags vCenter objects of a kind carry. */
function known(kind: Kind): Set<string> {
  const names =
    kind === "compute"
      ? clusters.map((c) => c.name)
      : kind === "storage"
        ? Object.values(datastores).flatMap((l) => l.map((d) => d.name))
        : networks.map((n) => n.name);
  return new Set(names.flatMap((n) => tags[n] ?? []));
}

/** The constraints of a VM blueprint naming a tag no vCenter object of their kind carries. */
export function constraintWarnings(bp: Record<string, unknown> | undefined, ptr: string): FieldError[] {
  if (!bp || bp.kind !== "vm") return [];
  const out: FieldError[] = [];
  const what = { compute: "cluster", storage: "datastore", network: "network" } as const;
  const walk = (at: string, lists: Record<string, unknown>) => {
    for (const kind of kinds) {
      (list(lists[kind]) ?? []).forEach((entry, i) => {
        if (entry.includes("${")) return;
        const k = parse(entry);
        if (known(kind).has(k.tag)) return;
        out.push({
          field: `${at}/constraints/${kind}/${i}`,
          code: "unknown_tag",
          message:
            k.negated || k.soft
              ? `No ${what[kind]} in vCenter is tagged ${k.tag}: check the tag's name.`
              : `No ${what[kind]} in vCenter is tagged ${k.tag} yet: requests fail until one is.`,
        });
      });
    }
  };
  walk(ptr, obj(bp.constraints));
  (Array.isArray(bp.components) ? bp.components : [])
    .map(obj)
    .forEach((c, i) => walk(`${ptr}/components/${i}`, obj(c.constraints)));
  return out;
}
