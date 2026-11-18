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

/** The end of the string literal opening at `at` with `quote`, past its closing quote. */
function literalEnd(text: string, at: number, quote: string, escapes: boolean): number {
  let i = at + quote.length;
  while (i < text.length && !text.startsWith(quote, i)) i += escapes && text[i] === "\\" ? 2 : 1;
  return i + quote.length;
}

/**
 * CEL without its string literals and comments, which read nothing: the
 * server reads the parsed expression (policy.ReadsOf).
 */
export function celCode(expr: string): string {
  let out = "";
  for (let i = 0; i < expr.length;) {
    const c = expr[i]!;
    if (c === "/" && expr[i + 1] === "/") {
      const nl = expr.indexOf("\n", i);
      i = nl < 0 ? expr.length : nl;
    } else if (c === "'" || c === '"') {
      const quote = expr.startsWith(c.repeat(3), i) ? c.repeat(3) : c;
      const prefix = /\w*$/.exec(expr.slice(0, i))![0].toLowerCase();
      const raw = prefix === "r" || prefix === "br" || prefix === "rb";
      i = literalEnd(expr, i, quote, !raw);
      out += " ";
    } else {
      out += c;
      i++;
    }
  }
  return out;
}

/**
 * The actions of Go template text without their comments and string
 * literals: what the server's walk of the parsed template sees
 * (blueprint.UserDataComponents). The text between actions reads nothing.
 */
export function templateCode(text: string): string {
  let out = "";
  for (let i = text.indexOf("{{"); i >= 0; i = text.indexOf("{{", i)) {
    let j = i + 2;
    const comment = /^(?:- )?\/\*/.exec(text.slice(j, j + 4));
    if (comment) {
      const end = text.indexOf("*/", j + comment[0].length);
      if (end < 0) break;
      i = end + 2;
      continue;
    }
    while (j < text.length && !text.startsWith("}}", j)) {
      const c = text[j]!;
      if (c === '"' || c === "'" || c === "`") {
        j = literalEnd(text, j, c, c !== "`");
        out += " ";
      } else {
        out += c;
        j++;
      }
    }
    out += "\n";
    i = j + 2;
  }
  return out;
}

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
      ...read(celCode(tags), inCel),
      ...read(templateCode(userData), inUserData),
    ]);
    out.set(
      key,
      keys.filter((k) => k !== key && named.has(k)),
    );
  }
  return out;
}
