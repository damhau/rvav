import { describe, expect, it } from "vitest";
import { blueprintGraph, compositeGraph, vmGraph, words } from "./blueprint";
import { describeNode, nodeForPointer, type CanvasGraph } from "./graph";

const ids = (g: CanvasGraph) => g.nodes.map((n) => n.id);
const edge = (g: CanvasGraph, id: string) => g.edges.find((e) => e.id === id);
const node = (g: CanvasGraph, id: string) => {
  const n = g.nodes.find((x) => x.id === id);
  if (!n) throw new Error(`no node ${id} in ${ids(g).join(", ")}`);
  return n;
};

// The seeded PostgreSQL 16 cluster (internal/seed/catalog.go).
const postgres = {
  kind: "composite",
  rollback_on_failure: true,
  steps: [
    {
      key: "nodes",
      title: "Database nodes",
      item: "pg-node",
      with: { nodes: 3, name: "inputs.name", size: "inputs.size" },
    },
    {
      key: "owner",
      title: "The database's owner",
      item: "db-user",
      with: { database: "inputs.name", role_name: 'inputs.name + "_owner"', privileges: '"owner"' },
    },
  ],
};

// ADR-0022's example: a parallel group, a binding to an earlier step, undo and when.
const parallel = {
  kind: "composite",
  steps: [
    { key: "vm", item: "rhel9-server", with: { size: "inputs.size" } },
    {
      parallel: [
        {
          key: "db_user",
          awx: { job_template: "pg-create-role", title: "Create the database user" },
          with: { host: "steps.vm.fqdn" },
          undo: { job_template: "pg-drop-role" },
        },
        { key: "monitoring", workflow: "register-monitoring", with: { fqdn: "steps.vm.fqdn" } },
      ],
    },
    { key: "dba_notice", awx: { job_template: "dba-notice" }, when: 'inputs.database == "ledger"' },
  ],
};

describe("compositeGraph", () => {
  it("draws the steps in order, from the request to done", () => {
    const g = compositeGraph(postgres);
    expect(g).toMatchObject({ kind: "composite", direction: "TB", groups: [], problems: [] });
    expect(ids(g)).toEqual(["inputs", "step:nodes", "step:owner", "end"]);
    expect(g.edges.map((e) => `${e.source}>${e.target}`)).toEqual([
      "inputs>step:nodes",
      "step:nodes>step:owner",
      "step:owner>end",
    ]);
    expect(g.edges.every((e) => e.kind === "sequence" && !e.conditional)).toBe(true);
    expect(node(g, "step:nodes")).toMatchObject({
      kind: "step",
      title: "Database nodes",
      subtitle: "nodes",
      badge: "Item",
      lines: [{ label: "Runs", text: "pg-node", mono: true }],
      relations: ["Runs first"],
      pointer: "/definition/blueprint/steps/0",
    });
    expect(node(g, "step:owner").relations).toEqual(["Runs after Database nodes"]);
    expect(node(g, "end").lines).toEqual([{ label: "On a failure", text: "undo what was built" }]);
    expect(edge(g, "seq:step:nodes>step:owner")?.description).toBe("Database nodes, then The database's owner");
  });

  it("frames a parallel group, and shows when, undo and the steps a step uses", () => {
    const g = compositeGraph(parallel);
    expect(ids(g)).toEqual(["inputs", "step:vm", "step:db_user", "step:monitoring", "step:dba_notice", "end"]);
    expect(g.groups).toEqual([
      {
        id: "parallel:1",
        label: "At the same time",
        nodes: ["step:db_user", "step:monitoring"],
        problems: [],
        pointer: "/definition/blueprint/steps/1",
      },
    ]);
    // Edges join the frame, not each step in it.
    expect(g.edges.map((e) => `${e.source}>${e.target}`)).toEqual([
      "inputs>step:vm",
      "step:vm>parallel:1",
      "parallel:1>step:dba_notice",
      "step:dba_notice>end",
    ]);
    const db = node(g, "step:db_user");
    expect(db).toMatchObject({
      title: "Create the database user",
      subtitle: "db_user",
      badge: "AWX",
      group: "parallel:1",
      pointer: "/definition/blueprint/steps/1/parallel/0",
      relations: ["Runs after vm", "At the same time as monitoring"],
    });
    expect(db.lines).toEqual([
      { label: "Runs", text: "pg-create-role", mono: true },
      { label: "Uses", text: "vm", mono: true },
      { label: "Undo", text: "pg-drop-role", mono: true },
    ]);
    expect(node(g, "step:monitoring")).toMatchObject({ badge: "Workflow", title: "monitoring" });
    // A condition dashes the edge into its step and shows on it.
    expect(edge(g, "seq:parallel:1>step:dba_notice")).toMatchObject({
      conditional: true,
      label: "when",
      description: 'Create the database user and monitoring, then dba_notice when inputs.database == "ledger"',
    });
    expect(node(g, "step:dba_notice").lines).toContainEqual({
      label: "When",
      text: 'inputs.database == "ledger"',
      mono: true,
    });
    expect(node(g, "end").lines).toEqual([{ label: "On a failure", text: "keep what was built" }]);
  });

  it("puts the structural checks on the step they are about", () => {
    const g = compositeGraph({
      kind: "composite",
      steps: [
        { key: "a", awx: { job_template: "x" }, with: { x: "steps.b.fqdn" } },
        { key: "a", item: "rhel9-server" },
        { key: "group", parallel: [{ key: "c", workflow: "w" }, { parallel: [] }] },
      ],
    });
    // The key twice: each by its pointer.
    expect(ids(g)).toEqual([
      "inputs",
      "step@/definition/blueprint/steps/0",
      "step@/definition/blueprint/steps/1",
      "step:c",
      "end",
    ]);
    expect(node(g, "step@/definition/blueprint/steps/0").problems).toEqual(["steps.b is not an earlier step."]);
    expect(node(g, "step@/definition/blueprint/steps/1").problems).toEqual(["Another step has the key a."]);
    expect(g.groups[0]?.problems).toEqual(["A parallel group only lists steps.", "Parallel groups don't nest."]);
    expect(g.problems).toHaveLength(4);
    expect(nodeForPointer(g, "/definition/blueprint/steps/1/key")?.id).toBe("step@/definition/blueprint/steps/1");
    expect(nodeForPointer(g, "/definition/blueprint/steps/10")).toBeUndefined();
  });

  it("has the request and done when there is no step yet", () => {
    const g = compositeGraph({ kind: "composite", steps: [] });
    expect(ids(g)).toEqual(["inputs", "end"]);
    expect(g.edges.map((e) => e.id)).toEqual(["seq:inputs>end"]);
    expect(node(g, "end").relations).toEqual(["No step yet"]);
  });
});

// A three-tier item on an NSX segment, in groups with the rules between them.
const threeTier = {
  kind: "vm",
  template: "rhel9-template",
  guest: { os_family: "linux" },
  segments: [{ key: "app", title: "Application" }],
  bindings: { network: "segments.app", groups: "inputs.extra_groups" },
  awx_templates: { "linux-baseline": { title: "Linux baseline", job_template: "linux-baseline" } },
  security_groups: [
    {
      key: "web",
      title: "Web servers",
      rules: [
        { from: ["any"], services: ["HTTPS"] },
        { from: ["nsx:grp-admin-jump"], services: ["SSH"] },
      ],
    },
    { key: "app", title: "App servers", rules: [{ from: ["web"], services: ["TCP-8080"] }] },
    {
      key: "db",
      title: "Databases",
      rules: [
        { from: ["app"], services: ["MSSQL"] },
        { from: ["any"], services: ["any"], action: "drop" },
      ],
    },
  ],
  components: [
    {
      key: "web",
      title: "Web",
      count: "inputs.web_nodes",
      anti_affinity: true,
      groups: ["web"],
      bindings: { post_config: '["linux-baseline"]' },
    },
    { key: "app", title: "App", count: 2, groups: ["app", "nsx:grp-backup-clients"] },
    {
      key: "db",
      title: "SQL Server",
      template: "win2022-template",
      guest: { os_family: "windows" },
      groups: ["db"],
      disks: [{ size_gb: 200, storage_policy: "Gold" }, { size_gb: "inputs.log_gb" }],
      nics: [{ key: "backup", network: '"dvpg-backup-vl900"', ipam: "dhcp" }],
    },
  ],
};

describe("vmGraph", () => {
  it("draws a three-tier item's components, networks, groups and the rules between them", () => {
    const g = vmGraph(threeTier);
    expect(g).toMatchObject({ kind: "vm", direction: "LR", groups: [], problems: [] });
    expect(ids(g)).toEqual([
      "component:web",
      "component:app",
      "component:db",
      'network:"dvpg-backup-vl900"',
      "segment:app",
      "sg:web",
      "sg:app",
      "sg:db",
      "groups:inputs.extra_groups",
      "nsx:grp-backup-clients",
      "nsx:grp-admin-jump",
      "any",
    ]);
    expect(node(g, "component:web")).toMatchObject({
      badge: "VMs",
      lines: [
        { label: "Count", text: "inputs.web_nodes", mono: true },
        { label: "Template", text: "rhel9-template · linux", mono: true },
        { text: "On separate hosts" },
        { label: "Then", text: "Linux baseline" },
      ],
    });
    expect(node(g, "component:app").badge).toBe("2 VMs");
    expect(node(g, "component:db").lines).toEqual([
      { label: "Template", text: "win2022-template · windows", mono: true },
      { label: "Disks", text: "system, 200 GB (Gold), inputs.log_gb GB" },
    ]);
    expect(node(g, "component:db").relations).toEqual([
      "Adapter nic1 on Application",
      "Adapter backup on dvpg-backup-vl900, by DHCP",
      "In Databases",
      "In Groups chosen in the request",
    ]);

    const byKind = (k: string) => g.edges.filter((e) => e.kind === k).map((e) => `${e.source}>${e.target}`);
    expect(byKind("network")).toEqual([
      "segment:app>component:web",
      "segment:app>component:app",
      "segment:app>component:db",
      'network:"dvpg-backup-vl900">component:db',
    ]);
    expect(edge(g, "nic:component:db:backup")?.label).toBe("backup · DHCP");
    expect(byKind("member")).toEqual([
      "component:web>sg:web",
      "component:web>groups:inputs.extra_groups",
      "component:app>sg:app",
      "component:app>nsx:grp-backup-clients",
      "component:app>groups:inputs.extra_groups",
      "component:db>sg:db",
      "component:db>groups:inputs.extra_groups",
    ]);
    expect(byKind("rule")).toEqual([
      "any>sg:web",
      "nsx:grp-admin-jump>sg:web",
      "sg:web>sg:app",
      "sg:app>sg:db",
      "any>sg:db",
    ]);
    expect(edge(g, "rule:web:0:0")).toMatchObject({
      label: "HTTPS",
      deny: false,
      description: "Allows HTTPS from Any to Web servers",
    });
    expect(edge(g, "rule:db:1:0")).toMatchObject({
      label: "any service · drop",
      deny: true,
      description: "Drops any service from Any to Databases",
    });
    expect(node(g, "sg:db").relations).toEqual([
      "Holds SQL Server",
      "Allows MSSQL from App servers",
      "Drops any service from Any",
    ]);
    expect(node(g, "sg:app").relations).toContain("Rule into Databases: allows MSSQL");
    expect(node(g, "nsx:grp-admin-jump")).toMatchObject({
      title: "grp-admin-jump",
      lines: [{ text: "Existing, in NSX" }],
    });
  });

  it("draws a single-VM item as one VM on the network the request chooses", () => {
    const g = vmGraph({ kind: "vm", template: "tpl", bindings: { network: "inputs.network" } });
    expect(ids(g)).toEqual(["component:vm", "network:inputs.network"]);
    expect(node(g, "component:vm")).toMatchObject({
      title: "Virtual machine",
      badge: "VM",
      pointer: "/definition/blueprint",
    });
    expect(node(g, "network:inputs.network")).toMatchObject({
      title: "From the request",
      subtitle: "inputs.network",
      lines: [],
    });
    expect(g.edges).toHaveLength(1);
    expect(node(g, "component:vm").relations).toEqual(["Adapter nic1 on the request's network (inputs.network)"]);
    // Without a network binding, placement chooses.
    expect(ids(vmGraph({ kind: "vm", template: "tpl" }))).toEqual(["component:vm", "network:auto"]);
  });

  it("marks what names a segment or group the item doesn't have", () => {
    const g = vmGraph({
      kind: "vm",
      components: [{ key: "web", bindings: { network: "segments.dmz" }, groups: ["front"] }],
    });
    expect(node(g, "segment:dmz").problems).toEqual(["No segment has the key dmz."]);
    expect(node(g, "sg:front").problems).toEqual(["No security group has the key front."]);
    expect(g.problems.map((p) => `${p.field} ${p.code}`)).toEqual([
      "/definition/blueprint/components/0/bindings/network unknown_segment",
      "/definition/blueprint/components/0/groups/0 unknown_group",
    ]);
  });
});

describe("blueprintGraph", () => {
  it("draws composites and VM items, and nothing else", () => {
    expect(blueprintGraph(postgres)?.kind).toBe("composite");
    expect(blueprintGraph(threeTier)?.kind).toBe("vm");
    expect(blueprintGraph({ kind: "awx", job_template: "x" })).toBeNull();
    expect(blueprintGraph(null)).toBeNull();
  });

  it("names each node with everything it shows", () => {
    const g = compositeGraph(parallel);
    expect(describeNode(node(g, "step:db_user"), "failed")).toBe(
      "AWX Create the database user. db_user. Runs: pg-create-role. Uses: vm. Undo: pg-drop-role. " +
        "Runs after vm. At the same time as monitoring. Failed",
    );
  });

  it("joins names", () => {
    expect(words([])).toBe("");
    expect(words(["a"])).toBe("a");
    expect(words(["a", "b", "c"])).toBe("a, b and c");
  });
});
