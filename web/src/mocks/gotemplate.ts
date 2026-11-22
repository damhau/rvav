// The part of Go's text/template the notification templates use, for the
// mock backend (ADR-0059): actions, if/else if/else, range, with, variables,
// pipelines and the functions templates call. The server's checks are the
// real ones; these follow them closely enough to show the same problems.

/** A kind's variables by name (".Request.Number"), as the server's sample has them. */
export type Values = Record<string, unknown>;

export class TemplateError extends Error {}

type Dot = { path: string } | { value: unknown };

interface Text {
  kind: "text";
  text: string;
}
interface Action {
  kind: "action";
  pipe: Pipe;
  line: number;
}
interface Block {
  kind: "if" | "range" | "with";
  pipe: Pipe;
  line: number;
  body: Node[];
  /** else if is an if alone in the else. */
  otherwise: Node[];
}
type Node = Text | Action | Block;

type Operand =
  | { kind: "field"; path: string[]; on?: Operand }
  | { kind: "dot" }
  | { kind: "var"; name: string; path: string[] }
  | { kind: "string" | "number" | "bool" | "nil"; value: unknown }
  | { kind: "ident"; name: string }
  | { kind: "pipe"; pipe: Pipe };
interface Pipe {
  decl?: string[];
  cmds: Operand[][];
  line: number;
}

const functions = new Set([
  "and",
  "or",
  "not",
  "len",
  "index",
  "slice",
  "print",
  "printf",
  "println",
  "html",
  "js",
  "urlquery",
  "eq",
  "ne",
  "lt",
  "le",
  "gt",
  "ge",
  "date",
  "join",
]);

const fail = (line: number, msg: string): never => {
  throw new TemplateError(`line ${line}: ${msg}`);
};

function lex(src: string, line: number): (string | Operand)[] {
  const out: (string | Operand)[] = [];
  let i = 0;
  while (i < src.length) {
    const c = src[i]!;
    if (/\s/.test(c) || c === ",") {
      i++;
    } else if (c === '"' || c === "`") {
      let j = i + 1;
      let s = "";
      while (j < src.length && src[j] !== c) {
        if (c === '"' && src[j] === "\\") {
          const e = src[j + 1];
          s += e === "n" ? "\n" : e === "t" ? "\t" : (e ?? "");
          j += 2;
        } else s += src[j++];
      }
      if (j >= src.length) fail(line, "unterminated quoted string");
      out.push({ kind: "string", value: s });
      i = j + 1;
    } else if (c === "(" || c === ")" || c === "|") {
      out.push(c);
      i++;
    } else if (src.startsWith(":=", i) || (c === "=" && src[i + 1] !== "=")) {
      out.push(":=");
      i += c === "=" ? 1 : 2;
    } else {
      const m = /^[^\s()|"`,]+/.exec(src.slice(i))![0];
      i += m.length;
      if (m === ".") out.push({ kind: "dot" });
      else if (m.startsWith(".")) {
        const path = m.slice(1).split(".");
        if (path.some((p) => !/^[A-Za-z_]\w*$/.test(p))) fail(line, `bad field ${m}`);
        out.push({ kind: "field", path });
      } else if (m.startsWith("$")) {
        const [name, ...path] = m.split(".");
        out.push({ kind: "var", name: name!, path });
      } else if (/^-?\d+(\.\d+)?$/.test(m)) out.push({ kind: "number", value: Number(m) });
      else if (m === "true" || m === "false") out.push({ kind: "bool", value: m === "true" });
      else if (m === "nil") out.push({ kind: "nil", value: null });
      else if (/^[A-Za-z_]\w*$/.test(m)) out.push({ kind: "ident", name: m });
      else fail(line, `unexpected "${m}" in command`);
    }
  }
  return out;
}

function pipeOf(tokens: (string | Operand)[], line: number): Pipe {
  let rest = tokens;
  let decl: string[] | undefined;
  const assign = rest.indexOf(":=");
  if (assign > 0) {
    decl = rest
      .slice(0, assign)
      .map((t) => (typeof t !== "string" && t.kind === "var" ? t.name : fail(line, "bad declaration")));
    rest = rest.slice(assign + 1);
  }
  const cmds: Operand[][] = [[]];
  for (let i = 0; i < rest.length; i++) {
    const t = rest[i]!;
    if (t === "|") cmds.push([]);
    else if (t === "(") {
      let depth = 1;
      let j = i + 1;
      for (; j < rest.length && depth > 0; j++) depth += rest[j] === "(" ? 1 : rest[j] === ")" ? -1 : 0;
      if (depth > 0) fail(line, "unclosed left paren");
      const inner: Operand = { kind: "pipe", pipe: pipeOf(rest.slice(i + 1, j - 1), line) };
      const next = rest[j];
      if (next && typeof next !== "string" && next.kind === "field") {
        cmds.at(-1)!.push({ kind: "field", path: next.path, on: inner });
        j++;
      } else cmds.at(-1)!.push(inner);
      i = j - 1;
    } else if (t === ")") fail(line, "unexpected right paren");
    else if (t === ":=") fail(line, "unexpected :=");
    else cmds.at(-1)!.push(t as Operand);
  }
  if (cmds.some((c) => c.length === 0)) fail(line, "missing value for command");
  for (const cmd of cmds) {
    const head = cmd[0]!;
    if (head.kind === "ident" && !functions.has(head.name)) fail(line, `function "${head.name}" not defined`);
  }
  return { decl, cmds, line };
}

/** Parses a template: its problems are a TemplateError, "line 2: unexpected {{end}}". */
export function parse(src: string): Node[] {
  const root: Node[] = [];
  const stack: { block: Block; inElse: boolean; elseIf: boolean }[] = [];
  const into = () => {
    const top = stack.at(-1);
    return top ? (top.inElse ? top.block.otherwise : top.block.body) : root;
  };
  let i = 0;
  let trimNext = false;
  while (i < src.length) {
    const open = src.indexOf("{{", i);
    let text = src.slice(i, open < 0 ? src.length : open);
    if (trimNext) text = text.replace(/^\s+/, "");
    trimNext = false;
    if (open < 0) {
      if (text) into().push({ kind: "text", text });
      break;
    }
    const line = src.slice(0, open).split("\n").length;
    const close = src.indexOf("}}", open + 2);
    if (close < 0) fail(line, "unclosed action");
    let body = src.slice(open + 2, close);
    if (body.startsWith("- ")) {
      text = text.replace(/\s+$/, "");
      body = body.slice(2);
    }
    if (body.endsWith(" -")) {
      trimNext = true;
      body = body.slice(0, -2);
    }
    if (text) into().push({ kind: "text", text });
    i = close + 2;
    body = body.trim();
    if (body.startsWith("/*")) continue;
    const word = /^[a-z]+/.exec(body)?.[0] ?? "";
    const rest = body.slice(word.length);
    if (word === "define" || word === "block" || word === "template") {
      fail(line, word === "template" ? "a template can't call another" : "a template can't define others");
    } else if (word === "if" || word === "range" || word === "with") {
      const block: Block = { kind: word, pipe: pipeOf(lex(rest, line), line), line, body: [], otherwise: [] };
      into().push(block);
      stack.push({ block, inElse: false, elseIf: false });
    } else if (word === "else") {
      const top = stack.at(-1);
      if (!top || top.inElse) fail(line, "unexpected {{else}}");
      top!.inElse = true;
      const elseIf = rest.trim();
      if (elseIf.startsWith("if ")) {
        const block: Block = {
          kind: "if",
          pipe: pipeOf(lex(elseIf.slice(3), line), line),
          line,
          body: [],
          otherwise: [],
        };
        top!.block.otherwise.push(block);
        stack.push({ block, inElse: false, elseIf: true });
      }
    } else if (word === "end") {
      if (stack.length === 0) fail(line, "unexpected {{end}}");
      while (stack.pop()?.elseIf);
    } else {
      if (!body) fail(line, "missing value for command");
      into().push({ kind: "action", pipe: pipeOf(lex(body, line), line), line });
    }
  }
  if (stack.length > 0) fail(src.split("\n").length, `unexpected EOF: {{${stack.at(-1)!.block.kind}}} has no {{end}}`);
  return root;
}

/** The variable names and the objects on their way: .Request is one when .Request.Number is. */
function known(vars: string[]): Set<string> {
  const out = new Set<string>();
  for (const v of vars) {
    const parts = v.split(".");
    for (let n = 2; n <= parts.length; n++) out.add(parts.slice(0, n).join("."));
  }
  return out;
}

type Seen = { path: string } | { item: string } | "any";

/** Says the first field the template names that isn't one of vars, or a range over anything but lists. */
export function checkFields(nodes: Node[], vars: string[], lists: string[]) {
  const names = known(vars);
  const scope: Record<string, Seen>[] = [{ $: { path: "" } }];
  const look = (n: string) => scope.findLast((s) => n in s)?.[n] ?? "any";
  const field = (line: number, on: Seen, path: string[]): Seen => {
    let at = on;
    for (const name of path) {
      if (at === "any") return "any";
      if ("item" in at) fail(line, `the items of ${at.item} are text: they have no field ${name}`);
      const p = (at as { path: string }).path + "." + name;
      if (!names.has(p)) {
        const parent = (at as { path: string }).path;
        fail(
          line,
          parent === ""
            ? `${p} is not a variable of this kind`
            : `${parent} has no field ${name}: ${p} is not a variable of this kind`,
        );
      }
      at = { path: p };
    }
    return at;
  };
  const operand = (dot: Seen, o: Operand, line: number): Seen => {
    switch (o.kind) {
      case "field":
        return field(line, o.on ? operand(dot, o.on, line) : dot, o.path);
      case "dot":
        return dot;
      case "var":
        return field(line, look(o.name), o.path);
      case "pipe":
        return pipe(dot, o.pipe);
    }
    return "any";
  };
  const pipe = (dot: Seen, p: Pipe): Seen => {
    let out: Seen = "any";
    for (const cmd of p.cmds) for (const o of cmd) out = operand(dot, o, p.line);
    if (p.cmds.length !== 1 || p.cmds[0]!.length !== 1) out = "any";
    for (const d of p.decl ?? []) scope.at(-1)![d] = out;
    return out;
  };
  let ranges = 0;
  const walk = (dot: Seen, list: Node[]) => {
    scope.push({});
    for (const n of list) {
      if (n.kind === "action") pipe(dot, n.pipe);
      else if (n.kind === "if") {
        pipe(dot, n.pipe);
        walk(dot, n.body);
        walk(dot, n.otherwise);
      } else if (n.kind === "with") {
        scope.push({});
        walk(pipe(dot, n.pipe), n.body);
        scope.pop();
        walk(dot, n.otherwise);
      } else if (n.kind === "range") {
        scope.push({});
        const over = pipe(dot, { ...n.pipe, decl: undefined });
        if (over === "any" || !("path" in over) || !lists.includes(over.path))
          fail(n.line, "range goes over a list variable of this kind, such as .Approval.Reasons of approvals");
        // A list in a list writes its length squared.
        if (ranges > 0) fail(n.line, "a range can't be inside another");
        const item: Seen = { item: (over as { path: string }).path };
        const decl = n.pipe.decl ?? [];
        if (decl.length) scope.at(-1)![decl.at(-1)!] = item;
        if (decl.length === 2) scope.at(-1)![decl[0]!] = "any";
        ranges++;
        walk(item, n.body);
        ranges--;
        scope.pop();
        walk(dot, n.otherwise);
      }
    }
    scope.pop();
  };
  walk({ path: "" }, nodes);
}

const truthy = (v: unknown) =>
  !(v === undefined || v === null || v === false || v === 0 || v === "" || (Array.isArray(v) && v.length === 0));

const text = (v: unknown): string =>
  v === undefined || v === null
    ? "<no value>"
    : Array.isArray(v)
      ? `[${v.map(text).join(" ")}]`
      : typeof v === "string"
        ? v
        : typeof v === "number" || typeof v === "boolean"
          ? String(v)
          : "map[]";

const escapeHtml = (s: string) =>
  s
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&#34;")
    .replaceAll("'", "&#39;");

const MaxWidth = 100;

/** Refuses a width or precision past MaxWidth, or one taken from the data, as the server's printf. */
function checkPrintf(format: string, line: number) {
  for (let i = 0; i < format.length; i++) {
    if (format[i] !== "%") continue;
    for (i++; i < format.length && "+-# 0123456789.*[]".includes(format[i]!); i++) {
      const c = format[i]!;
      if (c === "*")
        fail(
          line,
          "error calling printf: printf takes its width and precision from the template, not from * and the data",
        );
      else if (c === "[") {
        const j = format.indexOf("]", i);
        if (j > i) i = j;
      } else if (c >= "1" && c <= "9") {
        let j = i;
        while (j < format.length && format[j]! >= "0" && format[j]! <= "9") j++;
        if (Number(format.slice(i, j)) > MaxWidth)
          fail(line, `error calling printf: printf pads to ${MaxWidth} characters at most`);
        i = j - 1;
      }
    }
  }
}

function printf(format: string, args: unknown[]): string {
  let i = 0;
  return format.replace(/%(-?)(\d*)([svdq%])/g, (_, left: string, width: string, verb: string) => {
    if (verb === "%") return "%";
    const v = args[i++];
    const s = verb === "q" ? JSON.stringify(text(v)) : text(v);
    const w = Number(width || 0);
    return left ? s.padEnd(w) : s.padStart(w);
  });
}

function call(name: string, args: unknown[], line: number): unknown {
  const [a, b] = args;
  const cmp = (f: (x: number | string, y: number | string) => boolean) => f(a as number | string, b as number | string);
  switch (name) {
    case "and":
      return args.find((x) => !truthy(x)) ?? args.at(-1);
    case "or":
      return args.find(truthy) ?? args.at(-1);
    case "not":
      return !truthy(a);
    case "len":
      return Array.isArray(a) || typeof a === "string" ? a.length : fail(line, "len of a value that has none");
    case "index": {
      const list = a as unknown[];
      const n = b as number;
      if (!Array.isArray(list) || n < 0 || n >= list.length)
        fail(line, `error calling index: index out of range: ${String(n)}`);
      return list[n];
    }
    case "slice":
      return typeof a === "string" || Array.isArray(a) ? a.slice(b as number, args[2] as number | undefined) : a;
    case "print":
      return args.map(text).join("");
    case "println":
      return args.map(text).join(" ") + "\n";
    case "printf":
      checkPrintf(String(a), line);
      return printf(String(a), args.slice(1));
    case "html":
      return escapeHtml(args.map(text).join(""));
    case "js":
      return JSON.stringify(args.map(text).join("")).slice(1, -1);
    case "urlquery":
      return encodeURIComponent(args.map(text).join(""));
    case "eq":
      return args.slice(1).some((x) => x === a);
    case "ne":
      return a !== b;
    case "lt":
      return cmp((x, y) => x < y);
    case "le":
      return cmp((x, y) => x <= y);
    case "gt":
      return cmp((x, y) => x > y);
    case "ge":
      return cmp((x, y) => x >= y);
    case "date":
      return a ?? "";
    case "join":
      return Array.isArray(a) ? a.join(String(b)) : fail(line, "join takes a list");
  }
  return fail(line, `function "${name}" not defined`);
}

/** Writes a parsed template with a kind's values; esc escapes what actions write, for HTML. */
export function execute(nodes: Node[], values: Values, esc: (s: string) => string = (s) => s): string {
  const scope: Record<string, unknown>[] = [{ $: { path: "" } satisfies Dot }];
  const asDot = (v: unknown): Dot => (v && typeof v === "object" && "path" in v ? (v as Dot) : { value: v });
  const resolve = (on: Dot, path: string[], line: number): unknown => {
    if (path.length === 0) return "value" in on ? on.value : on;
    if ("value" in on) return fail(line, `can't evaluate field ${path[0]} in type string`);
    const full = on.path + "." + path.join(".");
    if (full in values) return values[full];
    return { path: full } satisfies Dot;
  };
  const operand = (dot: Dot, o: Operand, line: number): unknown => {
    switch (o.kind) {
      case "field":
        return resolve(o.on ? asDot(operand(dot, o.on, line)) : dot, o.path, line);
      case "dot":
        return "value" in dot ? dot.value : dot;
      case "var":
        return resolve(asDot(scope.findLast((s) => o.name in s)?.[o.name]), o.path, line);
      case "pipe":
        return pipe(dot, o.pipe);
      case "ident":
        return call(o.name, [], line);
    }
    return o.value;
  };
  const pipe = (dot: Dot, p: Pipe): unknown => {
    let last: unknown;
    p.cmds.forEach((cmd, i) => {
      const head = cmd[0]!;
      if (head.kind === "ident") {
        const args = cmd.slice(1).map((o) => operand(dot, o, p.line));
        last = call(head.name, i > 0 ? [...args, last] : args, p.line);
      } else last = operand(dot, head, p.line);
    });
    for (const d of p.decl ?? []) scope.at(-1)![d] = last;
    return last;
  };
  const shown = (v: unknown) => (v && typeof v === "object" && "path" in v ? "map[]" : text(v));
  const run = (dot: Dot, list: Node[]): string => {
    scope.push({});
    let out = "";
    for (const n of list) {
      if (n.kind === "text") out += n.text;
      else if (n.kind === "action") {
        const v = pipe(dot, n.pipe);
        if (!n.pipe.decl) out += esc(shown(v));
      } else if (n.kind === "if") out += truthy(pipe(dot, n.pipe)) ? run(dot, n.body) : run(dot, n.otherwise);
      else if (n.kind === "with") {
        const v = pipe(dot, n.pipe);
        out += truthy(v) ? run(asDot(v), n.body) : run(dot, n.otherwise);
      } else {
        const over = pipe(dot, { ...n.pipe, decl: undefined });
        const items = Array.isArray(over) ? over : [];
        if (items.length === 0) out += run(dot, n.otherwise);
        items.forEach((item, i) => {
          scope.push({});
          const decl = n.pipe.decl ?? [];
          if (decl.length) scope.at(-1)![decl.at(-1)!] = item;
          if (decl.length === 2) scope.at(-1)![decl[0]!] = i;
          out += run({ value: item }, n.body);
          scope.pop();
        });
      }
    }
    scope.pop();
    return out;
  };
  return run({ path: "" }, nodes);
}

export const html = escapeHtml;
