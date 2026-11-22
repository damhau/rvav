import { describe, expect, it } from "vitest";
import { seededBlueprint } from "./blueprints";
import { vmBlueprintProblems } from "./vmBlueprint";

const at = "/definition/blueprint";
const fields = (bp: Record<string, unknown>) => vmBlueprintProblems(bp, at).map((e) => `${e.field} ${e.code}`);

describe("a VM blueprint's checks on save, as the server's", () => {
  it("passes every seeded VM blueprint", () => {
    for (const slug of ["rhel9-server", "ubuntu-server", "windows-server", "web-app", "three-tier", "pg-node"])
      expect(vmBlueprintProblems(seededBlueprint(slug)!, at), slug).toEqual([]);
  });

  it("compiles a network against a request's variables: a bare name is a variable", () => {
    const problems = vmBlueprintProblems(
      { kind: "vm", bindings: { network: "dvpg-web" }, nics: [{ network: "inputs.backup + suffix" }] },
      at,
    );
    expect(problems.map((p) => p.field)).toEqual([`${at}/bindings/network`, `${at}/nics/0/network`]);
    expect(problems[0]!.message).toBe(
      "This binding is not valid CEL: compile \"dvpg-web\": ERROR: <input>:1:1: undeclared reference to 'dvpg' (in container '')",
    );
    expect(problems[1]!.message).toMatch(
      /^The network is not valid CEL: .*:1:17: undeclared reference to 'suffix'.* \(a name is quoted: '"dvpg-backup-vl900"'\)\.$/,
    );
    expect(fields({ kind: "vm", bindings: { network: '"dvpg-web"', vcpu: "sizes[inputs.size].vcpu" } })).toEqual([]);
  });

  it("keeps VMs apart only for a count of two or more, and counts at most 20 VMs", () => {
    expect(
      fields({
        kind: "vm",
        components: [
          { key: "web", anti_affinity: true },
          { key: "db", count: 1, anti_affinity: true },
          { key: "app", count: "inputs.n", anti_affinity: true },
          { key: "big", count: 19 },
          { key: "neg", count: -1 },
        ],
      }),
    ).toEqual([
      `${at}/components/0/anti_affinity invalid`,
      `${at}/components/1/anti_affinity invalid`,
      `${at}/components/4/count out_of_range`,
      `${at}/components out_of_range`,
    ]);
  });

  it("checks keys, groups and what a component is built after", () => {
    expect(
      fields({
        kind: "vm",
        segments: [{ key: "app" }, { key: "app" }],
        security_groups: [{ key: "web", rules: [{ from: ["nope"], services: ["HTTPS"] }] }],
        components: [
          { key: "web", groups: ["web", "db", "nsx:grp-ok"], depends_on: ["db"] },
          { key: "db", depends_on: ["web", "db", "cache"] },
          { key: "vm" },
          { key: "Web" },
          { key: "web" },
        ],
      }),
    ).toEqual([
      `${at}/segments/1/key duplicate`,
      `${at}/security_groups/0/rules/0/from/0 unknown`,
      `${at}/components/0/groups/1 unknown`,
      `${at}/components/2/key reserved`,
      `${at}/components/3/key pattern`,
      `${at}/components/4/key duplicate`,
      `${at}/components/1/depends_on/1 invalid`,
      `${at}/components/1/depends_on/2 unknown_component`,
      `${at}/components/0/depends_on/0 cycle`,
    ]);
  });

  it("lets only the tags binding read what VMs are built with", () => {
    expect(
      fields({
        kind: "vm",
        components: [
          { key: "db" },
          { key: "web", bindings: { tags: '["app/" + components.db.addresses[0]]', network: "self.name" } },
        ],
      }),
    ).toEqual([`${at}/components/1/bindings/network invalid`]);
  });
});
