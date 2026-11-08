import { describe, expect, it } from "vitest";
import { allSteps, parseUrl, readComposite, secretInMessages } from "./composite";

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
    expect(both.problems.map((p) => p.message)).toEqual([
      "A step is one of: item, awx, workflow, rest, ask, wait, poll, branch or notify.",
    ]);
  });

  describe("a wait for a time", () => {
    const problems = (wait: Record<string, unknown>, more: Record<string, unknown> = {}) =>
      readComposite({ steps: [{ key: "a", wait, ...more }] }, "").problems.map((p) => [p.field, p.message]);

    it("reads how long, or until when and what that reads", () => {
      const v = readComposite({
        steps: [
          { key: "a", item: "x" },
          { key: "b", wait: { for: "72h" } },
          { key: "c", wait: { until: "steps.a.at" } },
        ],
      });
      expect(v.problems).toEqual([]);
      expect(v.groups.flat().map((s) => [s.kind, s.wait, s.uses])).toEqual([
        ["item", undefined, []],
        ["wait", { for: "72h", until: undefined }, []],
        ["wait", { for: undefined, until: "steps.a.at" }, ["a"]],
      ]);
    });

    it("refuses what the server refuses", () => {
      expect(problems({})).toEqual([["/steps/0/wait", "Say how long it waits, or until when."]]);
      expect(problems({ for: "1h", until: "inputs.x" })).toEqual([
        ["/steps/0/wait", "A wait is for a duration or until a time, not both."],
      ]);
      expect(problems({ for: "soon" })).toEqual([["/steps/0/wait/for", "A duration such as 72h."]]);
      expect(problems({ for: "1ms" })).toEqual([["/steps/0/wait/for", "A duration such as 72h."]]);
      expect(problems({ for: "1h" }, { with: { a: "1" }, undo: { job_template: "t" } })).toEqual([
        ["/steps/0/with", "A wait for a time has no inputs."],
        ["/steps/0/undo", "A wait for a time has nothing to undo."],
      ]);
      expect(problems({ until: "steps.later.at" })).toEqual([["/steps/0", "steps.later is not an earlier step."]]);
    });
  });

  describe("a poll", () => {
    const rest = { method: "GET", url: "https://itsm.example/change/${inputs.name}" };
    const good = { rest, until: "steps.a.json == 'approved'", every: "10m", timeout: "72h" };
    const problems = (poll: Record<string, unknown>, more: Record<string, unknown> = {}) =>
      readComposite({ steps: [{ key: "a", poll, ...more }] }, "").problems.map((p) => [p.field, p.message]);

    it("reads what it calls and waits for, not itself as an earlier step", () => {
      const v = readComposite({
        steps: [
          { key: "first", item: "x" },
          { key: "a", poll: { ...good, rest: { method: "PUT", url: "https://itsm.example/${steps.first.id}" } } },
        ],
      });
      expect(v.problems).toEqual([]);
      const s = v.groups[1]![0]!;
      expect([s.kind, s.target, s.uses]).toEqual(["poll", "PUT https://itsm.example/${steps.first.id}", ["first"]]);
      expect(s.poll).toEqual({ until: good.until, every: "10m", timeout: "72h, then the step fails" });
      const goesOn = readComposite({ steps: [{ key: "a", poll: { ...good, on_timeout: "continue" } }] });
      expect(goesOn.groups[0]![0]!.poll!.timeout).toBe("72h, then it goes on");
    });

    it("refuses what the server refuses", () => {
      const repeats = expect.stringContaining("may only GET, PUT or DELETE") as string;
      expect(problems(good)).toEqual([]);
      expect(problems({ ...good, rest: undefined })).toEqual([["/steps/0/poll/rest", "Give the call it repeats."]]);
      expect(problems({ ...good, rest: { ...rest, method: "POST" } })).toEqual([
        ["/steps/0/poll/rest/method", repeats],
      ]);
      expect(problems({ ...good, rest: { ...rest, method: "PATCH" } })).toEqual([
        ["/steps/0/poll/rest/method", repeats],
      ]);
      expect(problems({ ...good, until: "" })).toEqual([
        ["/steps/0/poll/until", "Say what it waits for: a condition on the call's answer."],
      ]);
      expect(problems({ ...good, until: "inputs.x == 'y'" })).toEqual([
        ["/steps/0/poll/until", expect.stringContaining("Read the call's answer, as steps.a.json")],
      ]);
      expect(problems({ ...good, every: "" })).toEqual([
        ["/steps/0/poll/every", "Say how often it checks: a duration such as 10m."],
      ]);
      expect(problems({ ...good, every: "often" })).toEqual([["/steps/0/poll/every", "A duration such as 10m."]]);
      expect(problems({ ...good, every: "59s" })).toEqual([
        ["/steps/0/poll/every", expect.stringContaining("At least 1m")],
      ]);
      expect(problems({ ...good, timeout: "" })).toEqual([
        ["/steps/0/poll/timeout", "Give it a deadline: a duration such as 72h."],
      ]);
      expect(problems({ ...good, timeout: "soon" })).toEqual([["/steps/0/poll/timeout", "A duration such as 72h."]]);
      expect(problems({ ...good, timeout: "5m" })).toEqual([
        ["/steps/0/poll/timeout", "At least as long as how often it checks."],
      ]);
      expect(problems({ ...good, on_timeout: "escalate" })).toEqual([
        ["/steps/0/poll/on_timeout", "fail or continue."],
      ]);
      expect(problems(good, { with: { a: "1" }, undo: { job_template: "t" } })).toEqual([
        ["/steps/0/with", expect.stringContaining("A poll has no inputs")],
        ["/steps/0/undo", "A poll has nothing to undo."],
      ]);
    });
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
      expect(form({ ...base, default: "𝄞".repeat(4096) })).toEqual([]);
      expect(form({ ...base, default: "é".repeat(4097) })).toEqual([
        ["/steps/0/ask/form/0/default", "At most 4096 characters."],
      ]);
    });

    it("counts characters as the server does, not UTF-16 units", () => {
      for (const c of ["é", "𝄞"]) {
        expect(problems({ requester: true, message: c.repeat(2000) })).toEqual([]);
        expect(problems({ requester: true, message: c.repeat(2001) })).toEqual([
          ["/steps/0/ask/message", "At most 2000 characters."],
        ]);
      }
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

describe("a branch", () => {
  const problems = (branch: Record<string, unknown>, more: Record<string, unknown> = {}, before: unknown[] = []) =>
    readComposite({ steps: [...before, { key: "b", branch, ...more }] }, "").problems.map((p) => [p.field, p.message]);
  const leaf = (key: string, more: Record<string, unknown> = {}) => ({
    key,
    awx: { job_template: "t" },
    ...more,
  });

  it("reads its sides, each with its steps, and what it reads", () => {
    const view = readComposite({
      steps: [
        {
          key: "b",
          title: "Which way",
          branch: {
            cases: [
              { title: "Refused", when: "inputs.ok == false", steps: [leaf("no")] },
              { when: "inputs.size > 500", steps: [] },
            ],
            else: [leaf("yes"), leaf("then", { when: "steps.yes.status == 200" })],
          },
        },
      ],
    });
    expect(view.problems).toEqual([]);
    const s = view.groups[0]![0]!;
    expect(s.kind).toBe("branch");
    expect(s.sides!.map((x) => [x.label, x.when, x.taken, x.steps.map((y) => y.key)])).toEqual([
      ["Refused", "inputs.ok == false", 1, ["no"]],
      ["Case 2", "inputs.size > 500", 2, []],
      ["Otherwise", undefined, 0, ["yes", "then"]],
    ]);
    expect(allSteps(view).map((x) => x.key)).toEqual(["b", "no", "yes", "then"]);
    expect(view.groups.flat().map((x) => x.key)).toEqual(["b"]);
  });

  it("has an empty otherwise that goes on, when it writes none", () => {
    const s = readComposite({ steps: [{ key: "b", branch: { cases: [{ when: "inputs.a" }] } }] }).groups[0]![0]!;
    expect(s.sides!.map((x) => [x.label, x.steps.length])).toEqual([
      ["Case 1", 0],
      ["Otherwise", 0],
    ]);
  });

  it("refuses what the server refuses", () => {
    expect(problems({ cases: [] })).toEqual([
      ["/steps/0/branch/cases", "Give it at least one case: a condition and the steps it runs."],
    ]);
    expect(problems({ cases: [{ when: "" }] })).toEqual([
      ["/steps/0/branch/cases/0/when", "Say when this case is taken: a CEL condition."],
    ]);
    expect(problems({ cases: [{ when: "inputs.a" }] }, { when: "inputs.b" }).map((p) => p[1])).toEqual([
      "A branch has no condition of its own: its cases have.",
    ]);
    expect(problems({ cases: [{ when: "inputs.a" }] }, { undo: { job_template: "x" } }).map((p) => p[1])).toEqual([
      "A branch has nothing to undo: only the steps that ran on its side are undone.",
    ]);
    // Nothing nests in a branch, and it nests in nothing.
    expect(
      problems({ cases: [{ when: "inputs.a", steps: [{ key: "inner", branch: { cases: [{ when: "inputs.b" }] } }] }] }),
    ).toEqual([["/steps/0/branch/cases/0/steps/0", expect.stringContaining("can't be on a side of a branch")]]);
    expect(problems({ cases: [{ when: "inputs.a", steps: [{ parallel: [leaf("p")] }] }] })).toEqual([
      ["/steps/0/branch/cases/0/steps/0", expect.stringContaining("parallel")],
    ]);
    expect(
      readComposite({
        steps: [{ parallel: [leaf("a"), { key: "b", branch: { cases: [{ when: "inputs.a" }] } }] }],
      }).problems.map((p) => p.message),
    ).toEqual([expect.stringContaining("parallel group")]);
    // A key is the composite's, on whichever side.
    expect(
      problems({
        cases: [
          { when: "inputs.a", steps: [leaf("x")] },
          { when: "inputs.b", steps: [leaf("x")] },
        ],
      }).map((p) => p[0]),
    ).toEqual(["/steps/0/branch/cases/1/steps/0/key"]);
  });

  it("refuses a step reading another side, and a case reading its own branch", () => {
    expect(
      problems({
        cases: [
          { when: "inputs.a", steps: [leaf("x")] },
          { when: "inputs.b", steps: [leaf("y", { when: "steps.x.status == 200" })] },
        ],
      }).map((p) => p[0]),
    ).toEqual(["/steps/0/branch/cases/1/steps/0"]);
    expect(problems({ cases: [{ when: "steps.b.case == 1" }] }).map((p) => p[0])).toEqual(["/steps/0"]);
  });

  it("lets a side read its branch and its earlier steps, and a later step any of them", () => {
    const view = readComposite({
      steps: [
        {
          key: "b",
          branch: {
            cases: [
              {
                when: "inputs.a",
                steps: [leaf("x", { when: "steps.b.case == 1" }), leaf("y", { when: "steps.x.ok" })],
              },
            ],
          },
        },
        leaf("after", { when: "has(steps.x) && steps.x.ok" }),
      ],
    });
    expect(view.problems).toEqual([]);
    expect(allSteps(view).find((s) => s.key === "after")!.uses).toEqual(["x"]);
  });
});

describe("a message", () => {
  const read = (notify: Record<string, unknown>) => readComposite({ steps: [{ key: "n", notify }] }, "");
  const problems = (notify: Record<string, unknown>) => read(notify).problems.map((p) => [p.field, p.message]);

  it("says who it tells and what it says", () => {
    const s = read({ requester: true, groups: ["dba"], users: ["usr_1"], message: "Ready: ${inputs.role}" })
      .groups[0]![0]!;
    expect(s.kind).toBe("notify");
    expect(s.notify).toEqual({ who: "the requester, dba, 1 person", message: "Ready: ${inputs.role}" });
  });

  it("refuses what the server refuses", () => {
    expect(problems({ message: "Hello" })).toEqual([
      ["/steps/0/notify", "Say who is told: the requester, the owner, groups or people."],
    ]);
    expect(problems({ requester: true })).toEqual([["/steps/0/notify/message", "Say what it says."]]);
    expect(problems({ requester: true, message: "x".repeat(2001) })).toEqual([
      ["/steps/0/notify/message", "At most 2000 characters."],
    ]);
    expect(problems({ requester: true, message: "Bad ${" })[0]![0]).toBe("/steps/0/notify/message");
    // Characters, as the server counts them: one for an accented letter, and
    // one for a letter outside the BMP, two UTF-16 units in .length.
    for (const c of ["é", "𝄞"]) {
      expect(problems({ requester: true, message: c.repeat(2000) })).toEqual([]);
      expect(problems({ requester: true, message: c.repeat(2001) })).toEqual([
        ["/steps/0/notify/message", "At most 2000 characters."],
      ]);
    }
    expect(
      readComposite({ steps: [{ key: "n", notify: { requester: true, message: "x" }, undo: { job_template: "t" } }] })
        .problems[0]!.message,
    ).toContain("nothing to undo");
  });

  it("reads what it fills in", () => {
    const view = readComposite({
      steps: [
        { key: "first", awx: { job_template: "t" } },
        { key: "n", notify: { owner: true, message: "Ready: ${steps.first.fqdn}" } },
      ],
    });
    expect(view.groups[1]![0]!.uses).toEqual(["first"]);
  });

  it("refuses a secret input, however it is reached", () => {
    const def = (message: string) => ({
      ui_schema: { fields: { token: { widget: "secret" }, name: {} } },
      blueprint: {
        kind: "composite",
        steps: [
          {
            key: "b",
            branch: { cases: [{ when: "inputs.a", steps: [{ key: "n", notify: { owner: true, message } }] }] },
          },
        ],
      },
    });
    const at = "/definition/blueprint/steps/0/branch/cases/0/steps/0/notify/message";
    expect(secretInMessages(def("Fine: ${inputs.name}"))).toEqual([]);
    for (const m of ["Yours: ${inputs.token}", "${inputs['token']}", "${size(inputs.token)}"])
      expect(secretInMessages(def(m)).map((p) => p.field)).toEqual([at]);
  });
});
