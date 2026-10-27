// A small subset of CEL (https://cel.dev) for form rules, evaluated in the
// browser for instant feedback. The server evaluates the same rules on preview
// and submit. Supported: literals, lists, member and index access, ! - * / % +
// < <= > >= == != in && || ?:, size(), and string startsWith/endsWith/contains.
// Unlike full CEL, a missing field evaluates to null instead of an error.
import { toText } from "@/lib/utils";

type Node =
  | { k: "lit"; v: unknown }
  | { k: "id"; name: string }
  | { k: "list"; items: Node[] }
  | { k: "member"; obj: Node; name: string }
  | { k: "index"; obj: Node; index: Node }
  | { k: "call"; fn: string; target: Node | null; args: Node[] }
  | { k: "unary"; op: string; arg: Node }
  | { k: "binary"; op: string; left: Node; right: Node }
  | { k: "cond"; test: Node; then: Node; else: Node };

type Token = { t: "num" | "str" | "id" | "op"; v: string };

const OPS = [
  "&&",
  "||",
  "==",
  "!=",
  "<=",
  ">=",
  "<",
  ">",
  "!",
  "+",
  "-",
  "*",
  "/",
  "%",
  "?",
  ":",
  "(",
  ")",
  "[",
  "]",
  ".",
  ",",
];

function tokenize(src: string): Token[] {
  const out: Token[] = [];
  let i = 0;
  while (i < src.length) {
    const c = src[i]!;
    if (/\s/.test(c)) {
      i++;
      continue;
    }
    if (/[0-9]/.test(c)) {
      const m = /^[0-9]+(\.[0-9]+)?/.exec(src.slice(i))!;
      out.push({ t: "num", v: m[0] });
      i += m[0].length;
      continue;
    }
    if (c === '"' || c === "'") {
      let j = i + 1;
      let s = "";
      while (j < src.length && src[j] !== c) {
        if (src[j] === "\\" && j + 1 < src.length) j++;
        s += src[j];
        j++;
      }
      if (j >= src.length) throw new Error("Unterminated string");
      out.push({ t: "str", v: s });
      i = j + 1;
      continue;
    }
    if (/[A-Za-z_]/.test(c)) {
      const m = /^[A-Za-z_][A-Za-z0-9_]*/.exec(src.slice(i))!;
      out.push({ t: "id", v: m[0] });
      i += m[0].length;
      continue;
    }
    const op = OPS.find((o) => src.startsWith(o, i));
    if (!op) throw new Error(`Unexpected “${c}”`);
    out.push({ t: "op", v: op });
    i += op.length;
  }
  return out;
}

function parse(src: string): Node {
  const tokens = tokenize(src);
  let pos = 0;
  const peek = () => tokens[pos];
  const isOp = (v: string) => peek()?.t === "op" && peek()?.v === v;
  const eat = (v: string) => {
    if (!isOp(v)) throw new Error(`Expected “${v}”`);
    pos++;
  };

  const expr = (): Node => {
    const test = or();
    if (isOp("?")) {
      pos++;
      const then = expr();
      eat(":");
      return { k: "cond", test, then, else: expr() };
    }
    return test;
  };
  const binary = (next: () => Node, ops: string[]) => (): Node => {
    let left = next();
    for (;;) {
      const tok = peek();
      const op =
        tok && ((tok.t === "op" && ops.includes(tok.v)) || (tok.t === "id" && tok.v === "in" && ops.includes("in")));
      if (!op) return left;
      pos++;
      left = { k: "binary", op: tok.v, left, right: next() };
    }
  };
  const unary = (): Node => {
    if (isOp("!") || isOp("-")) {
      const op = tokens[pos++]!.v;
      return { k: "unary", op, arg: unary() };
    }
    return postfix();
  };
  const args = (): Node[] => {
    const list: Node[] = [];
    eat("(");
    if (!isOp(")")) {
      list.push(expr());
      while (isOp(",")) {
        pos++;
        list.push(expr());
      }
    }
    eat(")");
    return list;
  };
  const postfix = (): Node => {
    let node = primary();
    for (;;) {
      if (isOp(".")) {
        pos++;
        const name = tokens[pos++];
        if (name?.t !== "id") throw new Error("Expected a field name");
        node = isOp("(")
          ? { k: "call", fn: name.v, target: node, args: args() }
          : { k: "member", obj: node, name: name.v };
      } else if (isOp("[")) {
        pos++;
        const index = expr();
        eat("]");
        node = { k: "index", obj: node, index };
      } else return node;
    }
  };
  const primary = (): Node => {
    const tok = tokens[pos++];
    if (!tok) throw new Error("Unexpected end of rule");
    if (tok.t === "num") return { k: "lit", v: Number(tok.v) };
    if (tok.t === "str") return { k: "lit", v: tok.v };
    if (tok.t === "id") {
      if (tok.v === "true") return { k: "lit", v: true };
      if (tok.v === "false") return { k: "lit", v: false };
      if (tok.v === "null") return { k: "lit", v: null };
      if (isOp("(")) return { k: "call", fn: tok.v, target: null, args: args() };
      return { k: "id", name: tok.v };
    }
    if (tok.v === "(") {
      const e = expr();
      eat(")");
      return e;
    }
    if (tok.v === "[") {
      const items: Node[] = [];
      if (!isOp("]")) {
        items.push(expr());
        while (isOp(",")) {
          pos++;
          items.push(expr());
        }
      }
      eat("]");
      return { k: "list", items };
    }
    throw new Error(`Unexpected “${tok.v}”`);
  };
  const mul = binary(unary, ["*", "/", "%"]);
  const add = binary(mul, ["+", "-"]);
  const rel = binary(add, ["<", "<=", ">", ">=", "==", "!=", "in"]);
  const and = binary(rel, ["&&"]);
  const or = binary(and, ["||"]);

  const node = expr();
  if (pos < tokens.length) throw new Error(`Unexpected “${tokens[pos]!.v}”`);
  return node;
}

const get = (obj: unknown, key: string | number): unknown =>
  obj !== null && typeof obj === "object" ? ((obj as Record<string | number, unknown>)[key] ?? null) : null;

const equal = (a: unknown, b: unknown): boolean => {
  if (Array.isArray(a) && Array.isArray(b)) return a.length === b.length && a.every((x, i) => equal(x, b[i]));
  return (a ?? null) === (b ?? null);
};

const toStr = toText;

const size = (v: unknown) =>
  typeof v === "string" || Array.isArray(v) ? v.length : v && typeof v === "object" ? Object.keys(v).length : 0;

function evaluate(n: Node, ctx: Record<string, unknown>): unknown {
  switch (n.k) {
    case "lit":
      return n.v;
    case "id":
      return ctx[n.name] ?? null;
    case "list":
      return n.items.map((i) => evaluate(i, ctx));
    case "member":
      return get(evaluate(n.obj, ctx), n.name);
    case "index": {
      const i = evaluate(n.index, ctx);
      return get(evaluate(n.obj, ctx), typeof i === "number" ? i : String(i));
    }
    case "unary": {
      const v = evaluate(n.arg, ctx);
      return n.op === "!" ? !v : -Number(v);
    }
    case "cond":
      return evaluate(n.test, ctx) ? evaluate(n.then, ctx) : evaluate(n.else, ctx);
    case "call": {
      const args = n.args.map((a) => evaluate(a, ctx));
      if (n.target === null) {
        if (n.fn === "size") return size(args[0]);
        if (n.fn === "int") return Math.trunc(Number(args[0]));
        if (n.fn === "string") return toStr(args[0]);
        throw new Error(`Unknown function ${n.fn}()`);
      }
      const target = evaluate(n.target, ctx);
      const s = typeof target === "string" ? target : "";
      const a = typeof args[0] === "string" ? args[0] : "";
      if (n.fn === "startsWith") return s.startsWith(a);
      if (n.fn === "endsWith") return s.endsWith(a);
      if (n.fn === "contains") return Array.isArray(target) ? target.some((x) => equal(x, args[0])) : s.includes(a);
      if (n.fn === "size") return size(target);
      throw new Error(`Unknown method ${n.fn}()`);
    }
    case "binary": {
      if (n.op === "&&") return Boolean(evaluate(n.left, ctx)) && Boolean(evaluate(n.right, ctx));
      if (n.op === "||") return Boolean(evaluate(n.left, ctx)) || Boolean(evaluate(n.right, ctx));
      const l = evaluate(n.left, ctx);
      const r = evaluate(n.right, ctx);
      switch (n.op) {
        case "==":
          return equal(l, r);
        case "!=":
          return !equal(l, r);
        case "in":
          return Array.isArray(r) ? r.some((x) => equal(x, l)) : r !== null && typeof r === "object" && String(l) in r;
        case "<":
        case "<=":
        case ">":
        case ">=": {
          if (l === null || r === null) return false;
          const [x, y] = typeof l === "string" && typeof r === "string" ? [l, r] : [Number(l), Number(r)];
          return n.op === "<" ? x < y : n.op === "<=" ? x <= y : n.op === ">" ? x > y : x >= y;
        }
        case "+":
          if (typeof l === "string" || typeof r === "string") return toStr(l) + toStr(r);
          if (Array.isArray(l) && Array.isArray(r)) return [...(l as unknown[]), ...(r as unknown[])];
          return Number(l) + Number(r);
        case "-":
          return Number(l) - Number(r);
        case "*":
          return Number(l) * Number(r);
        case "/":
          return Number(l) / Number(r);
        case "%":
          return Number(l) % Number(r);
      }
      throw new Error(`Unknown operator ${n.op}`);
    }
  }
}

const cache = new Map<string, Node>();

/** Evaluate a rule. Throws on syntax errors and unknown functions. */
export function celEval(expression: string, ctx: Record<string, unknown>): unknown {
  let ast = cache.get(expression);
  if (!ast) {
    ast = parse(expression);
    cache.set(expression, ast);
  }
  return evaluate(ast, ctx);
}

/** Evaluate a rule as a boolean, falling back when it is empty or broken. */
export function celBool(expression: string | undefined, ctx: Record<string, unknown>, fallback: boolean): boolean {
  if (!expression) return fallback;
  try {
    return Boolean(celEval(expression, ctx));
  } catch (e) {
    if (import.meta.env.DEV) console.warn(`Form rule “${expression}” failed:`, e);
    return fallback;
  }
}
