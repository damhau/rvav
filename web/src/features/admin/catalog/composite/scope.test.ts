import { parse, stringify } from "yaml";
import { describe, expect, it } from "vitest";
import { addProblems, blueprintGraph } from "@/components/canvas";
import { compositeStepTargets } from "@/mocks/catalogAdmin";
import { workflowRegistrations } from "@/mocks/extensibility";
import { seededBlueprint } from "@/mocks/blueprints";
import { db } from "@/mocks/seed";
import { minimalChange } from "../YamlEditor";
import { writtenBinding } from "./CompositeDesigner";
import { addStep, setBinding, setStepField, setUndo } from "./edits";
import type { CatalogEntitlements, CompositeStepTarget } from "@/lib/api/types";
import {
  bindingSources,
  designerProblems,
  earlierSteps,
  findTarget,
  flatten,
  formInputsOf,
  listedTarget,
  notOfferedTo,
  openEarlier,
  outputsOf,
  targetOf,
  targetValue,
} from "./scope";

const targets = compositeStepTargets(db, workflowRegistrations(db));
const form = {
  form_schema: {
    type: "object",
    properties: {
      role: { type: "string", title: "Role" },
      vm_name: { type: "string", readOnly: true },
      size: { type: "string" },
    },
  },
  ui_schema: {
    layout: { kind: "page", sections: [{ key: "s", fields: ["size", "role"] }] },
    fields: { size: { label: "Node size" } },
  },
  initial_values: {},
};
const pg = stringify({ ...form, blueprint: seededBlueprint("postgres-cluster")! }, { indent: 2, lineWidth: 0 });
const def = (text: string) => parse(text) as Record<string, unknown>;
const values = (text: string, g: number, planned = false) =>
  bindingSources(def(text), { g, planned }, targets).map((s) => s.value);

describe("what a step can bind to", () => {
  it("the form's inputs in its sections' order, without read-only ones", () => {
    expect(formInputsOf(def(pg))).toEqual([
      { name: "size", title: "Node size" },
      { name: "role", title: "Role" },
    ]);
  });

  it("the shared variables, a VM step's too, after what the request gives (ADR-0057)", () => {
    const shared = [
      { key: "network.dns_servers", description: "The resolvers every VM is given." },
      { key: "cmdb.site", description: "" },
    ];
    for (const planned of [false, true])
      expect(bindingSources(def(pg), { g: 1, planned }, targets, shared).slice(-3)).toEqual([
        { value: "project.id", label: "The project's ID", group: "The request" },
        { value: "vars.network.dns_servers", label: "The resolvers every VM is given.", group: "Shared variables" },
        { value: "vars.cmdb.site", label: "cmdb.site", group: "Shared variables" },
      ]);
  });

  it("the outputs of the steps before its own, not of those beside it, and none for a VM step", () => {
    // The group after the nodes: the nodes' outputs, not the other step's in the group.
    const inGroup = values(pg, 1);
    expect(inGroup).toEqual([
      "inputs.size",
      "inputs.role",
      "steps.nodes.name",
      "steps.nodes.fqdn",
      "steps.nodes.ip",
      "steps.nodes.moref",
      "steps.nodes.deployment_id",
      "steps.nodes.vms",
      "environment",
      "project.code",
      "project.name",
      "project.id",
    ]);
    // After the group: both of its steps' job IDs.
    const after = addStep(pg, { key: "monitor", awx: { job_template: "monitoring-agent" } }, "end");
    expect(values(after, 2)).toEqual(expect.arrayContaining(["steps.owner.job_id", "steps.backups.job_id"]));
    // A VM step is planned from the form.
    expect(values(after, 2, true).filter((v) => v.startsWith("steps."))).toEqual([]);
    // A template no one imported still gives its job's ID.
    expect(outputsOf({ awx: { job_template: "not-imported" } }, targets)).toEqual({
      outputs: [{ name: "job_id", type: "integer", description: "The AWX job's ID" }],
      open: false,
    });
  });

  it("a custom workflow's outputs are what it returns, written as CEL", () => {
    const wf = addStep(pg, { key: "register", workflow: "register-monitoring" }, { after: "step:nodes" });
    expect(openEarlier(def(wf), { g: 2 }, targets)).toEqual(["register"]);
    expect(values(wf, 2).some((v) => v.startsWith("steps.register."))).toBe(false);
  });
});

describe("what the server would refuse when the item is requested", () => {
  it("is said on the step, in its words", () => {
    let t = setStepField(pg, "step:nodes", "when", "inputs.size == 'L'");
    t = setUndo(t, "step:nodes", { kind: "awx", ref: "x" });
    t = addStep(t, { key: "first", awx: { job_template: "monitoring-agent" } }, "end");
    t = setBinding(t, "step:nodes", "network", "steps.first.ip");
    t = addStep(t, { key: "gone", item: "no-such-item" }, "end");
    t = addStep(t, { key: "wf", workflow: "not-registered" }, "end");
    expect(designerProblems(def(t), targets)).toEqual([
      {
        field: "/definition/blueprint/steps/0/undo",
        code: "invalid",
        message: "Database nodes: a VM isn't undone by a job or workflow; a failure deletes it.",
      },
      {
        field: "/definition/blueprint/steps/0/when",
        code: "invalid",
        message: "Database nodes: a VM step has no condition: its VMs are planned when the request is submitted.",
      },
      {
        field: "/definition/blueprint/steps/0/with/network",
        code: "invalid",
        message:
          "Database nodes: a VM is planned when the request is submitted, so its inputs come from the form, not from steps.",
      },
      {
        field: "/definition/blueprint/steps/3/item",
        code: "not_available",
        message: "gone uses no-such-item, which isn't published.",
      },
      {
        field: "/definition/blueprint/steps/4/workflow",
        code: "not_available",
        message: "wf: no custom workflow not-registered is registered for composite steps.",
      },
    ]);
    // The seeded composite has none.
    expect(designerProblems(def(pg), targets)).toEqual([]);
    expect(flatten(def(pg).blueprint as Record<string, unknown>).map((s) => s.pointer)).toEqual([
      "/definition/blueprint/steps/0",
      "/definition/blueprint/steps/1/parallel/0",
      "/definition/blueprint/steps/1/parallel/1",
    ]);
  });

  it("says a step whose target isn't offered to every project that may request the composite", () => {
    const limited = (t: CompositeStepTarget): CompositeStepTarget => ({ ...t, project_ids: ["prj_data"] });
    const some = targets.map((t) => (t.ref === "register-monitoring" || t.ref === "decommission" ? limited(t) : t));
    const projects = db.projects.map((p) => ({ id: p.id, code: p.code, name: p.name }));
    const data = projects.find((p) => p.id === "prj_data")!;
    const others = projects.filter((p) => p.id !== "prj_data");
    const offered = (ids: string[], all = false): CatalogEntitlements => ({
      all_projects: all,
      project_ids: ids,
      projects,
    });
    let t = addStep(pg, { key: "mon", workflow: "register-monitoring" }, "end");
    t = setUndo(t, "step:owner", { kind: "workflow", ref: "register-monitoring" });
    // Offered to the data project only, as the workflow is: nothing to say.
    expect(designerProblems(def(t), some, offered(["prj_data"]))).toEqual([]);
    expect(designerProblems(def(t), targets, offered([], true))).toEqual([]);
    // Offered to others: both the step and the undo are refused for them.
    expect(designerProblems(def(t), some, offered(["prj_data", others[0]!.id]))).toEqual([
      {
        field: "/definition/blueprint/steps/1/parallel/0/undo/workflow",
        code: "not_available",
        message: `The database's owner (undo): the custom workflow register-monitoring isn't offered to ${others[0]!.code}, which may request this item.`,
      },
      {
        field: "/definition/blueprint/steps/2/workflow",
        code: "not_available",
        message: `mon: the custom workflow register-monitoring isn't offered to ${others[0]!.code}, which may request this item.`,
      },
    ]);
    // To every project: the others are named, a long list cut short.
    const all = notOfferedTo(limited(targets[0]!), offered([], true));
    expect(all).toEqual(others.map((p) => p.code).sort());
    expect(all).not.toContain(data.code);
    const many = designerProblems(def(t), some, offered([], true))[1]!.message;
    expect(many).toContain(others.length > 4 ? `and ${others.length - 3} other projects` : all.at(-1));
    // Without the entitlements, or a target offered to all: nothing.
    expect(notOfferedTo(targets[0], offered(["prj_data", others[0]!.id]))).toEqual([]);
    expect(designerProblems(def(t), some)).toEqual([]);
  });

  it("finds an AWX template as the step names it: job or workflow template, by its ID when it gives one", () => {
    const awx = (name: string, workflow: boolean, id: number): CompositeStepTarget => ({
      kind: "awx",
      ref: name,
      title: name,
      runs: "awx",
      planned: false,
      inputs: [],
      outputs: [],
      open_outputs: false,
      awx_id: id,
      ...(workflow ? { awx_workflow: true } : {}),
    });
    const list = [awx("deploy", false, 7), awx("deploy", true, 7), awx("renamed", false, 9)];
    // Same name, one a workflow template: distinct values, each found.
    expect(list.map((t) => targetValue(listedTarget(t)))).toEqual(["awx:deploy", "awx-workflow:deploy", "awx:renamed"]);
    expect(findTarget(list, targetOf({ awx: { job_template: "deploy" } }))).toBe(list[0]);
    expect(findTarget(list, targetOf({ awx: { job_template: "deploy", workflow: true } }))).toBe(list[1]);
    // Launched by its ID: found under its name now, not under the one written.
    expect(findTarget(list, targetOf({ awx: { job_template: "deploy", template_id: 9 } }))).toBe(list[2]);
    expect(findTarget(list, targetOf({ awx: { job_template: "deploy", template_id: 8 } }))).toBeUndefined();
    // Every template the server lists has its ID.
    expect(targets.filter((t) => t.kind === "awx" && !t.awx_id)).toEqual([]);
  });

  it("says a shared variable read that doesn't exist on the step that reads it, once the variables are known", () => {
    const shared = [{ key: "cmdb.site" }] as Parameters<typeof designerProblems>[3];
    const post = (key: string, body: string) => ({ key, rest: { method: "POST", url: "https://h.example/x", body } });
    const d = {
      blueprint: {
        kind: "composite",
        steps: [
          { ...post("make", "{'s': vars.cmdb.site, 'n': vars.nope.x}"), catch: [post("fix", "{'g': vars.ghost.y}")] },
          { parallel: [post("a", "{'g': vars.ghost.y, 'again': vars.ghost.y}")] },
        ],
        on_error: [post("tell", "{'z': vars.nope.z}")],
      },
    };
    const missing = (v: string) => `There is no variable ${v}: add it under Administration > Variables.`;
    expect(designerProblems(d, [], undefined, shared)).toEqual([
      { field: "/definition/blueprint/steps/0", code: "unknown_variable", message: missing("vars.nope.x") },
      { field: "/definition/blueprint/steps/0/catch/0", code: "unknown_variable", message: missing("vars.ghost.y") },
      { field: "/definition/blueprint/steps/1/parallel/0", code: "unknown_variable", message: missing("vars.ghost.y") },
      { field: "/definition/blueprint/on_error/0", code: "unknown_variable", message: missing("vars.nope.z") },
    ]);
    expect(designerProblems(d, [])).toEqual([]);
  });

  it("goes on the node it is about, or on the group whose pointer holds it", () => {
    const graph = blueprintGraph(seededBlueprint("postgres-cluster")!)!;
    const withProblems = addProblems(graph, [
      { field: "/definition/blueprint/steps/0/when", code: "invalid", message: "No condition." },
      { field: "/definition/blueprint/steps/0/when", code: "invalid", message: "No condition." },
      { field: "/definition/blueprint/steps/1", code: "invalid", message: "The group." },
      { field: "/definition/form_schema", code: "invalid", message: "Not a step." },
    ]);
    expect(withProblems.nodes.find((n) => n.id === "step:nodes")?.problems).toEqual(["No condition."]);
    expect(withProblems.groups[0]?.problems).toEqual(["The group."]);
    expect(withProblems.problems).toEqual(
      expect.arrayContaining([expect.objectContaining({ message: "No condition." })]),
    );
    expect(addProblems(graph, [])).toBe(graph);
  });
});

describe("the editor's and the designer's helpers", () => {
  it("change only what differs", () => {
    expect(minimalChange("a: 1\nb: 2\n", "a: 1\nb: 3\n")).toEqual({ from: 8, to: 9, insert: "3" });
    expect(minimalChange("abc", "abc")).toEqual({ from: 3, to: 3, insert: "" });
    expect(minimalChange("aa", "aaa")).toEqual({ from: 2, to: 2, insert: "a" });
    expect(minimalChange("abca", "a")).toEqual({ from: 1, to: 4, insert: "" });
  });

  it("read a binding written out: numbers and booleans as such, the rest as CEL", () => {
    expect(writtenBinding(" 9187 ")).toBe(9187);
    expect(writtenBinding("-1.5")).toBe(-1.5);
    expect(writtenBinding("true")).toBe(true);
    expect(writtenBinding('"read"')).toBe('"read"');
    expect(writtenBinding("inputs.role + '_x'")).toBe("inputs.role + '_x'");
    expect(writtenBinding("  ")).toBeUndefined();
  });
});

describe("a REST call step", () => {
  const call = { key: "cmdb", title: "The CMDB", rest: { method: "POST", url: "https://h.example/x" } };

  it("is what the step names, and gives its status and what it keeps", () => {
    expect(targetOf(call)).toEqual({ kind: "rest", ref: "" });
    expect(targetValue(targetOf(call)!)).toBe("rest:");
    expect(findTarget(targets, targetOf(call))).toBeUndefined();
    expect(outputsOf(call, targets)).toMatchObject({ open: false, outputs: [{ name: "status" }, { name: "json" }] });
    const sources = bindingSources(
      { blueprint: { steps: [call, { key: "next", item: "x" }] } },
      { g: 1, planned: false },
      targets,
    );
    expect(sources.map((s) => s.value)).toEqual(expect.arrayContaining(["steps.cmdb.status", "steps.cmdb.json"]));
    expect(designerProblems({ blueprint: { steps: [call] } }, targets)).toEqual([]);
  });
});

describe("a wait for a person", () => {
  const step = {
    key: "manager",
    ask: {
      group: "managers",
      form: [
        { key: "size", label: "How big?", type: "number" },
        { key: "ok", label: "OK?", type: "boolean" },
        { key: "why", label: "Why?", type: "string" },
      ],
    },
  };

  it("is a target of its own", () => {
    expect(targetOf(step)).toEqual({ kind: "ask", ref: "" });
    expect(targetValue(targetOf(step)!)).toBe("ask:");
  });

  it("gives later steps who answered and each of its form's answers", () => {
    const { outputs, open } = outputsOf(step, []);
    expect(open).toBe(false);
    expect(outputs.map((o) => [o.name, o.type])).toEqual([
      ["answered", "boolean"],
      ["by", "string"],
      ["comment", "string"],
      ["answer.size", "number"],
      ["answer.ok", "boolean"],
      ["answer.why", "string"],
    ]);
    expect(outputsOf({ key: "m", ask: {} }, []).outputs.map((o) => o.name)).toEqual(["answered", "by", "comment"]);
  });

  it("is offered to the steps after it, not to those beside it", () => {
    const def = { blueprint: { kind: "composite", steps: [step, { key: "next", item: "x" }] } };
    expect(bindingSources(def, { g: 1, planned: false }, []).map((s) => s.value)).toContain(
      "steps.manager.answer.size",
    );
    expect(bindingSources(def, { g: 0, planned: false }, []).map((s) => s.value)).not.toContain(
      "steps.manager.answered",
    );
  });

  it("has nothing to say when it is requested: who asks is known then", () => {
    const def = { blueprint: { kind: "composite", steps: [{ key: "m", ask: { approvers: ["u1"] } }] } };
    expect(designerProblems(def, [])).toEqual([]);
  });
});

describe("a wait for a time and a poll, in the designer", () => {
  const steps = [
    { key: "keep", wait: { for: "72h" } },
    {
      key: "change",
      poll: {
        rest: { method: "GET", url: "https://h.example" },
        until: "steps.change.met",
        every: "1m",
        timeout: "1h",
      },
    },
    { key: "next", item: "x" },
  ];
  const def = { blueprint: { kind: "composite", steps } };

  it("names what they are, and what they give the steps after them", () => {
    expect(targetOf(steps[0]!)).toEqual({ kind: "wait", ref: "" });
    expect(targetOf(steps[1]!)).toEqual({ kind: "poll", ref: "" });
    expect(outputsOf(steps[0]!, []).outputs.map((o) => o.name)).toEqual(["until"]);
    expect(outputsOf(steps[1]!, []).outputs.map((o) => o.name)).toEqual(["status", "json", "met"]);
  });

  it("offers them to the steps after them, and has nothing to say when it is requested", () => {
    const later = bindingSources(def, { g: 2, planned: false }, []).map((s) => s.value);
    expect(later).toEqual(expect.arrayContaining(["steps.keep.until", "steps.change.json", "steps.change.met"]));
    expect(bindingSources(def, { g: 0, planned: false }, []).map((s) => s.value)).not.toContain("steps.keep.until");
    expect(designerProblems({ blueprint: { kind: "composite", steps: steps.slice(0, 2) } }, [])).toEqual([]);
  });
});

describe("a branch's sides and a message", () => {
  const blueprint = {
    kind: "composite",
    steps: [
      { key: "first", awx: { job_template: "a" } },
      {
        key: "b",
        branch: {
          cases: [
            {
              when: "inputs.a",
              steps: [
                { key: "x", awx: { job_template: "x" } },
                { key: "y", awx: { job_template: "y" } },
              ],
            },
            { when: "inputs.b", steps: [{ key: "z", awx: { job_template: "z" } }] },
          ],
          else: [{ key: "o", notify: { owner: true, message: "Hi" } }],
        },
      },
      { key: "last", awx: { job_template: "l" } },
    ],
  };
  const keys = (at: Parameters<typeof earlierSteps>[1]) => earlierSteps(blueprint, at).map((s) => s.key);

  it("flattens a side's steps right after their branch, with their own pointers", () => {
    const flat = flatten(blueprint);
    expect(flat.map((s) => s.key)).toEqual(["first", "b", "x", "y", "z", "o", "last"]);
    expect(flat.find((s) => s.key === "y")).toMatchObject({
      pointer: "/definition/blueprint/steps/1/branch/cases/0/steps/1",
      side: { taken: 1, i: 1 },
    });
    expect(flat.find((s) => s.key === "o")).toMatchObject({
      pointer: "/definition/blueprint/steps/1/branch/else/0",
      side: { taken: 0, i: 0 },
    });
  });

  it("reads its branch and the steps before it on its side, not another side's", () => {
    expect(keys({ g: 1, side: { taken: 1, i: 0 } })).toEqual(["first", "b"]);
    expect(keys({ g: 1, side: { taken: 1, i: 1 } })).toEqual(["first", "b", "x"]);
    expect(keys({ g: 1, side: { taken: 2, i: 0 } })).toEqual(["first", "b"]);
    expect(keys({ g: 1, side: { taken: 0, i: 0 } })).toEqual(["first", "b"]);
    // A branch reads neither its own sides nor itself.
    expect(keys({ g: 1 })).toEqual(["first"]);
    // A later step reads every side: each as a step `when` skipped.
    expect(keys({ g: 2 })).toEqual(["first", "b", "x", "y", "z", "o"]);
  });

  it("offers what a branch and a message give, as bindings", () => {
    const def = { blueprint };
    const values = (at: Parameters<typeof bindingSources>[1]) => bindingSources(def, at, targets).map((s) => s.value);
    expect(values({ g: 2, planned: false })).toEqual(
      expect.arrayContaining(["steps.b.case", "steps.o.told", "steps.first.job_id"]),
    );
    expect(values({ g: 1, planned: false, side: { taken: 2, i: 0 } })).not.toContain("steps.x.job_id");
    expect(outputsOf({ branch: {} }, targets).outputs.map((o) => o.name)).toEqual(["case"]);
    expect(outputsOf({ notify: {} }, targets).outputs.map((o) => o.name)).toEqual(["told"]);
    expect(targetOf({ branch: {} })).toEqual({ kind: "branch", ref: "" });
    expect(targetOf({ notify: {} })).toEqual({ kind: "notify", ref: "" });
  });

  it("refuses a VM on a side and a secret in a message, on the step", () => {
    const vm = targets.find((t) => t.planned)!;
    const def = {
      ui_schema: { fields: { token: { widget: "secret" } } },
      blueprint: {
        kind: "composite",
        steps: [
          {
            key: "b",
            branch: {
              cases: [
                {
                  when: "inputs.a",
                  steps: [
                    { key: "vm", item: vm.ref },
                    { key: "n", notify: { owner: true, message: "It is ${inputs.token}" } },
                  ],
                },
              ],
            },
          },
        ],
      },
    };
    const at = "/definition/blueprint/steps/0/branch/cases/0/steps";
    const found = designerProblems(def, targets);
    expect(found.map((p) => p.field)).toEqual(expect.arrayContaining([`${at}/0/item`, `${at}/1/notify/message`]));
    expect(found.find((p) => p.field === `${at}/0/item`)!.message).toContain("can't be on a side");
    expect(found.find((p) => p.field === `${at}/1/notify/message`)!.message).toContain("the secret input token");
  });
});

describe("loops, catch paths and waits for a signal (ADR-0057)", () => {
  const vm = targets.find((t) => t.planned)!;
  const call = (key: string, extra: Record<string, unknown> = {}) => ({
    key,
    rest: { method: "POST", url: "https://h.example/x" },
    ...extra,
  });
  const bp = {
    kind: "composite",
    steps: [
      call("first"),
      call("dns", { catch: [call("ticket"), call("page")] }),
      { parallel: [call("p1"), call("p2")], catch: [call("tell")] },
      call("last"),
    ],
    on_error: [call("incident"), call("after_incident")],
  };
  const keys = (at: Parameters<typeof earlierSteps>[1]) => earlierSteps(bp, at).map((s) => s.key);

  it("flattens a catch path after what it catches, and the error handler last", () => {
    expect(flatten(bp).map((s) => [s.key, s.g, s.caught?.i ?? null])).toEqual([
      ["first", 0, null],
      ["dns", 1, null],
      ["ticket", 1, 0],
      ["page", 1, 1],
      ["p1", 2, null],
      ["p2", 2, null],
      ["tell", 2, 0],
      ["last", 3, null],
      ["incident", 4, 0],
      ["after_incident", 4, 1],
    ]);
    expect(flatten(bp).find((s) => s.key === "tell")?.pointer).toBe("/definition/blueprint/steps/2/catch/0");
  });

  it("reads before what it catches and before it on its path; later steps read the path", () => {
    const list = "/definition/blueprint/steps/1/catch";
    expect(keys({ g: 1, caught: { list, i: 1 } })).toEqual(["first", "ticket"]);
    expect(keys({ g: 2, caught: { list: "/definition/blueprint/steps/2/catch", i: 0 } })).toEqual([
      "first",
      "dns",
      "ticket",
      "page",
    ]);
    expect(keys({ g: 3 })).toEqual(["first", "dns", "ticket", "page", "p1", "p2", "tell"]);
    expect(keys({ g: 4, caught: { list: "/definition/blueprint/on_error", i: 1 } })).toEqual([
      "first",
      "dns",
      "ticket",
      "page",
      "p1",
      "p2",
      "tell",
      "last",
      "incident",
    ]);
  });

  it("gives a loop's passes, what a caught step says, and what came for a signal", () => {
    const names = (step: Record<string, unknown>) => outputsOf(step, targets).outputs.map((o) => o.name);
    expect(names(call("x", { for_each: { items: "[1]" } }))).toEqual(["items", "count"]);
    expect(names(call("x", { catch: [call("y")] }))).toEqual(["status", "json", "caught", "error"]);
    expect(outputsOf({ signal: { name: "go", timeout: "1h" } }, targets)).toEqual({ outputs: [], open: true });
    expect(targetOf({ signal: {} })).toEqual({ kind: "signal", ref: "" });
  });

  it("refuses a VM step that loops, catches or is caught, as the server does on request", () => {
    const def = {
      blueprint: {
        kind: "composite",
        steps: [
          { key: "a", item: vm.ref, for_each: { items: "[1]" }, catch: [call("fix")] },
          { parallel: [{ key: "b", item: vm.ref }, call("c")], catch: [call("tell")] },
        ],
        on_error: [{ key: "late", item: vm.ref }],
      },
    };
    const found = designerProblems(def, targets).map((p) => [p.field, p.message.replace(/^(a|b|late): /, "")]);
    expect(found).toEqual([
      [
        "/definition/blueprint/steps/0/for_each",
        "a VM step can't loop: its VMs are planned when the request is submitted. Give its item a count instead.",
      ],
      [
        "/definition/blueprint/steps/0/catch",
        "a VM step has no catch path: what it built is the rollback's to delete, so the request can't go on past it.",
      ],
      [
        "/definition/blueprint/steps/1/catch",
        "Step group 2 has a VM step, so it has no catch path: what it built is the rollback's to delete, so the request can't go on past it.",
      ],
      [
        "/definition/blueprint/on_error/0/item",
        "a VM is planned when the request is submitted, before a branch chooses or a step fails, so it can't be on a side of a branch or on a catch path.",
      ],
    ]);
  });
});
