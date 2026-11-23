import { celCode } from "./dependsOn";

/** What a blueprint's bindings, counts, sizes and networks read: the server's `forms` variables (policy/cel.go). */
export const formVariables: ReadonlySet<string> = new Set([
  "inputs",
  "environment",
  "project",
  "item",
  "vm",
  "user",
  "sizes",
  "quota",
  "segments",
  "propgroup",
  "request",
  "update",
  "resource",
  "deployment",
]);

/** What the tags binding reads, worked out as each VM is built (policy/cel.go's `build`). */
export const buildVariables: ReadonlySet<string> = new Set(["inputs", "self", "components"]);

// The keywords, and the type names CEL declares as values (type(x) == string).
const keywords = new Set([
  "true",
  "false",
  "null",
  "in",
  "int",
  "uint",
  "double",
  "bool",
  "string",
  "bytes",
  "list",
  "map",
  "type",
  "null_type",
]);
const macro = /\.\s*(?:all|exists|exists_one|map|filter)\s*\(\s*([A-Za-z_]\w*)\s*,/g;

/**
 * The variables an expression reads, as CEL resolves its names: not a
 * field (after a dot), a function (before a parenthesis), a keyword, or the
 * variable a macro binds (`.map(x, …)`). What's in a string or a comment
 * isn't read.
 */
export function celRoots(expr: string): string[] {
  const code = celCode(expr);
  const bound = new Set([...code.matchAll(macro)].map((m) => m[1]!));
  const out = new Set<string>();
  for (const m of code.matchAll(/(?<!\w)[A-Za-z_]\w*/g)) {
    const name = m[0];
    if (keywords.has(name) || bound.has(name)) continue;
    if (code.slice(0, m.index).trimEnd().endsWith(".")) continue;
    if (
      code
        .slice(m.index + name.length)
        .trimStart()
        .startsWith("(")
    )
      continue;
    out.add(name);
  }
  return [...out];
}

/** The inputs an expression reads by name, as inputs.<name> or inputs["<name>"] (policy.ReadsOf). */
export function inputsRead(expr: string): string[] {
  const dotted = [...celCode(expr).matchAll(/(?<![\w.])inputs\s*\.\s*([A-Za-z_]\w*)/g)].map((m) => m[1]!);
  const indexed = [...expr.matchAll(/(?<![\w.])inputs\s*\[\s*(["'])(\w+)\1\s*\]/g)].map((m) => m[2]!);
  return [...new Set([...dotted, ...indexed])];
}

/** The first variable an expression reads that `known` doesn't have, if any. */
export function undeclared(expr: string, known: ReadonlySet<string> = formVariables): string | undefined {
  return celRoots(expr).find((r) => !known.has(r));
}
