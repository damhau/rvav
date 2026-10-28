import { describe, expect, it } from "vitest";
import { readComposite } from "./composite";

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
});
