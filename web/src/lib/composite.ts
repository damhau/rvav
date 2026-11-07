// A composite blueprint's steps, read from the definition (ADR-0022): the
// groups they run in, what each runs and which earlier steps it uses, with
// the server's structural checks (CEL itself is checked when saved; a REST
// call's host and secret, and the users a wait for a person names, too, with
// the lists only the server has).
import type { FieldError } from "@/lib/api/types";

export interface CompositeStepView {
  key: string;
  title?: string;
  kind: "item" | "awx" | "workflow" | "rest" | "ask" | "wait" | "poll" | "branch" | "notify" | "?";
  target: string;
  /** A wait for a person, in words. A requester's topology names neither a group nor anyone. */
  ask?: { who: string; wants: string; timeout?: string };
  /** A wait for a time: how long, or the CEL that says until when. */
  wait?: { for?: string; until?: string };
  /** A wait for a condition: what it waits for, how often it checks and for how long. */
  poll?: { until: string; every: string; timeout: string };
  /** A branch's sides: its cases in order, then what runs otherwise. */
  sides?: CompositeSideView[];
  /** A message: who it tells, and what it says. A requester's topology names no group or person, and has no text. */
  notify?: { who: string; message?: string };
  when?: string;
  uses: string[];
  undo?: string;
  pointer: string;
}

/** One side of a branch. */
export interface CompositeSideView {
  /** A case's title, "Case 2", or "Otherwise". */
  label: string;
  /** A case's condition; undefined for Otherwise. */
  when?: string;
  /** Which it is, as a run says which it took (`Step.branch_case`): 1 for the first case, 0 for Otherwise. */
  taken: number;
  steps: CompositeStepView[];
}

export interface CompositeView {
  groups: CompositeStepView[][];
  problems: FieldError[];
}

type Raw = Record<string, unknown>;

const keyPattern = /^[a-z][a-z0-9_]{0,40}$/;
const stepRef = /\bsteps\.([a-z][a-z0-9_]*)/g;

const str = (v: unknown) => (typeof v === "string" ? v : "");

/** A piece of a REST call's URL template: text as written, or a CEL expression (`${...}`). */
export type UrlPart = { text: string } | { expr: string };

/** Where the expression opening a text ends: the index of its `}`, past the braces and strings inside it. */
function exprEnd(s: string): number {
  let depth = 0;
  let quote = "";
  for (let i = 0; i < s.length; i++) {
    const c = s[i]!;
    if (quote) {
      if (c === "\\") i++;
      else if (c === quote) quote = "";
    } else if (c === "'" || c === '"') quote = c;
    else if (c === "{") depth++;
    else if (c === "}") {
      if (depth === 0) return i;
      depth--;
    }
  }
  return -1;
}

/** A URL template's parts, as the server reads them (restcall.ParseURL), or what is wrong with it. */
export function parseUrl(template: string): { parts: UrlPart[]; error?: string } {
  const parts: UrlPart[] = [];
  let rest = template;
  for (;;) {
    const i = rest.indexOf("${");
    if (i < 0) {
      if (rest) parts.push({ text: rest });
      return { parts };
    }
    if (i > 0) parts.push({ text: rest.slice(0, i) });
    const end = exprEnd(rest.slice(i + 2));
    if (end < 0) return { parts: [], error: "a ${ isn't closed" };
    const expr = rest.slice(i + 2, i + 2 + end).trim();
    if (!expr) return { parts: [], error: "a ${} is empty" };
    parts.push({ expr });
    rest = rest.slice(i + 2 + end + 1);
  }
}

const urlExpressions = (template: string) => parseUrl(template).parts.flatMap((p) => ("expr" in p ? [p.expr] : []));

/** The CEL of a REST call: its URL's expressions and its body. */
function restExpressions(call: Raw | undefined): string[] {
  if (!call) return [];
  return [...urlExpressions(str(call.url)), ...(str(call.body) ? [str(call.body)] : [])];
}

/** What the server checks of a REST call on its own (core.checkRESTCall). */
function checkRest(call: Raw, ptr: string, err: (field: string, code: string, message: string) => void) {
  if (!["GET", "POST", "PUT", "PATCH", "DELETE"].includes(str(call.method)))
    err(`${ptr}/method`, "invalid", "GET, POST, PUT, PATCH or DELETE.");
  const url = str(call.url);
  const { parts, error } = parseUrl(url);
  // Its scheme and host, whatever the expressions give.
  const skeleton = parts.map((p) => ("expr" in p ? "x" : p.text)).join("");
  let parsed: URL | null = null;
  try {
    parsed = new URL(skeleton);
  } catch {
    // Said below.
  }
  const scheme = url.indexOf("://");
  const firstExpr = url.indexOf("${");
  const afterHost = url.slice(scheme + 3).search(/[/?#]/);
  if (!url) err(`${ptr}/url`, "required", "Give the URL to call.");
  else if (error) err(`${ptr}/url`, "invalid", `This URL can't be read: ${error}.`);
  else if (!parsed || !["http:", "https:"].includes(parsed.protocol) || !parsed.host)
    err(`${ptr}/url`, "invalid", "Give an http or https URL.");
  else if (firstExpr >= 0 && (afterHost < 0 || firstExpr < scheme + 3 + afterHost))
    err(`${ptr}/url`, "invalid", "Expressions may fill the URL's path and query, not its scheme, host or port.");
  else if (parsed.username || parsed.password)
    err(`${ptr}/url`, "invalid", "Put credentials in a stored secret, not in the URL.");
  if (str(call.body) && call.method === "GET") err(`${ptr}/body`, "invalid", "A GET has no body.");
  const accept = Array.isArray(call.accept) ? call.accept : [];
  accept.forEach((status, i) => {
    if (typeof status !== "number" || status < 100 || status > 599)
      err(`${ptr}/accept/${i}`, "invalid", "An HTTP status, 100 to 599.");
  });
}

/** A Go duration's length in milliseconds, or null if it isn't one (time.ParseDuration). */
export function goDuration(v: string): number | null {
  const unit: Record<string, number> = { ns: 1e-6, us: 1e-3, µs: 1e-3, ms: 1, s: 1e3, m: 6e4, h: 36e5 };
  if (!/^[+-]?(\d+(\.\d*)?(ns|us|µs|ms|s|m|h)|\.\d+(ns|us|µs|ms|s|m|h))+$/.test(v)) return null;
  let total = 0;
  for (const m of v.matchAll(/([\d.]+)(ns|us|µs|ms|s|m|h)/g)) total += Number(m[1]) * unit[m[2]!]!;
  return v.startsWith("-") ? -total : total;
}

/** A question's value: what is wrong with it, if anything (core.askValueProblem). */
function askValueProblem(f: Raw, v: unknown): string {
  const options = Array.isArray(f.options) ? (f.options as unknown[]).map(String) : [];
  switch (f.type) {
    case "string":
      return typeof v === "string" ? "" : "Give text.";
    case "number":
      return typeof v === "number" ? "" : "Give a number.";
    case "boolean":
      return typeof v === "boolean" ? "" : "Give true or false.";
    case "choice":
      return typeof v === "string" && options.includes(v) ? "" : `Choose one of: ${options.join(", ")}.`;
    default:
      return "";
  }
}

/** What the server checks of a wait for a person on its own (core.checkAsk). */
function checkAsk(ask: Raw, ptr: string, err: (field: string, code: string, message: string) => void) {
  const named = Array.isArray(ask.approvers) ? ask.approvers : [];
  if (!str(ask.group) && named.length === 0 && !ask.requester && !ask.owner)
    err(ptr, "required", "Say who is asked: an OIDC group, approvers, the requester or the owner.");
  if (str(ask.message).length > 2000) err(`${ptr}/message`, "too_long", "At most 2000 characters.");
  if (str(ask.timeout)) {
    const d = goDuration(str(ask.timeout));
    if (d === null || d < 1000) err(`${ptr}/timeout`, "invalid", "A duration such as 48h.");
  }
  if (str(ask.on_timeout)) {
    if (!["fail", "continue", "escalate"].includes(str(ask.on_timeout)))
      err(`${ptr}/on_timeout`, "enum", "fail, continue or escalate.");
    if (!str(ask.timeout)) err(`${ptr}/on_timeout`, "required", "What happens on time-out needs a timeout.");
  }
  const to = (ask.escalate_to ?? {}) as Raw;
  if (ask.on_timeout === "escalate" && !str(to.group) && !(Array.isArray(to.approvers) && to.approvers.length > 0))
    err(`${ptr}/escalate_to`, "required", "Say whom to escalate to: an OIDC group or approvers.");
  const form = Array.isArray(ask.form) ? (ask.form as Raw[]) : [];
  if (form.length > 20) err(`${ptr}/form`, "too_many", "At most 20 questions.");
  const seen = new Set<string>();
  form.forEach((f, i) => {
    const at = `${ptr}/form/${i}`;
    const key = str(f.key);
    if (!keyPattern.test(key)) err(`${at}/key`, "pattern", "A key of lower-case letters, digits and underscores.");
    else if (seen.has(key)) err(`${at}/key`, "duplicate", `Another question has the key ${key}.`);
    seen.add(key);
    if (!str(f.label).trim()) err(`${at}/label`, "required", "Say what it asks.");
    // The answer goes through Temporal's history to every later step.
    if (f.secret || f.type === "secret" || f.type === "password") {
      err(
        at,
        "secret",
        "A step's form can't ask for a secret: its answer travels through Temporal's history to every later step. Keep secrets in the request's form or a stored secret.",
      );
      return;
    }
    if (!["string", "number", "boolean", "choice"].includes(str(f.type))) {
      err(`${at}/type`, "enum", "string, number, boolean or choice.");
      return;
    }
    const options = Array.isArray(f.options) ? f.options : [];
    if (f.type === "choice" && options.length === 0) err(`${at}/options`, "required", "List what it may be.");
    if (f.type !== "choice" && options.length > 0) err(`${at}/options`, "invalid", "Only a choice has options.");
    if (f.default !== undefined && f.default !== null) {
      const why = askValueProblem(f, f.default);
      if (why) err(`${at}/default`, "invalid", why);
    }
  });
}

/** The shortest interval a poll may check at, in milliseconds (blueprint.MinPollEvery). */
export const minPollEvery = 60_000;

/** What the server checks of a wait for a time (core.checkWait). */
function checkWait(w: Raw, ptr: string, err: (field: string, code: string, message: string) => void) {
  const forText = str(w.for);
  const until = str(w.until);
  if (!forText && !until) err(ptr, "required", "Say how long it waits, or until when.");
  if (forText && until) err(ptr, "one_of", "A wait is for a duration or until a time, not both.");
  if (forText) {
    const d = goDuration(forText);
    if (d === null || d < 1000) err(`${ptr}/for`, "invalid", "A duration such as 72h.");
  }
}

/** What the server checks of a wait for a condition (core.checkPoll). */
function checkPoll(p: Raw, ptr: string, key: string, err: (field: string, code: string, message: string) => void) {
  const call = p.rest as Raw | undefined;
  if (!call) err(`${ptr}/rest`, "required", "Give the call it repeats.");
  else {
    checkRest(call, `${ptr}/rest`, err);
    if (call.method === "POST" || call.method === "PATCH")
      err(
        `${ptr}/rest/method`,
        "invalid",
        "A poll repeats its call, so it may only GET, PUT or DELETE: a POST or a PATCH may do something each time.",
      );
  }
  const until = str(p.until);
  if (!until) err(`${ptr}/until`, "required", "Say what it waits for: a condition on the call's answer.");
  else if (![...until.matchAll(stepRef)].some((m) => m[1] === key))
    err(
      `${ptr}/until`,
      "invalid",
      `Read the call's answer, as steps.${key}.json or .status: nothing else changes during the wait.`,
    );
  const every = goDuration(str(p.every));
  if (!str(p.every)) err(`${ptr}/every`, "required", "Say how often it checks: a duration such as 10m.");
  else if (every === null) err(`${ptr}/every`, "invalid", "A duration such as 10m.");
  else if (every < minPollEvery)
    err(`${ptr}/every`, "invalid", "At least 1m: each check calls that API and adds to the run's history.");
  const timeout = goDuration(str(p.timeout));
  if (!str(p.timeout)) err(`${ptr}/timeout`, "required", "Give it a deadline: a duration such as 72h.");
  else if (timeout === null) err(`${ptr}/timeout`, "invalid", "A duration such as 72h.");
  else if (every !== null && every > 0 && timeout < every)
    err(`${ptr}/timeout`, "invalid", "At least as long as how often it checks.");
  if (str(p.on_timeout) && !["fail", "continue"].includes(str(p.on_timeout)))
    err(`${ptr}/on_timeout`, "enum", "fail or continue.");
}

const timeoutWords: Record<string, string> = {
  fail: "then the step fails",
  continue: "then it goes on",
  escalate: "then it escalates",
};

/** A wait for a person in words, from what a definition or a requester's topology has of it. */
function askWords(ask: Raw): NonNullable<CompositeStepView["ask"]> {
  const named = Array.isArray(ask.approvers) ? ask.approvers.length : 0;
  const who = [
    ask.requester ? "the requester" : "",
    ask.owner ? "the owner" : "",
    str(ask.group),
    named === 0 ? "" : named === 1 ? "1 person" : `${named} people`,
  ].filter(Boolean);
  const questions = Array.isArray(ask.form) ? ask.form.length : 0;
  return {
    who: who.join(", ") || "a person",
    wants: questions === 0 ? "to approve or reject" : questions === 1 ? "1 answer" : `${questions} answers`,
    timeout: str(ask.timeout)
      ? `${str(ask.timeout)}, ${timeoutWords[str(ask.on_timeout)] ?? timeoutWords.fail}`
      : undefined,
  };
}

/** The longest a message's text may be (core.maxStepMessage). */
export const maxStepMessage = 2000;

/** What the server checks of a branch on its own (core.checkBranch). */
function checkBranch(b: Raw, ptr: string, err: (field: string, code: string, message: string) => void) {
  const cases = Array.isArray(b.cases) ? (b.cases as Raw[]) : [];
  if (cases.length === 0)
    err(`${ptr}/cases`, "required", "Give it at least one case: a condition and the steps it runs.");
  cases.forEach((c, i) => {
    if (!str(c.when)) err(`${ptr}/cases/${i}/when`, "required", "Say when this case is taken: a CEL condition.");
  });
}

/** What the server checks of a message on its own (core.checkNotify). */
function checkNotify(n: Raw, ptr: string, err: (field: string, code: string, message: string) => void) {
  const groups = Array.isArray(n.groups) ? (n.groups as unknown[]) : [];
  const users = Array.isArray(n.users) ? (n.users as unknown[]) : [];
  if (!n.requester && !n.owner && groups.length === 0 && users.length === 0)
    err(ptr, "required", "Say who is told: the requester, the owner, groups or people.");
  groups.forEach((g, i) => {
    if (!str(g).trim()) err(`${ptr}/groups/${i}`, "required", "Name the OIDC group.");
  });
  const message = str(n.message);
  if (!message.trim()) {
    err(`${ptr}/message`, "required", "Say what it says.");
    return;
  }
  if (message.length > maxStepMessage) err(`${ptr}/message`, "too_long", `At most ${maxStepMessage} characters.`);
  const { error } = parseUrl(message);
  if (error) err(`${ptr}/message`, "invalid", `This message can't be read: ${error}.`);
}

/** A message in words, from what a definition or a requester's topology has of it. */
function notifyWords(n: Raw): NonNullable<CompositeStepView["notify"]> {
  const groups = Array.isArray(n.groups) ? (n.groups as unknown[]).map(str).filter(Boolean) : [];
  const users = Array.isArray(n.users) ? n.users.length : 0;
  const who = [
    n.requester ? "the requester" : "",
    n.owner ? "the owner" : "",
    ...groups,
    users === 0 ? "" : users === 1 ? "1 person" : `${users} people`,
  ].filter(Boolean);
  return { who: who.join(", ") || "other people", message: str(n.message) || undefined };
}

/** A REST call on one line. */
const restLine = (call: Raw) => `${str(call.method)} ${str(call.url)}`.trim();

function expressions(s: Raw): string[] {
  const out: string[] = [];
  if (str(s.when)) out.push(str(s.when));
  for (const v of Object.values((s.with as Raw | undefined) ?? {})) if (typeof v === "string") out.push(v);
  out.push(...restExpressions(s.rest as Raw | undefined));
  const wait = s.wait as Raw | undefined;
  if (str(wait?.until)) out.push(str(wait?.until));
  out.push(...restExpressions((s.poll as Raw | undefined)?.rest as Raw | undefined));
  const branch = s.branch as Raw | undefined;
  for (const c of Array.isArray(branch?.cases) ? (branch.cases as Raw[]) : []) if (str(c.when)) out.push(str(c.when));
  const notify = s.notify as Raw | undefined;
  if (notify) out.push(...urlExpressions(str(notify.message)));
  return out;
}

/** The CEL of a step's undo: it runs after the step, so it may read its outputs too. */
function undoExpressions(s: Raw): string[] {
  const undo = s.undo as Raw | undefined;
  const out: string[] = [];
  for (const v of Object.values((undo?.with as Raw | undefined) ?? {})) if (typeof v === "string") out.push(v);
  out.push(...restExpressions(undo?.rest as Raw | undefined));
  return out;
}

/** What a step on a side of a branch can read beyond the groups before it, and what it can't (core.sideScope). */
interface SideScope {
  open: Set<string>;
  others: Set<string>;
}

/** The steps of a composite blueprint and what is wrong with them (pointers under prefix). */
export function readComposite(blueprint: Raw, prefix = "/definition/blueprint"): CompositeView {
  const problems: FieldError[] = [];
  const err = (field: string, code: string, message: string) => problems.push({ field, code, message });
  const steps = Array.isArray(blueprint.steps) ? (blueprint.steps as Raw[]) : [];
  const seen = new Map<string, number>();
  const groups: CompositeStepView[][] = [];

  const readStep = (s: Raw, sp: string, g: number, side: SideScope | null): CompositeStepView => {
    const key = str(s.key);
    if (!keyPattern.test(key)) err(`${sp}/key`, "pattern", "A key of lower-case letters, digits and underscores.");
    else if (seen.has(key)) err(`${sp}/key`, "duplicate", `Another step has the key ${key}.`);
    const awx = s.awx as Raw | undefined;
    const rest = s.rest as Raw | undefined;
    const ask = s.ask as Raw | undefined;
    const wait = s.wait as Raw | undefined;
    const poll = s.poll as Raw | undefined;
    const branch = s.branch as Raw | undefined;
    const notify = s.notify as Raw | undefined;
    const kinds = [s.item, awx, s.workflow, rest, ask, wait, poll, branch, notify].filter(Boolean).length;
    if (kinds !== 1)
      err(sp, "one_of", "A step is one of: item, awx, workflow, rest, ask, wait, poll, branch or notify.");
    if (awx && !str(awx.job_template)) err(`${sp}/awx/job_template`, "required", "Name the AWX template.");
    if (rest) {
      checkRest(rest, `${sp}/rest`, err);
      if (s.with !== undefined)
        err(`${sp}/with`, "invalid", "A REST call has no inputs: its URL and body read inputs and steps themselves.");
    }
    if (ask) {
      checkAsk(ask, `${sp}/ask`, err);
      if (s.with !== undefined) err(`${sp}/with`, "invalid", "A wait for a person has no inputs.");
      if (s.undo !== undefined) err(`${sp}/undo`, "invalid", "A wait for a person has nothing to undo.");
    }
    if (wait) {
      checkWait(wait, `${sp}/wait`, err);
      if (s.with !== undefined) err(`${sp}/with`, "invalid", "A wait for a time has no inputs.");
      if (s.undo !== undefined) err(`${sp}/undo`, "invalid", "A wait for a time has nothing to undo.");
    }
    if (poll) {
      checkPoll(poll, `${sp}/poll`, key, err);
      if (s.with !== undefined)
        err(
          `${sp}/with`,
          "invalid",
          "A poll has no inputs: its URL, body and condition read inputs and steps themselves.",
        );
      if (s.undo !== undefined) err(`${sp}/undo`, "invalid", "A poll has nothing to undo.");
    }
    if (branch) {
      if (str(s.when)) err(`${sp}/when`, "invalid", "A branch has no condition of its own: its cases have.");
      if (s.with !== undefined)
        err(`${sp}/with`, "invalid", "A branch has no inputs: its conditions read inputs and steps themselves.");
      if (s.undo !== undefined)
        err(`${sp}/undo`, "invalid", "A branch has nothing to undo: only the steps that ran on its side are undone.");
      checkBranch(branch, `${sp}/branch`, err);
    }
    if (notify) {
      if (s.with !== undefined)
        err(`${sp}/with`, "invalid", "A message has no inputs: its text reads inputs and steps itself.");
      if (s.undo !== undefined) err(`${sp}/undo`, "invalid", "A message can't be unsent: it has nothing to undo.");
      checkNotify(notify, `${sp}/notify`, err);
    }
    const undo = s.undo as Raw | undefined;
    const undoRest = undo?.rest as Raw | undefined;
    if (
      undo &&
      !ask &&
      !wait &&
      !poll &&
      !branch &&
      !notify &&
      [str(undo.job_template), str(undo.workflow), undoRest].filter(Boolean).length !== 1
    )
      err(`${sp}/undo`, "one_of", "Undo with a job_template, a workflow or a rest call.");
    if (undoRest) {
      checkRest(undoRest, `${sp}/undo/rest`, err);
      if (undo?.with !== undefined)
        err(
          `${sp}/undo/with`,
          "invalid",
          "A REST call has no inputs: its URL and body read inputs and steps themselves.",
        );
    }
    const uses = new Set<string>();
    const read = (e: string, own: string | null) => {
      for (const m of e.matchAll(stepRef)) {
        const ref = m[1]!;
        if (ref === own) continue;
        uses.add(ref);
        if (side?.open.has(ref)) continue;
        if (side?.others.has(ref)) {
          err(sp, "other_side", `steps.${ref} is on another side of this branch: only one side runs.`);
          continue;
        }
        const gr = seen.get(ref);
        if (gr === undefined || gr >= g) err(sp, "unknown_step", `steps.${ref} is not an earlier step.`);
      }
    };
    for (const e of expressions(s)) read(e, null);
    for (const e of undoExpressions(s)) read(e, key);
    // A poll's condition reads its own answer.
    if (str(poll?.until)) read(str(poll?.until), key);
    if (key) seen.set(key, g);
    return {
      key,
      title: str(s.title) || str(awx?.title) || undefined,
      kind: s.item
        ? "item"
        : awx
          ? "awx"
          : s.workflow
            ? "workflow"
            : rest
              ? "rest"
              : ask
                ? "ask"
                : wait
                  ? "wait"
                  : poll
                    ? "poll"
                    : branch
                      ? "branch"
                      : notify
                        ? "notify"
                        : "?",
      target:
        str(s.item) ||
        str(awx?.job_template) ||
        str(s.workflow) ||
        (rest ? restLine(rest) : poll?.rest ? restLine(poll.rest as Raw) : ""),
      ask: ask ? askWords(ask) : undefined,
      wait: wait ? { for: str(wait.for) || undefined, until: str(wait.until) || undefined } : undefined,
      poll: poll
        ? {
            until: str(poll.until),
            every: str(poll.every),
            timeout: str(poll.timeout)
              ? `${str(poll.timeout)}, ${timeoutWords[str(poll.on_timeout)] ?? timeoutWords.fail}`
              : "",
          }
        : undefined,
      notify: notify ? notifyWords(notify) : undefined,
      sides: branch ? readSides(branch, key, `${sp}/branch`, g) : undefined,
      when: str(s.when) || undefined,
      uses: [...uses],
      undo: undo ? str(undo.job_template) || str(undo.workflow) || (undoRest ? restLine(undoRest) : "") : undefined,
      pointer: sp,
    };
  };

  /**
   * A branch's sides. Their steps come after it, which they read, and share its
   * group: only later groups read them, as they read a step `when` skipped.
   */
  const readSides = (b: Raw, branchKey: string, ptr: string, g: number): CompositeSideView[] => {
    const cases = Array.isArray(b.cases) ? (b.cases as Raw[]) : [];
    const lists = [
      ...cases.map((c, i) => ({
        label: str(c.title) || `Case ${i + 1}`,
        when: str(c.when),
        taken: i + 1,
        steps: Array.isArray(c.steps) ? (c.steps as Raw[]) : [],
        at: `${ptr}/cases/${i}/steps`,
      })),
      {
        label: "Otherwise",
        when: undefined,
        taken: 0,
        steps: Array.isArray(b.else) ? (b.else as Raw[]) : [],
        at: `${ptr}/else`,
      },
    ];
    return lists.map((list, i) => {
      const scope: SideScope = {
        open: new Set([branchKey]),
        others: new Set(lists.flatMap((o, k) => (k === i ? [] : o.steps.map((x) => str(x.key))))),
      };
      const views: CompositeStepView[] = [];
      list.steps.forEach((x, j) => {
        const xp = `${list.at}/${j}`;
        if (Array.isArray(x.parallel))
          err(xp, "invalid", "A side of a branch lists steps in order: a parallel group can't be one of them.");
        else if (x.branch)
          err(
            xp,
            "invalid",
            "A branch can't be on a side of a branch: give it more cases instead, each with its conditions.",
          );
        else {
          views.push(readStep(x, xp, g, scope));
          scope.open.add(str(x.key));
        }
      });
      return { label: list.label, when: list.when, taken: list.taken, steps: views };
    });
  };

  steps.forEach((st, g) => {
    const at = `${prefix}/steps/${g}`;
    const parallel = Array.isArray(st.parallel) ? (st.parallel as Raw[]) : null;
    if (
      parallel &&
      (st.key || st.item || st.awx || st.workflow || st.rest || st.ask || st.wait || st.poll || st.branch || st.notify)
    )
      err(at, "invalid", "A parallel group only lists steps.");
    const list = parallel ?? [st];
    const group: CompositeStepView[] = [];
    list.forEach((s, j) => {
      const sp = parallel ? `${at}/parallel/${j}` : at;
      if (parallel && Array.isArray(s.parallel)) {
        err(sp, "invalid", "Parallel groups don't nest.");
        return;
      }
      if (parallel && s.branch) {
        err(sp, "invalid", "A branch can't run in a parallel group: put it before or after the group.");
        return;
      }
      group.push(readStep(s, sp, g, null));
    });
    groups.push(group);
  });
  return { groups, problems };
}

/** A view's steps and those on its branches' sides, in reading order. */
export function allSteps(view: CompositeView): CompositeStepView[] {
  return view.groups.flat().flatMap((s) => [s, ...(s.sides ?? []).flatMap((side) => side.steps)]);
}

/**
 * The form's secret inputs a composite's messages read (core.checkNotifyRefs),
 * refused on save: they would reach other people and their mailboxes. One read
 * some other way fails when it runs: its value is a marker.
 */
export function secretInMessages(def: Raw, prefix = "/definition"): FieldError[] {
  const fields = (((def.ui_schema ?? {}) as Raw).fields ?? {}) as Record<string, Raw>;
  const secret = new Set(
    Object.entries(fields)
      .filter(([, f]) => f?.secret === true || f?.widget === "secret")
      .map(([k]) => k),
  );
  const inputRead = /\binputs\s*(?:\.\s*([A-Za-z_][A-Za-z0-9_]*)|\[\s*["']([^"']+)["']\s*\])/g;
  const out: FieldError[] = [];
  const check = (s: Raw, at: string) => {
    const n = s.notify as Raw | undefined;
    if (!n) return;
    for (const e of urlExpressions(str(n.message)))
      for (const m of e.matchAll(inputRead)) {
        const field = m[1] ?? m[2]!;
        if (secret.has(field))
          out.push({
            field: `${at}/notify/message`,
            code: "secret",
            message: `A message can't show the secret input ${field}: it reaches other people and their mailboxes.`,
          });
      }
  };
  const bp = (def.blueprint ?? {}) as Raw;
  (Array.isArray(bp.steps) ? (bp.steps as Raw[]) : []).forEach((st, g) => {
    const at = `${prefix}/blueprint/steps/${g}`;
    if (Array.isArray(st.parallel)) (st.parallel as Raw[]).forEach((s, j) => check(s, `${at}/parallel/${j}`));
    else check(st, at);
    const branch = st.branch as Raw | undefined;
    if (!branch) return;
    (Array.isArray(branch.cases) ? (branch.cases as Raw[]) : []).forEach((c, i) =>
      (Array.isArray(c.steps) ? (c.steps as Raw[]) : []).forEach((s, j) =>
        check(s, `${at}/branch/cases/${i}/steps/${j}`),
      ),
    );
    (Array.isArray(branch.else) ? (branch.else as Raw[]) : []).forEach((s, j) => check(s, `${at}/branch/else/${j}`));
  });
  return out;
}
