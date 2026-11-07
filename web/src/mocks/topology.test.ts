// The mocks' topologyOf against the server's blueprint.Topology, on the same
// blueprints: internal/seed/testdata/topology.json, which TestTopologyFixture
// keeps current (the seeded VM and composite items, and probes of what a
// requester must not read). A field the canvas draws is added to both, and
// the file rewritten (ADR-0032).
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { maskLiterals, topologyOf } from "./blueprints";

// Vitest runs in web/, where its configuration is.
const fixture = JSON.parse(readFileSync(resolve(process.cwd(), "../internal/seed/testdata/topology.json"), "utf8")) as {
  name: string;
  blueprint: Record<string, unknown>;
  topology: unknown;
}[];

describe("what a requester sees of a blueprint", () => {
  it("lists the seeded items and the probes", () => {
    expect(fixture.map((c) => c.name)).toEqual(
      expect.arrayContaining([
        "rhel9-server",
        "three-tier",
        "postgres-cluster",
        "probe-composite-literals",
        "probe-composite-branch",
      ]),
    );
  });

  it.each(fixture.map((c) => [c.name, c] as const))("%s: the mocks' topology is the server's", (_, c) => {
    expect(topologyOf(c.blueprint)).toEqual(c.topology);
  });

  it.each([
    // The review's probes, as policy's TestMaskLiterals has them.
    ["inputs.token == 'tok_live_SECRET'", false, "inputs.token == '…'"],
    ["{'prod':'vlan-pci-secret'}[inputs.env]", true, "{'…':'…'}[inputs.env]"],
    ['project.code in ["fin", "hr"] && inputs.size == "L"', false, 'project.code in ["…", "…"] && inputs.size == "…"'],
    ["inputs.database != ''", false, "inputs.database != ''"],
    ['inputs.role + "_owner"', false, 'inputs.role + "…"'],
    [`r"a\\d" + b'x' + """multi "line" """ + 'it\\'s' + inputs.x`, false, `r"…" + b'…' + """…""" + '…' + inputs.x`],
    ['"é—ü" == inputs.name', false, '"…" == inputs.name'],
    ["inputs.env == 'prod' // the PCI token is tok_123\n  && inputs.ok", false, "inputs.env == '…'\n  && inputs.ok"],
    ['"dvpg-web-prod-vl110"', true, '"dvpg-web-prod-vl110"'],
    ["['linux-baseline', 'idm-join']", true, "['linux-baseline', 'idm-join']"],
    ['"dvpg-web-prod-vl110"', false, '"…"'],
    ["['linux-baseline', inputs.extra]", true, "['…', inputs.extra]"],
    ["inputs.token == 'tok_live_SECRET", false, "…"],
  ] as const)("masks %s", (expr, lone, want) => {
    expect(maskLiterals(expr, lone)).toBe(want);
  });
});
