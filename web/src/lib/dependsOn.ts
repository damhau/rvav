// Which components of a VM blueprint each is built after (ADR-0024), as the
// server's Blueprint.DependsOn says: those its depends_on names and those
// its tags binding or cloud-init user data read as components.<key>, in the
// blueprint's order, never itself.

type Raw = Record<string, unknown>;

const str = (v: unknown) => (typeof v === "string" ? v : "");
const obj = (v: unknown): Raw => (v && typeof v === "object" && !Array.isArray(v) ? (v as Raw) : {});

const inCel = /(?<![\w.])components\.([a-z][a-z0-9]*)/g;
const inUserData = /(?<!\w)\.components\.([a-z][a-z0-9]*)/g;

const read = (text: string, re: RegExp) => [...text.matchAll(re)].map((m) => m[1]!);

/** Each component's key, with the keys of the components it is built after (none for a blueprint without components). */
export function dependsOn(bp: Raw): Map<string, string[]> {
  const components = Array.isArray(bp.components) ? bp.components.map(obj) : [];
  const keys = components.map((c) => str(c.key));
  const out = new Map<string, string[]>();
  for (const c of components) {
    const key = str(c.key);
    const tags = str({ ...obj(bp.bindings), ...obj(c.bindings) }.tags);
    const userData = str(obj(obj(c.guest).cloud_init ?? obj(bp.guest).cloud_init).user_data);
    const named = new Set([
      ...(Array.isArray(c.depends_on) ? c.depends_on.map(str) : []),
      ...read(tags, inCel),
      ...read(userData, inUserData),
    ]);
    out.set(
      key,
      keys.filter((k) => k !== key && named.has(k)),
    );
  }
  return out;
}
