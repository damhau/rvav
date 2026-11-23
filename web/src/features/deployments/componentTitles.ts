// Kept apart from topology.ts so the Overview reads the titles without
// loading the canvas code the Topology tab loads lazily.
type Raw = Record<string, unknown>;
const obj = (v: unknown): Raw => (v && typeof v === "object" && !Array.isArray(v) ? (v as Raw) : {});
const str = (v: unknown) => (typeof v === "string" ? v : "");

/** Each component's title in a request's topology, by key; its key when it has none. */
export function componentTitles(topology: unknown): Map<string, string> {
  const out = new Map<string, string>();
  const components = obj(topology).components;
  if (Array.isArray(components))
    for (const c of components.map(obj)) if (str(c.key)) out.set(str(c.key), str(c.title) || str(c.key));
  return out;
}
