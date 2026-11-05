import { parse, parseDocument } from "yaml";
import { describe, expect, it } from "vitest";
import { seededBlueprint } from "@/mocks/blueprints";
import { toYaml } from "../definitionYaml";
import {
  addStep,
  EditError,
  joinPrevious,
  moveStep,
  readStep,
  removeStep,
  setBinding,
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
