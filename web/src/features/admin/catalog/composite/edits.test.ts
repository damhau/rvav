import { parse, parseDocument } from "yaml";
import { describe, expect, it } from "vitest";
import { seededBlueprint } from "@/mocks/blueprints";
import { toYaml } from "../definitionYaml";
import {
  addAskQuestion,
  addStep,
  EditError,
  joinPrevious,
  moveStep,
  readStep,
  removeAskQuestion,
  removeStep,
  setAskEscalate,
  setAskField,
  setAskQuestion,
  setBinding,
  setRestField,
  setRollback,
  setStepField,
  setTarget,
  setUndo,
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
