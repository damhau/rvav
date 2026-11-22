// Edits made on a definition's YAML document (ADR-0032), shared by the
// composite and VM designers: each edit parses the text with yaml's Document
// API, changes the nodes it is about and writes the text back, so comments,
// quoting, key order and the flow or block style of everything else stay as
// written.
//
// An edit changes what it is made on and no other part, whatever anchors and
// aliases (&name, *name) the text shares: a part that is an alias gets a copy
// of what it named before it is edited (own), and an alias elsewhere whose
// anchor the edit changed, removed or moved after it becomes a copy of what
// it named before the edit (save).
import { type Alias, Document, isAlias, isMap, isNode, isScalar, isSeq, parseDocument, type Node, visit } from "yaml";
import type { YAMLMap, YAMLSeq } from "yaml";
import { i18n } from "@/lib/i18n";

const tr = i18n.getFixedT(null, "admin-catalog");

/** How the edited text is written: as toYaml writes it, flow collections as {a: b}. */
export const yamlOutput = { indent: 2, lineWidth: 0, flowCollectionPadding: false } as const;

/** Thrown when an edit can't be made, with why: the YAML doesn't parse, what it is about isn't there, or the definition can't hold it. */
export class EditError extends Error {}

/** What each alias of a document named when it was loaded: its data, and a copy of it. */
type Shared = Map<Alias, { json: string; copy: unknown }>;
const shared = new WeakMap<Document, Shared>();

const dataOf = (doc: Document, node: Node) => JSON.stringify(node.toJS(doc));

/** A copy of a node, its aliases replaced by copies of what they name, without anchors. */
export function copyOf(doc: Document, node: unknown): unknown {
  if (isAlias(node)) {
    const target = node.resolve(doc);
    if (!target) throw new EditError(tr("composite.edit.alias", { alias: node.source }));
    return copyOf(doc, target);
  }
  if (!isNode(node)) return node;
  const out = node.clone();
  if (isScalar(out) || isMap(out) || isSeq(out)) out.anchor = undefined;
  if (isMap(node) && isMap(out))
    out.items = node.items.map((p) => {
      const c = p.clone();
      c.key = copyOf(doc, p.key);
      c.value = copyOf(doc, p.value);
      return c;
    });
  else if (isSeq(node) && isSeq(out)) out.items = node.items.map((i) => copyOf(doc, i));
  return out;
}

/** Notes what each alias names now, to keep it so through the edit (save). */
export function remember(doc: Document) {
  const now: Shared = new Map();
  visit(doc, {
    Alias(_, a) {
      const target = a.resolve(doc);
      if (!target) throw new EditError(tr("composite.edit.alias", { alias: a.source }));
      now.set(a, { json: dataOf(doc, target), copy: copyOf(doc, target) });
    },
  });
  shared.set(doc, now);
}

/** The text's document, to edit; refused when it doesn't parse. */
export function load(text: string): Document {
  const doc = parseDocument(text);
  if (doc.errors.length > 0) throw new EditError(tr("composite.edit.noParse"));
  remember(doc);
  return doc;
}

/**
 * The text of an edited document. An alias whose anchor the edit changed,
 * removed or moved after it is replaced by a copy of what it named: the
 * other parts keep their data.
 */
export function save(doc: Document): string {
  const before = shared.get(doc);
  if (before?.size)
    visit(doc, {
      Alias(_, a) {
        const was = before.get(a);
        if (!was) return undefined;
        const target = a.resolve(doc);
        let same = false;
        try {
          same = target !== undefined && dataOf(doc, target) === was.json;
        } catch {
          // It names something holding an alias the edit broke.
        }
        return same ? undefined : (was.copy as Node);
      },
    });
  return doc.toString(yamlOutput);
}

/** A map's value at a key to edit: an alias there is replaced by a copy of what it names first. */
export function own(doc: Document, map: YAMLMap, key: string): unknown {
  const value: unknown = map.get(key, true);
  if (!isAlias(value)) return value;
  const copy = copyOf(doc, value);
  map.set(key, copy);
  return copy;
}

/** A list's item to edit: an alias there is replaced by a copy of what it names first. */
export function ownItem(doc: Document, list: YAMLSeq, i: number): unknown {
  const value: unknown = list.items[i];
  if (!isAlias(value)) return value;
  const copy = copyOf(doc, value);
  list.items[i] = copy;
  return copy;
}

/** A scalar's text, "" for anything else. */
export const scalarText = (n: unknown) => (isScalar(n) && typeof n.value === "string" ? n.value : "");

/** Sets a key of a map, keeping a scalar's style; a key new to it goes where `order` puts it. */
export function put(doc: Document, map: YAMLMap, key: string, value: unknown, order: string[] = []) {
  const prev = map.get(key, true);
  if (isScalar(prev) && (value === null || typeof value !== "object")) {
    prev.value = value;
    return;
  }
  const node = doc.createNode(value) as Node;
  if (map.has(key)) {
    map.set(key, node);
    return;
  }
  if (map.items.length === 0) map.flow = false;
  const pair = doc.createPair(key, node);
  const rank = order.indexOf(key);
  const at =
    rank < 0
      ? -1
      : map.items.findIndex((p) => {
          const r = order.indexOf(scalarText(p.key));
          return r > rank;
        });
  if (at < 0) map.items.push(pair);
  else map.items.splice(at, 0, pair);
}
