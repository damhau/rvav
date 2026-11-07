import { describe, expect, it } from "vitest";
import { parseUrl, readComposite } from "./composite";

describe("readComposite", () => {
  it("groups the steps and says what each uses", () => {
    const v = readComposite({
      kind: "composite",
      steps: [
        { key: "vm", item: "rhel9-server" },
        {
          parallel: [
            {
              key: "db_user",
              item: "db-user",
              with: { host: "steps.vm.fqdn" },
              undo: { job_template: "pg-drop-role" },
            },
            { key: "monitoring", awx: { job_template: "monitoring-agent", title: "Monitoring" } },
          ],
        },
      ],
    });
    expect(v.problems).toEqual([]);
    expect(v.groups.map((g) => g.map((s) => s.key))).toEqual([["vm"], ["db_user", "monitoring"]]);
    expect(v.groups[1]![0]).toMatchObject({ kind: "item", uses: ["vm"], undo: "pg-drop-role" });
    expect(v.groups[1]![1]).toMatchObject({ kind: "awx", title: "Monitoring", target: "monitoring-agent" });
  });

  it("refuses what the server refuses", () => {
    const v = readComposite({
      steps: [
        { key: "a", awx: { job_template: "x" }, with: { x: "steps.b.fqdn" } },
        { key: "a", item: "rhel9-server", awx: { job_template: "x" } },
        {
          parallel: [
            { key: "c", workflow: "w", with: { y: "steps.d.result" } },
            { key: "d", item: "x" },
          ],
        },
        { key: "Bad", item: "x", undo: {} },
      ],
    });
    expect(v.problems.map((p) => `${p.field} ${p.code}`)).toEqual([
      "/definition/blueprint/steps/0 unknown_step",
      "/definition/blueprint/steps/1/key duplicate",
      "/definition/blueprint/steps/1 one_of",
      "/definition/blueprint/steps/2/parallel/0 unknown_step",
      "/definition/blueprint/steps/3/key pattern",
      "/definition/blueprint/steps/3/undo one_of",
    ]);
  });

  const rest = (call: Record<string, unknown>, extra: Record<string, unknown> = {}) => ({
    kind: "composite",
    steps: [
      { key: "vm", item: "x" },
      { key: "cmdb", rest: { method: "POST", ...call }, ...extra },
    ],
  });
  const fields = (bp: Record<string, unknown>) => readComposite(bp).problems.map((p) => p.field.split("/steps/1/")[1]);

  it("reads a REST call: what it calls, what it uses, its undo", () => {
    const { groups, problems } = readComposite(
      rest(
        { url: "https://h.example/ci/${steps.vm.name}?e=${environment}", body: "{'o': inputs.o}" },
        {
          undo: {
            rest: { method: "DELETE", url: "https://h.example/ci/${steps.cmdb.json.id}/${steps.vm.id}" },
          },
        },
      ),
    );
    expect(problems).toEqual([]);
    const s = groups[1]![0]!;
    expect(s.kind).toBe("rest");
    expect(s.target).toBe("POST https://h.example/ci/${steps.vm.name}?e=${environment}");
    expect(s.undo).toBe("DELETE https://h.example/ci/${steps.cmdb.json.id}/${steps.vm.id}");
    // An undo may read its own step; that isn't a use of another.
    expect(s.uses).toEqual(["vm"]);
  });

  it("refuses in a REST call what the server refuses", () => {
    const url = "https://h.example/x";
    expect(fields(rest({ url: "" }))).toEqual(["rest/url"]);
    expect(fields(rest({ url, method: "FETCH" }))).toEqual(["rest/method"]);
    expect(fields(rest({ url, method: "GET", body: "1" }))).toEqual(["rest/body"]);
    expect(fields(rest({ url, accept: [200, 99] }))).toEqual(["rest/accept/1"]);
    expect(fields(rest({ url }, { with: { a: "1" } }))).toEqual(["with"]);
    for (const bad of [
      "ftp://h.example/x",
      "not a URL",
      "https://${inputs.h}/x",
      "https://h.example:${inputs.p}/x",
      "https://h.example${inputs.p}",
      "${inputs.s}://h.example/x",
      "https://u:pw@h.example/x",
      "https://h.example/${inputs.a",
      "https://h.example/${}",
    ])
      expect(fields(rest({ url: bad })), bad).toEqual(["rest/url"]);
    // A step reads earlier steps only; the undo its own step too, not a later one.
    const later = readComposite({
      kind: "composite",
      steps: [
        {
          key: "a",
          rest: { method: "GET", url: "https://h.example/${steps.a.json.id}" },
          undo: { rest: { method: "DELETE", url: "https://h.example/${steps.b.id}" } },
        },
        { key: "b", item: "x" },
      ],
    }).problems.map((p) => p.message);
    expect(later).toEqual(["steps.a is not an earlier step.", "steps.b is not an earlier step."]);
    // An undo is one of its kinds, and a REST one has no inputs.
    expect(fields(rest({ url }, { undo: { job_template: "t", rest: { method: "GET", url } } }))).toEqual(["undo"]);
    expect(fields(rest({ url }, { undo: { with: { a: "1" }, rest: { method: "GET", url } } }))).toEqual(["undo/with"]);
    expect(fields(rest({ url }, { undo: { rest: { method: "GET", url: "" } } }))).toEqual(["undo/rest/url"]);
    // A step is one kind.
    const both = readComposite({ steps: [{ key: "x", item: "a", rest: { method: "GET", url } }] });
    expect(both.problems.map((p) => p.message)).toEqual(["A step is one of: item, awx, workflow, rest or ask."]);
  });

  describe("a wait for a person", () => {
    const problems = (ask: Record<string, unknown>, more: Record<string, unknown> = {}) =>
      readComposite({ steps: [{ key: "a", ask, ...more }] }, "").problems.map((p) => [p.field, p.message]);
    const secret = expect.stringContaining("can't ask for a secret") as string;

    it("says who is asked, how long, and for what, as the server does", () => {
      const v = readComposite({
        steps: [
          { key: "a", ask: { group: "managers", approvers: ["u1", "u2"], timeout: "48h", on_timeout: "continue" } },
          { key: "b", ask: { requester: true, owner: true, form: [{ key: "x", label: "X", type: "string" }] } },
          { key: "c", ask: {} },
        ],
      });
      expect(v.groups.flat().map((s) => [s.kind, s.ask])).toEqual([
        ["ask", { who: "managers, 2 people", wants: "to approve or reject", timeout: "48h, then it goes on" }],
        ["ask", { who: "the requester, the owner", wants: "1 answer", timeout: undefined }],
        ["ask", { who: "a person", wants: "to approve or reject", timeout: undefined }],
      ]);
    });

    it("refuses what the server refuses", () => {
      const asks = { requester: true };
      expect(problems({})).toEqual([["/steps/0/ask", expect.stringContaining("Say who is asked")]]);
      expect(problems({ ...asks, message: "x".repeat(2001) })).toEqual([
        ["/steps/0/ask/message", "At most 2000 characters."],
      ]);
      expect(problems({ ...asks, timeout: "soon" })).toEqual([["/steps/0/ask/timeout", "A duration such as 48h."]]);
      expect(problems({ ...asks, timeout: "1ms" })).toEqual([["/steps/0/ask/timeout", "A duration such as 48h."]]);
      expect(problems({ ...asks, on_timeout: "fail" })).toEqual([
        ["/steps/0/ask/on_timeout", "What happens on time-out needs a timeout."],
      ]);
      expect(problems({ ...asks, timeout: "1h", on_timeout: "later" })).toEqual([
        ["/steps/0/ask/on_timeout", "fail, continue or escalate."],
      ]);
      expect(problems({ ...asks, timeout: "1h", on_timeout: "escalate" })).toEqual([
        ["/steps/0/ask/escalate_to", expect.stringContaining("Say whom to escalate to")],
      ]);
      expect(problems(asks, { with: { a: "1" }, undo: { job_template: "t" } })).toEqual([
        ["/steps/0/with", "A wait for a person has no inputs."],
        ["/steps/0/undo", "A wait for a person has nothing to undo."],
      ]);
    });

    it("refuses a form's secrets and what its questions get wrong", () => {
      const form = (...f: Record<string, unknown>[]) => problems({ requester: true, form: f });
      const base = { key: "q", label: "Q", type: "string" };
      expect(form({ ...base, secret: true })).toEqual([["/steps/0/ask/form/0", secret]]);
      expect(form({ ...base, type: "password" })).toEqual([["/steps/0/ask/form/0", secret]]);
      expect(form(base, base)).toEqual([["/steps/0/ask/form/1/key", "Another question has the key q."]]);
      expect(form({ ...base, label: " " })).toEqual([["/steps/0/ask/form/0/label", "Say what it asks."]]);
      expect(form({ ...base, type: "date" })).toEqual([
        ["/steps/0/ask/form/0/type", "string, number, boolean or choice."],
      ]);
      expect(form({ ...base, type: "choice" })).toEqual([["/steps/0/ask/form/0/options", "List what it may be."]]);
      expect(form({ ...base, options: ["a"] })).toEqual([
        ["/steps/0/ask/form/0/options", "Only a choice has options."],
      ]);
      expect(form({ ...base, default: 3 })).toEqual([["/steps/0/ask/form/0/default", "Give text."]]);
      expect(form({ ...base, type: "choice", options: ["a", "b"], default: "c" })).toEqual([
        ["/steps/0/ask/form/0/default", "Choose one of: a, b."],
      ]);
    });

    it("reads a step's outputs as any other's", () => {
      const v = readComposite({
        steps: [
          { key: "a", ask: { requester: true } },
          { key: "b", item: "x", with: { size: "steps.a.answer.size" } },
        ],
      });
      expect(v.problems).toEqual([]);
      expect(v.groups[1]![0]!.uses).toEqual(["a"]);
    });
  });
});

describe("parseUrl", () => {
  it("reads a URL's expressions as the server does", () => {
    expect(parseUrl("https://h/a/${steps.vm.name}?x=${{'a}': 'b'}[inputs.k]}&y=${ inputs.y }").parts).toEqual([
      { text: "https://h/a/" },
      { expr: "steps.vm.name" },
      { text: "?x=" },
      { expr: "{'a}': 'b'}[inputs.k]" },
      { text: "&y=" },
      { expr: "inputs.y" },
    ]);
    for (const bad of ["https://h/${inputs.a", "https://h/${'}", "https://h/${}"])
      expect(parseUrl(bad).error).toBeTruthy();
  });
});
