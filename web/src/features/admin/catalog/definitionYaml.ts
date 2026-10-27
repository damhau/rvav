// A version's definition as YAML, and server problems mapped to its lines.
import { isMap, isSeq, LineCounter, parseDocument, stringify, type Node } from "yaml";
import type { CatalogVersionDefinition, FieldError } from "@/lib/api/types";
import type { LineProblem } from "./YamlEditor";

export function toYaml(def: CatalogVersionDefinition): string {
  return stringify(def, { indent: 2, lineWidth: 0 });
}

/** Parses the editor's text, or says what is wrong and where. */
export function fromYaml(text: string): { def?: CatalogVersionDefinition; problems: LineProblem[] } {
  const lc = new LineCounter();
  const doc = parseDocument(text, { lineCounter: lc });
  if (doc.errors.length > 0) {
    return { problems: doc.errors.map((e) => ({ line: lc.linePos(e.pos[0]).line, message: e.message })) };
  }
  const def = doc.toJS() as Partial<CatalogVersionDefinition> | null;
  const missing = (["form_schema", "ui_schema", "blueprint"] as const).filter((k) => !def?.[k]);
  if (!def || missing.length > 0) {
    return { problems: [{ line: 1, message: `The definition needs ${missing.join(", ")}.` }] };
  }
  return { def: { initial_values: {}, ...def } as CatalogVersionDefinition, problems: [] };
}

/** The line of the node a server pointer (under prefix) names, or the deepest on its way. */
export function problemsOnLines(text: string, errors: FieldError[], prefix = "/definition"): LineProblem[] {
  const lc = new LineCounter();
  const doc = parseDocument(text, { lineCounter: lc });
  return errors.map((e) => {
    const path = e.field.startsWith(prefix) ? e.field.slice(prefix.length) : e.field;
    let node: Node | null = doc.contents;
    let offset = 0;
    for (const raw of path.split("/").filter(Boolean)) {
      const seg = raw.replace(/~1/g, "/").replace(/~0/g, "~");
      if (isMap(node)) {
        const pair = node.items.find((p) => String((p.key as { value?: unknown })?.value ?? p.key) === seg);
        if (!pair) break;
        offset = (pair.key as Node | null)?.range?.[0] ?? offset;
        node = pair.value as Node | null;
      } else if (isSeq(node)) {
        const item = node.items[Number(seg)] as Node | undefined;
        if (!item) break;
        offset = item.range?.[0] ?? offset;
        node = item;
      } else break;
    }
    return { line: lc.linePos(offset).line, message: e.message };
  });
}
