import { parse, parseDocument } from "yaml";
import { describe, expect, it } from "vitest";
import { seededBlueprint } from "@/mocks/blueprints";
import { toYaml } from "../definitionYaml";
import {
  addAskQuestion,
  addBranchCase,
  addStep,
  EditError,
  joinPrevious,
  moveBranchCase,
  moveStep,
  readStep,
  removeAskQuestion,
  removeBranchCase,
  removeStep,
  setAskEscalate,
  setAskField,
  setAskQuestion,
  setBinding,
  setBranchCase,
  setNotifyField,
  setPollField,
  setRestField,
  setRollback,
  setStepField,
  setTarget,
  setUndo,
  setWaitField,
  stepKeys,
  uniqueKey,
  yamlOutput,
} from "./edits";

const definition = (blueprint: Record<string, unknown>) =>
  toYaml({
    form_schema: { type: "object", properties: {} },
    ui_schema: { layout: { kind: "page", sections: [] }, fields: {} },
    initial_values: {},
    blueprint,
  });

const pg = definition(seededBlueprint("postgres-cluster")!);
const blueprint = (text: string) => (parse(text) as { blueprint: Record<string, unknown> }).blueprint;
/** The steps as keys: a parallel group as a list. */
const shape = (text: string) =>
  (blueprint(text).steps as Record<string, unknown>[]).map((s) =>
    Array.isArray(s.parallel) ? (s.parallel as { key: string }[]).map((p) => p.key) : (s.key as string),
  );

// What a reader wrote, which the canvas's edits keep.
const commented = `# The cluster, then its users.
form_schema: {type: object}
ui_schema: {layout: {kind: page, sections: []}, fields: {}}
initial_values: {}
blueprint:
  kind: composite
  rollback_on_failure: true
  steps:
    # The VMs come first.
    - key: vm
      item: rhel9-server
      with: {role: inputs.role}
    - key: db_user
      item: db-user
      with: {host: "steps.vm.fqdn", privileges: '"read"'} # quoted as typed
`;

describe("the composite designer's edits", () => {
  it("give back the server's YAML unchanged but for the edit", () => {
    for (const slug of ["rhel9-server", "web-app", "three-tier", "postgres-cluster", "pg-node"]) {
      const text = definition(seededBlueprint(slug)!);
      expect(parseDocument(text).toString(yamlOutput)).toBe(text);
    }
    expect(setRollback(pg, true)).toBe(pg);
    const off = setRollback(pg, false);
    expect(off).toBe(pg.replace("rollback_on_failure: true", "rollback_on_failure: false"));
  });

  it("keep comments, quotes and inline maps", () => {
    const next = setBinding(commented, "step:db_user", "database", "inputs.role");
    expect(next).toContain("# The cluster, then its users.");
    expect(next).toContain("    # The VMs come first.");
    expect(next).toContain(
      `with: {host: "steps.vm.fqdn", privileges: '"read"', database: inputs.role} # quoted as typed`,
    );
    // Everything else as it was.
    expect(next.replace(", database: inputs.role", "")).toBe(commented);
  });

  it("add a step at the end, after one, or at the same time as one", () => {
    const step = { key: "monitor", awx: { job_template: "monitoring-agent" } };
    expect(shape(addStep(pg, step, "end"))).toEqual(["nodes", ["owner", "backups"], "monitor"]);
    expect(shape(addStep(pg, step, { after: "step:nodes" }))).toEqual(["nodes", "monitor", ["owner", "backups"]]);
    // After a step in a group: after the group.
    expect(shape(addStep(pg, step, { after: "step:owner" }))).toEqual(["nodes", ["owner", "backups"], "monitor"]);
    expect(shape(addStep(pg, step, { with: "step:owner" }))).toEqual(["nodes", ["owner", "monitor", "backups"]]);
    // Beside a step on its own: the two become a group.
    const beside = addStep(pg, step, { with: "step:nodes" });
    expect(shape(beside)).toEqual([
      ["nodes", "monitor"],
      ["owner", "backups"],
    ]);
    expect(beside).toContain("  steps:\n    - parallel:\n        - key: nodes\n");
    // The first step of a new composite: an empty inline list becomes a block.
    const empty = definition({ kind: "composite", rollback_on_failure: true, steps: [] });
    expect(empty).toContain("steps: []");
    const first = addStep(empty, { key: "vm", item: "rhel9-server" }, "end");
    expect(first).toContain("  steps:\n    - key: vm\n      item: rhel9-server\n");
  });

  it("remove a step, and a group left with one step is that step again", () => {
    expect(shape(removeStep(pg, "step:nodes"))).toEqual([["owner", "backups"]]);
    const left = removeStep(pg, "step:backups");
    expect(shape(left)).toEqual(["nodes", "owner"]);
    expect(left).toContain("    - key: owner\n      title: The database's owner\n");
    expect(() => removeStep(pg, "step:gone")).toThrow(EditError);
  });

  it("move a step, out of its group, or into the one before", () => {
    expect(shape(moveStep(pg, "step:nodes", 1))).toEqual([["owner", "backups"], "nodes"]);
    expect(() => moveStep(pg, "step:nodes", -1)).toThrow("It runs first.");
    expect(shape(moveStep(pg, "step:owner", -1))).toEqual(["nodes", "owner", "backups"]);
    expect(shape(moveStep(pg, "step:owner", 1))).toEqual(["nodes", "backups", "owner"]);
    const three = addStep(pg, { key: "monitor", workflow: "register-monitoring" }, "end");
    expect(shape(moveStep(three, "step:backups", 1))).toEqual(["nodes", "owner", "backups", "monitor"]);
    expect(shape(joinPrevious(three, "step:monitor"))).toEqual(["nodes", ["owner", "backups", "monitor"]]);
    expect(shape(joinPrevious(moveStep(pg, "step:owner", -1), "step:owner"))).toEqual([["nodes", "owner"], "backups"]);
    expect(() => joinPrevious(pg, "step:nodes")).toThrow("It runs first.");
  });

  it("set a step's key, title and condition; a new key is written where it is used", () => {
    const renamed = setStepField(pg, "step:nodes", "key", "db_nodes");
    expect(stepKeys(renamed)).toEqual(["db_nodes", "owner", "backups"]);
    const backups = readStep(renamed, "step:backups")!.step;
    expect(backups.with).toEqual({ hosts: "steps.db_nodes.vms", retention_days: "inputs.backup_retention_days" });
    expect(backups.undo).toEqual({ job_template: "pgbackrest-stanza-delete", with: { hosts: "steps.db_nodes.vms" } });
    // A longer key that starts the same is another step.
    const two = setBinding(pg, "step:owner", "host", "steps.nodes_extra.fqdn + steps.nodes.ip");
    expect(readStep(setStepField(two, "step:nodes", "key", "n"), "step:owner")!.step.with).toMatchObject({
      host: "steps.nodes_extra.fqdn + steps.n.ip",
    });

    const when = setStepField(pg, "step:owner", "when", 'inputs.role != "test"');
    expect(Object.keys(readStep(when, "step:owner")!.step)).toEqual(["key", "title", "item", "when", "with"]);
    expect(readStep(setStepField(when, "step:owner", "when", " "), "step:owner")!.step.when).toBeUndefined();
    expect(readStep(setStepField(pg, "step:nodes", "title", ""), "step:nodes")!.step.title).toBeUndefined();
  });

  it("set what a step runs, one kind at a time", () => {
    const awx = setTarget(pg, "step:owner", { kind: "awx", ref: "pg-create-role" });
    expect(readStep(awx, "step:owner")!.step).toMatchObject({ awx: { job_template: "pg-create-role" } });
    expect(readStep(awx, "step:owner")!.step.item).toBeUndefined();
    // Another AWX template: its name and ID, not the previous one's title.
    const backups = setTarget(pg, "step:backups", {
      kind: "awx",
      ref: "fw-flow-request",
      workflow: true,
      templateId: 12,
    });
    expect(readStep(backups, "step:backups")!.step.awx).toEqual({
      job_template: "fw-flow-request",
      template_id: 12,
      workflow: true,
    });
    // Then one named only: the previous one's ID goes too, or it would launch that one.
    const named = setTarget(backups, "step:backups", { kind: "awx", ref: "pg-drop-role" });
    expect(readStep(named, "step:backups")!.step.awx).toEqual({ job_template: "pg-drop-role" });
    // The same template again keeps the title written for it.
    const same = setTarget(pg, "step:backups", { kind: "awx", ref: "pgbackrest-stanza", templateId: 106 });
    expect(readStep(same, "step:backups")!.step.awx).toEqual({
      job_template: "pgbackrest-stanza",
      template_id: 106,
      title: "pgBackRest stanza",
    });
    const fresh = setTarget(pg, "step:owner", { kind: "awx", ref: "monitoring-agent", templateId: 105 });
    expect(readStep(fresh, "step:owner")!.step.awx).toEqual({ job_template: "monitoring-agent", template_id: 105 });
    const wf = setTarget(pg, "step:backups", { kind: "workflow", ref: "register-monitoring" });
    expect(readStep(wf, "step:backups")!.step).toMatchObject({ key: "backups", workflow: "register-monitoring" });
    expect(readStep(wf, "step:backups")!.step.awx).toBeUndefined();
  });

  it("bind and unbind inputs, literals as literals", () => {
    let t = setBinding(pg, "step:owner", "role_name", "inputs.role");
    t = setBinding(t, "step:owner", "count", 3);
    expect(readStep(t, "step:owner")!.step.with).toEqual({
      database: "inputs.role",
      role_name: "inputs.role",
      privileges: '"owner"',
      count: 3,
    });
    for (const name of ["database", "role_name", "privileges", "count"])
      t = setBinding(t, "step:owner", name, undefined);
    expect(readStep(t, "step:owner")!.step.with).toBeUndefined();
    // A step without bindings gets a with, in its place.
    const first = setBinding(t, "step:owner", "database", "inputs.role");
    expect(Object.keys(readStep(first, "step:owner")!.step)).toEqual(["key", "title", "item", "with"]);
    // A string that needs quoting is quoted.
    expect(setBinding(pg, "step:owner", "privileges", '"read"')).toContain(`privileges: '"read"'`);
  });

  it("set and clear a step's undo, with its own bindings", () => {
    let t = setUndo(pg, "step:owner", { kind: "awx", ref: "pg-drop-role" });
    t = setBinding(t, "step:owner", "role_name", 'inputs.role + "_owner"', "undo");
    expect(readStep(t, "step:owner")!.step.undo).toEqual({
      job_template: "pg-drop-role",
      with: { role_name: 'inputs.role + "_owner"' },
    });
    t = setUndo(t, "step:owner", { kind: "workflow", ref: "cleanup" });
    expect(readStep(t, "step:owner")!.step.undo).toEqual({
      workflow: "cleanup",
      with: { role_name: 'inputs.role + "_owner"' },
    });
    expect(readStep(setUndo(t, "step:owner", null), "step:owner")!.step.undo).toBeUndefined();
    expect(() => setBinding(pg, "step:owner", "x", "y", "undo")).toThrow("The step has no undo.");
  });

  it("name steps whose key is taken twice by where they are, and find free keys", () => {
    const twice = addStep(pg, { key: "nodes", awx: { job_template: "x" } }, "end");
    expect(readStep(twice, "step:nodes")).toBeUndefined();
    expect(readStep(twice, "step@/definition/blueprint/steps/2")!.step.awx).toEqual({ job_template: "x" });
    expect(readStep(twice, "step@/definition/blueprint/steps/1/parallel/1")!.step.key).toBe("backups");
    expect(uniqueKey(pg, "nodes")).toBe("nodes_2");
    expect(uniqueKey(pg, "Monitoring agent")).toBe("monitoring_agent");
    expect(uniqueKey(pg, "2 db-users")).toBe("db_users");
    expect(uniqueKey(pg, "--")).toBe("step");
    expect(() => addStep("blueprint: [\n", {}, "end")).toThrow("The YAML doesn't parse.");
  });
});

// Steps that share bindings through an anchor (&) and its aliases (*).
const anchored = `form_schema: {type: object}
ui_schema: {layout: {kind: page, sections: []}, fields: {}}
initial_values: {}
blueprint:
  kind: composite
  steps:
    - key: vm
      item: rhel9-server
    - key: a
      awx: {job_template: one}
      with: &common {size: inputs.size, cpu: steps.vm.fqdn}
      undo: &undo {job_template: one-undo, with: {size: inputs.size}}
    - key: b
      awx: {job_template: two}
      with: *common
      undo: *undo
    - key: c
      awx: {job_template: three}
      with: *common
`;

/** Each step's data, by key, as the canvas and the server read it. */
const data = (text: string) =>
  Object.fromEntries(
    (blueprint(text).steps as Record<string, unknown>[]).flatMap((s) =>
      (Array.isArray(s.parallel) ? (s.parallel as Record<string, unknown>[]) : [s]).map(
        (x): [string, Record<string, unknown>] => [String(x.key), x],
      ),
    ),
  );
const common = { size: "inputs.size", cpu: "steps.vm.fqdn" };

describe("the edits of steps that share an anchor", () => {
  it("read an alias as what it names", () => {
    expect(readStep(anchored, "step:b")!.step.with).toEqual(common);
    expect(readStep(anchored, "step:b")!.step.undo).toEqual({
      job_template: "one-undo",
      with: { size: "inputs.size" },
    });
  });

  it("bind and unbind a step's input through its alias: that step gets a copy, the others keep theirs", () => {
    const bound = setBinding(anchored, "step:b", "size", "inputs.other");
    expect(data(bound).b!.with).toEqual({ ...common, size: "inputs.other" });
    expect(data(bound).a!.with).toEqual(common);
    expect(data(bound).c!.with).toEqual(common);
    // c still shares a's.
    expect(bound).toContain("with: *common");

    const unbound = setBinding(anchored, "step:b", "cpu", undefined);
    expect(data(unbound).b!.with).toEqual({ size: "inputs.size" });
    expect(data(unbound).a!.with).toEqual(common);

    const undo = setBinding(anchored, "step:b", "size", "steps.a.job_id", "undo");
    expect(data(undo).b!.undo).toEqual({ job_template: "one-undo", with: { size: "steps.a.job_id" } });
    expect(data(undo).a!.undo).toEqual({ job_template: "one-undo", with: { size: "inputs.size" } });
    expect(data(setUndo(anchored, "step:b", { kind: "workflow", ref: "cleanup" })).a!.undo).toEqual({
      job_template: "one-undo",
      with: { size: "inputs.size" },
    });
  });

  it("change the anchor's step only: the aliases keep what they named", () => {
    const bound = setBinding(anchored, "step:a", "size", "inputs.other");
    expect(data(bound).a!.with).toEqual({ ...common, size: "inputs.other" });
    expect(data(bound).b!.with).toEqual(common);
    expect(data(bound).c!.with).toEqual(common);
    const cleared = setBinding(setBinding(anchored, "step:a", "size", undefined), "step:a", "cpu", undefined);
    expect(data(cleared).a!.with).toBeUndefined();
    expect(data(cleared).b!.with).toEqual(common);
    expect(data(setUndo(anchored, "step:a", null)).b!.undo).toEqual({
      job_template: "one-undo",
      with: { size: "inputs.size" },
    });
  });

  it("remove or move the anchor's step, or an alias's before it, without changing the others", () => {
    const steps = (text: string) => {
      const d = data(text);
      return { b: d.b, c: d.c };
    };
    const before = steps(anchored);
    const removed = removeStep(anchored, "step:a");
    expect(shape(removed)).toEqual(["vm", "b", "c"]);
    expect(steps(removed)).toEqual(before);
    const later = moveStep(anchored, "step:a", 1);
    expect(shape(later)).toEqual(["vm", "b", "a", "c"]);
    expect(steps(later)).toEqual(before);
    expect(data(later).a).toEqual(data(anchored).a);
    const earlier = moveStep(anchored, "step:b", -1);
    expect(shape(earlier)).toEqual(["vm", "b", "a", "c"]);
    expect(steps(earlier)).toEqual(before);
    const joined = joinPrevious(moveStep(anchored, "step:a", -1), "step:vm");
    expect(steps(joined)).toEqual(before);
  });

  it("rename a key in every step, aliases with their anchor", () => {
    const renamed = setStepField(anchored, "step:vm", "key", "server");
    for (const k of ["a", "b", "c"]) expect(data(renamed)[k]!.with).toEqual({ ...common, cpu: "steps.server.fqdn" });
    expect(renamed).toContain("with: *common");
  });
});

describe("a step's REST call", () => {
  const stepsOf = (text: string) => blueprint(text).steps as Record<string, unknown>[];
  const withRest = definition({
    kind: "composite",
    steps: [
      { key: "vm", item: "x", with: { a: "inputs.a" } },
      {
        key: "cmdb",
        rest: { method: "POST", url: "https://h.example/steps.vm/${steps.vm.name}", body: "{'n': steps.vm.name}" },
        undo: { rest: { method: "DELETE", url: "https://h.example/${steps.vm.id}/${steps.cmdb.json.id}" } },
      },
    ],
  });

  it("is what a step runs: its bindings go, and another target takes its place", () => {
    const made = setTarget(withRest, "step:vm", { kind: "rest", ref: "" });
    expect(stepsOf(made)[0]).toEqual({ key: "vm", rest: { method: "GET", url: "" } });
    expect(stepsOf(setTarget(withRest, "step:cmdb", { kind: "item", ref: "y" }))[1]).toEqual({
      key: "cmdb",
      item: "y",
      undo: stepsOf(withRest)[1]!.undo,
    });
    // Chosen again, it stays as written.
    expect(setTarget(withRest, "step:cmdb", { kind: "rest", ref: "" })).toBe(withRest);
  });

  it("sets its fields in their order, an empty one removed, the method and URL kept", () => {
    let text = setRestField(withRest, "step:cmdb", "keep", "$.result");
    text = setRestField(text, "step:cmdb", "secret", "cmdb-token");
    text = setRestField(text, "step:cmdb", "accept", [200, 201]);
    expect(Object.keys(stepsOf(text)[1]!.rest as object)).toEqual([
      "method",
      "url",
      "body",
      "secret",
      "accept",
      "keep",
    ]);
    expect(text).toContain("accept: [200, 201]");
    expect(stepsOf(setRestField(text, "step:cmdb", "keep", " "))[1]!.rest).not.toHaveProperty("keep");
    expect(stepsOf(setRestField(text, "step:cmdb", "accept", []))[1]!.rest).not.toHaveProperty("accept");
    expect(stepsOf(setRestField(text, "step:cmdb", "url", ""))[1]!.rest).toHaveProperty("url", "");
    expect(() => setRestField(withRest, "step:vm", "url", "x")).toThrow(EditError);
  });

  it("undoes a step, in place of what did, and its fields are set there", () => {
    const u = (t: string, key = 0) => stepsOf(t)[key]!.undo;
    const made = setUndo(withRest, "step:vm", { kind: "rest" });
    expect(u(made)).toEqual({ rest: { method: "DELETE", url: "" } });
    const url = setRestField(made, "step:vm", "url", "https://h.example/x", "undo");
    expect(u(url)).toEqual({ rest: { method: "DELETE", url: "https://h.example/x" } });
    expect(u(setUndo(url, "step:vm", { kind: "awx", ref: "t" }))).toEqual({ job_template: "t" });
    const fromAwx = setUndo(
      setBinding(setUndo(withRest, "step:vm", { kind: "awx", ref: "t" }), "step:vm", "a", "1", "undo"),
      "step:vm",
      { kind: "rest" },
    );
    expect(u(fromAwx)).toEqual({ rest: { method: "DELETE", url: "" } });
    expect(() => setRestField(withRest, "step:vm", "url", "x", "undo")).toThrow(EditError);
  });

  it("follows a step's new key in its URLs' expressions and body, not in the URL's text", () => {
    const steps = stepsOf(setStepField(withRest, "step:vm", "key", "server"));
    expect(steps[1]!.rest).toEqual({
      method: "POST",
      url: "https://h.example/steps.vm/${steps.server.name}",
      body: "{'n': steps.server.name}",
    });
    expect(steps[1]!.undo).toEqual({
      rest: { method: "DELETE", url: "https://h.example/${steps.server.id}/${steps.cmdb.json.id}" },
    });
  });
});

describe("a step's wait for a person", () => {
  const stepsOf = (text: string) =>
    (parse(text) as { blueprint: { steps: Record<string, unknown>[] } }).blueprint.steps;
  const made = () =>
    setTarget(
      definition({
        kind: "composite",
        steps: [
          { key: "vm", awx: { job_template: "t" }, with: { a: "inputs.a" }, undo: { job_template: "u" } },
          { key: "later", item: "x", with: { b: "steps.vm.answer.ok" } },
        ],
      }),
      "step:vm",
      { kind: "ask", ref: "" },
    );
  const ask = (text: string) => stepsOf(text)[0]!.ask as Record<string, unknown>;

  it("replaces what the step ran, its inputs and its undo", () => {
    expect(stepsOf(made())[0]).toEqual({ key: "vm", ask: {} });
    // Asked again, it keeps what it has.
    const filled = setAskField(made(), "step:vm", "requester", true);
    expect(setTarget(filled, "step:vm", { kind: "ask", ref: "" })).toBe(filled);
    // Back to a template: the wait goes.
    expect(stepsOf(setTarget(filled, "step:vm", { kind: "awx", ref: "t" }))[0]).toEqual({
      key: "vm",
      awx: { job_template: "t" },
    });
  });

  it("writes its fields in order, and removes what is emptied", () => {
    let text = made();
    text = setAskField(text, "step:vm", "timeout", " 48h ");
    text = setAskField(text, "step:vm", "message", "Why?");
    text = setAskField(text, "step:vm", "requester", true);
    text = setAskField(text, "step:vm", "approvers", ["u1", "u2"]);
    text = setAskField(text, "step:vm", "group", "managers");
    expect(Object.keys(ask(text))).toEqual(["group", "approvers", "requester", "message", "timeout"]);
    expect(text).toContain("approvers: [u1, u2]");
    expect(ask(text).timeout).toBe("48h");
    expect(ask(setAskField(text, "step:vm", "requester", false))).not.toHaveProperty("requester");
    expect(ask(setAskField(text, "step:vm", "approvers", []))).not.toHaveProperty("approvers");
    expect(ask(setAskField(text, "step:vm", "group", " "))).not.toHaveProperty("group");
  });

  it("drops what depends on a timeout with it, and whom it escalates to with its escalation", () => {
    let text = setAskField(made(), "step:vm", "timeout", "1h");
    text = setAskField(text, "step:vm", "on_timeout", "escalate");
    text = setAskEscalate(text, "step:vm", "approvers", ["u9"]);
    text = setAskEscalate(text, "step:vm", "group", "chiefs");
    expect(ask(text).escalate_to).toEqual({ group: "chiefs", approvers: ["u9"] });
    const back = setAskField(text, "step:vm", "on_timeout", "continue");
    expect(ask(back).on_timeout).toBe("continue");
    expect(ask(back)).not.toHaveProperty("escalate_to");
    const none = setAskField(text, "step:vm", "timeout", "");
    expect(Object.keys(ask(none))).toEqual([]);
    // Nobody left: it goes.
    const gone = setAskEscalate(setAskEscalate(text, "step:vm", "group", ""), "step:vm", "approvers", []);
    expect(ask(gone)).not.toHaveProperty("escalate_to");
  });

  it("adds, changes and removes the questions of its form", () => {
    let text = addAskQuestion(addAskQuestion(made(), "step:vm"), "step:vm");
    const form = () => ask(text).form as Record<string, unknown>[];
    expect(form().map((q) => q.key)).toEqual(["question_1", "question_2"]);
    text = setAskQuestion(text, "step:vm", 1, "key", "size");
    text = setAskQuestion(text, "step:vm", 1, "label", "How big?");
    text = setAskQuestion(text, "step:vm", 1, "type", "choice");
    text = setAskQuestion(text, "step:vm", 1, "options", ["S", "L"]);
    text = setAskQuestion(text, "step:vm", 1, "default", "S");
    text = setAskQuestion(text, "step:vm", 1, "required", true);
    expect(form()[1]).toEqual({
      key: "size",
      label: "How big?",
      type: "choice",
      required: true,
      options: ["S", "L"],
      default: "S",
    });
    // A key taken back is free again; another type takes what the old one offered.
    expect(addAskQuestion(setAskQuestion(text, "step:vm", 0, "key", "x"), "step:vm")).toContain("question_1");
    text = setAskQuestion(text, "step:vm", 1, "type", "number");
    expect(form()[1]).toEqual({ key: "size", label: "How big?", type: "number", required: true });
    text = setAskQuestion(text, "step:vm", 1, "required", false);
    expect(form()[1]).not.toHaveProperty("required");
    // Emptied, a key stays for the step to say what is missing.
    expect((ask(setAskQuestion(text, "step:vm", 1, "key", "")).form as { key: string }[])[1]!.key).toBe("");
    text = removeAskQuestion(text, "step:vm", 0);
    expect(form().map((q) => q.key)).toEqual(["size"]);
    expect(ask(removeAskQuestion(text, "step:vm", 0))).not.toHaveProperty("form");
    expect(() => removeAskQuestion(text, "step:vm", 5)).toThrow(EditError);
  });

  it("refuses a step that doesn't wait", () => {
    expect(() => setAskField(made(), "step:later", "requester", true)).toThrow(EditError);
  });

  it("leaves its message alone when a step's key is renamed", () => {
    const text = setAskField(made(), "step:vm", "message", "See steps.vm in the runbook");
    const renamed = setStepField(text, "step:vm", "key", "manager");
    expect(ask(renamed).message).toBe("See steps.vm in the runbook");
    expect(stepsOf(renamed)[1]!.with).toEqual({ b: "steps.manager.answer.ok" });
  });
});

describe("a wait for a time and a poll", () => {
  const stepsOf = (text: string) => blueprint(text).steps as Record<string, unknown>[];
  const base = () =>
    definition({
      kind: "composite",
      steps: [
        { key: "vm", awx: { job_template: "t" }, with: { a: "inputs.a" }, undo: { job_template: "u" } },
        { key: "later", item: "x", with: { b: "steps.vm.until" }, when: "steps.vm.met" },
      ],
    });
  const asWait = () => setTarget(base(), "step:vm", { kind: "wait", ref: "" });
  const asPoll = () => setTarget(base(), "step:vm", { kind: "poll", ref: "" });

  it("replaces what the step ran, its inputs and its undo, and keeps what it has when asked again", () => {
    expect(stepsOf(asWait())[0]).toEqual({ key: "vm", wait: {} });
    expect(stepsOf(asPoll())[0]).toEqual({
      key: "vm",
      poll: { rest: { method: "GET", url: "" }, until: "", every: "10m", timeout: "72h" },
    });
    const filled = setWaitField(asWait(), "step:vm", "for", "72h");
    expect(setTarget(filled, "step:vm", { kind: "wait", ref: "" })).toBe(filled);
    // One kind for another: the wait goes.
    expect(stepsOf(setTarget(filled, "step:vm", { kind: "poll", ref: "" }))[0]).not.toHaveProperty("wait");
    expect(stepsOf(setTarget(asPoll(), "step:vm", { kind: "rest", ref: "" }))[0]).not.toHaveProperty("poll");
  });

  it("writes how long or until when, one taking the other's place", () => {
    let text = setWaitField(asWait(), "step:vm", "for", " 72h ");
    expect(stepsOf(text)[0]!.wait).toEqual({ for: "72h" });
    text = setWaitField(text, "step:vm", "until", "inputs.window");
    expect(stepsOf(text)[0]!.wait).toEqual({ until: "inputs.window" });
    text = setWaitField(text, "step:vm", "until", " ");
    expect(stepsOf(text)[0]!.wait).toEqual({});
  });

  it("writes a poll's fields and its call's in order, and removes what is emptied", () => {
    let text = setPollField(asPoll(), "step:vm", "on_timeout", "continue");
    text = setPollField(text, "step:vm", "until", " steps.vm.status == 200 ");
    text = setRestField(text, "step:vm", "url", "https://h.example/x", "poll");
    text = setRestField(text, "step:vm", "method", "PUT", "poll");
    text = setRestField(text, "step:vm", "accept", [200, 404], "poll");
    text = setRestField(text, "step:vm", "keep", "$.state", "poll");
    const poll = stepsOf(text)[0]!.poll as Record<string, unknown>;
    expect(Object.keys(poll)).toEqual(["rest", "until", "every", "timeout", "on_timeout"]);
    expect(poll.until).toBe("steps.vm.status == 200");
    expect(poll.rest).toEqual({ method: "PUT", url: "https://h.example/x", accept: [200, 404], keep: "$.state" });
    expect(stepsOf(setPollField(text, "step:vm", "every", ""))[0]!.poll).not.toHaveProperty("every");
    expect(() => setPollField(base(), "step:vm", "every", "1m")).toThrow(EditError);
    expect(() => setWaitField(base(), "step:vm", "for", "1m")).toThrow(EditError);
    expect(() => setRestField(base(), "step:vm", "url", "u", "poll")).toThrow(EditError);
  });

  it("writes a new key into what they read, its own too", () => {
    let text = setPollField(asPoll(), "step:vm", "until", "steps.vm.json == 'x'");
    text = setRestField(text, "step:vm", "url", "https://h.example/${steps.vm.id}", "poll");
    text = setStepField(text, "step:vm", "key", "change");
    const [change, later] = stepsOf(text);
    const poll = change!.poll as Record<string, Record<string, string>>;
    expect(poll.until).toBe("steps.change.json == 'x'");
    expect(poll.rest!.url).toBe("https://h.example/${steps.change.id}");
    expect(later).toEqual({ key: "later", item: "x", with: { b: "steps.change.until" }, when: "steps.change.met" });

    const wait = setStepField(
      definition({
        kind: "composite",
        steps: [
          { key: "a", item: "x" },
          { key: "b", wait: { until: "steps.a.at" } },
        ],
      }),
      "step:a",
      "key",
      "first",
    );
    expect(stepsOf(wait)[1]!.wait).toEqual({ until: "steps.first.at" });
  });
});

describe("a branch and its sides", () => {
  const base = () =>
    definition({
      kind: "composite",
      steps: [
        { key: "first", awx: { job_template: "a" } },
        { key: "b", branch: { cases: [{ when: "inputs.a", steps: [{ key: "x", awx: { job_template: "x" } }] }] } },
        { key: "last", awx: { job_template: "z" } },
      ],
    });
  const b = "step:b";
  const branchOf = (text: string) => (blueprint(text).steps as { branch?: Record<string, unknown> }[])[1]!.branch!;
  const caseKeys = (text: string, c = 0) =>
    (branchOf(text).cases as { steps?: { key: string }[] }[])[c]!.steps!.map((s) => s.key);
  const other = (key: string) => ({ key, awx: { job_template: key } });

  it("turns a step into a branch, whose condition, inputs and undo go", () => {
    const text = definition({
      kind: "composite",
      steps: [{ key: "a", awx: { job_template: "t" }, when: "inputs.x", with: { y: 1 }, undo: { job_template: "u" } }],
    });
    const next = blueprint(setTarget(text, "step:a", { kind: "branch", ref: "" })).steps as Record<string, unknown>[];
    expect(next[0]).toEqual({ key: "a", branch: { cases: [{ when: "", steps: [] }] } });
  });

  it("turns a step into a message, whose inputs and undo go", () => {
    const text = definition({
      kind: "composite",
      steps: [{ key: "a", awx: { job_template: "t" }, when: "inputs.x", with: { y: 1 }, undo: { job_template: "u" } }],
    });
    const next = blueprint(setTarget(text, "step:a", { kind: "notify", ref: "" })).steps as Record<string, unknown>[];
    expect(next[0]).toEqual({ key: "a", notify: {}, when: "inputs.x" });
  });

  it("adds, moves and removes its cases, one edit each", () => {
    let text = addBranchCase(base(), b);
    expect(branchOf(text).cases).toHaveLength(2);
    text = setBranchCase(text, b, 1, "when", " inputs.b ");
    text = setBranchCase(text, b, 1, "title", "Big");
    expect((branchOf(text).cases as unknown[])[1]).toEqual({ title: "Big", when: "inputs.b", steps: [] });
    text = moveBranchCase(text, b, 1, -1);
    expect((branchOf(text).cases as { when: string }[]).map((c) => c.when)).toEqual(["inputs.b", "inputs.a"]);
    expect(() => moveBranchCase(text, b, 0, -1)).toThrow("It comes first.");
    expect(() => moveBranchCase(text, b, 1, 1)).toThrow("It comes last.");
    expect((branchOf(setBranchCase(text, b, 0, "title", "")).cases as unknown[])[0]).toEqual({
      when: "inputs.b",
      steps: [],
    });
    // A case's condition stays empty, for the step to say what is missing.
    expect((branchOf(setBranchCase(text, b, 0, "when", "")).cases as { when: string }[])[0]!.when).toBe("");
    text = removeBranchCase(text, b, 0);
    expect((branchOf(text).cases as { when: string }[]).map((c) => c.when)).toEqual(["inputs.a"]);
    expect(caseKeys(text)).toEqual(["x"]);
    expect(() => setBranchCase(text, b, 4, "when", "x")).toThrow(EditError);
    expect(() => addBranchCase(text, "step:first")).toThrow("It isn't a branch.");
  });

  it("adds a step to a side, makes otherwise when it adds to it, and removes it when it empties", () => {
    let text = addStep(base(), other("y"), { side: b, taken: 1 });
    expect(caseKeys(text)).toEqual(["x", "y"]);
    text = addStep(text, other("z"), { after: "step:x" });
    expect(caseKeys(text)).toEqual(["x", "z", "y"]);
    text = addStep(text, other("o"), { side: b, taken: 0 });
    expect(branchOf(text).else).toEqual([other("o")]);
    expect(stepKeys(text)).toEqual(["first", "b", "x", "z", "y", "o", "last"]);
    text = removeStep(text, "step:o");
    expect(branchOf(text)).not.toHaveProperty("else");
    // A case keeps its list, empty: it goes on.
    for (const k of ["x", "y", "z"]) text = removeStep(text, `step:${k}`);
    expect(caseKeys(text)).toEqual([]);
    expect(shape(text)).toEqual(["first", "b", "last"]);
  });

  it("moves a side's steps within their side", () => {
    let text = addStep(base(), other("y"), { side: b, taken: 1 });
    text = moveStep(text, "step:y", -1);
    expect(caseKeys(text)).toEqual(["y", "x"]);
    expect(() => moveStep(text, "step:y", -1)).toThrow("It runs first on its side.");
    expect(() => moveStep(text, "step:x", 1)).toThrow("It runs last on its side.");
    expect(shape(text)).toEqual(["first", "b", "last"]);
  });

  it("nests nothing, and has nothing on a side run with another", () => {
    const branch = { key: "inner", branch: { cases: [{ when: "x", steps: [] }] } };
    expect(() => addStep(base(), branch, { side: b, taken: 1 })).toThrow(/can't be on a side/);
    expect(() => addStep(base(), branch, { after: "step:x" })).toThrow(/can't be on a side/);
    expect(() => addStep(base(), other("y"), { with: "step:x" })).toThrow(/parallel group/);
    expect(() => addStep(base(), other("y"), { with: b })).toThrow(/parallel group/);
    expect(() => addStep(base(), branch, { with: "step:first" })).toThrow(/parallel group/);
    expect(() => setTarget(base(), "step:x", { kind: "branch", ref: "" })).toThrow(/can't be on a side/);
    expect(() => joinPrevious(base(), b)).toThrow(/parallel group/);
    expect(() => joinPrevious(base(), "step:last")).toThrow(/parallel group/);
    expect(() => joinPrevious(base(), "step:x")).toThrow(/parallel group/);
  });

  it("follows a key renamed into its cases, a side's conditions and a message", () => {
    let text = definition({
      kind: "composite",
      steps: [
        { key: "first", awx: { job_template: "a" } },
        {
          key: "b",
          branch: {
            cases: [
              { when: "steps.first.ok", steps: [{ key: "x", awx: { job_template: "x" }, when: "steps.first.ok" }] },
            ],
          },
        },
        { key: "n", notify: { owner: true, message: "It is ${steps.first.ok}, steps.first stays" } },
      ],
    });
    text = setStepField(text, "step:first", "key", "renamed");
    const [, br, n] = blueprint(text).steps as Record<string, Record<string, unknown>>[];
    const c = (br!.branch!.cases as { when: string; steps: { when: string }[] }[])[0]!;
    expect(c.when).toBe("steps.renamed.ok");
    expect(c.steps[0]!.when).toBe("steps.renamed.ok");
    expect(n!.notify!.message).toBe("It is ${steps.renamed.ok}, steps.first stays");
  });

  it("says where a side's step is", () => {
    const text = addStep(base(), other("y"), { side: b, taken: 1 });
    expect(readStep(text, "step:y")).toMatchObject({
      pointer: "/definition/blueprint/steps/1/branch/cases/0/steps/1",
      side: { branch: "b", taken: 1, i: 1, length: 2 },
    });
    expect(readStep(text, "step:first")?.side).toBeUndefined();
  });

  it("sets a message's fields, one edit each, and leaves out what is empty", () => {
    let text = addStep(base(), { key: "n", notify: {} }, "end");
    const notify = (t: string) => (blueprint(t).steps as Record<string, unknown>[]).at(-1)!.notify;
    text = setNotifyField(text, "step:n", "message", " Ready ");
    text = setNotifyField(text, "step:n", "users", ["usr_1"]);
    text = setNotifyField(text, "step:n", "groups", ["dba", "ops"]);
    text = setNotifyField(text, "step:n", "owner", true);
    text = setNotifyField(text, "step:n", "requester", true);
    expect(text).toContain("groups: [dba, ops]");
    expect(Object.keys(notify(text) as object)).toEqual(["requester", "owner", "groups", "users", "message"]);
    text = setNotifyField(text, "step:n", "owner", false);
    text = setNotifyField(text, "step:n", "users", []);
    text = setNotifyField(text, "step:n", "message", "");
    expect(notify(text)).toEqual({ requester: true, groups: ["dba", "ops"] });
    expect(() => setNotifyField(base(), "step:first", "owner", true)).toThrow("It sends no message.");
  });
});
