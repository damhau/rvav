import { parse } from "yaml";
import { describe, expect, it } from "vitest";
import { vmGraph } from "@/components/canvas";
import { seededBlueprint } from "@/mocks/blueprints";
import { toYaml } from "../definitionYaml";
import {
  addComponent,
  addDisk,
  addNetwork,
  addNsxGroup,
  addResource,
  addSecurityGroup,
  addSegment,
  connect,
  connection,
  designable,
  EditError,
  freeKey,
  removeEdge,
  removeNode,
  rename,
  setField,
  setNetwork,
} from "./edits";

const definition = (blueprint: Record<string, unknown>) =>
  toYaml({
    form_schema: { type: "object", properties: {} },
    ui_schema: { layout: { kind: "page", sections: [] }, fields: {} },
    initial_values: {},
    blueprint,
  });

type Raw = Record<string, unknown>;
const blueprint = (text: string) => (parse(text) as { blueprint: Raw }).blueprint;
const components = (text: string) => blueprint(text).components as Raw[];
const component = (text: string, key: string) => components(text).find((c) => c.key === key)!;
const graphOf = (text: string) => vmGraph(blueprint(text), "/definition/blueprint", { designed: true });
const edgeIds = (text: string) => graphOf(text).edges.map((e) => e.id);
const nodeIds = (text: string) => graphOf(text).nodes.map((n) => n.id);

// A new VM item's draft, as the server starts it.
const starter = definition({ kind: "vm", template: "", naming: "", guest: { os_family: "linux", domain: "" } });

/** Refused: the error says why, and nothing is written (the text given back is never made). */
function refused(edit: () => string, why: RegExp) {
  expect(edit).toThrow(EditError);
  expect(edit).toThrow(why);
}

/** Two VMs, web after db, from the starter. */
const twoVms = addComponent(addComponent(starter, { key: "db", first: "web" }), { key: "cache", count: 2 });

describe("adding", () => {
  it("turns the item's one VM into the first component when a second is added, its settings shared", () => {
    const text = addComponent(starter, { key: "db", title: "Database", first: "web" });
    expect(components(text)).toEqual([{ key: "web" }, { key: "db", title: "Database" }]);
    expect(blueprint(text)).toMatchObject({ template: "", guest: { os_family: "linux" } });
    expect(nodeIds(text)).toEqual(["component:web", "component:db", "network:auto"]);
  });

  it("adds a component after the others, with its count", () => {
    expect(components(twoVms)).toEqual([{ key: "web" }, { key: "db" }, { key: "cache", count: 2 }]);
  });

  it("refuses a key that isn't one, is a single VM's or is taken, and too many VMs", () => {
    refused(() => addComponent(starter, { key: "Web", first: "app" }), /up to 16 lower-case/);
    refused(() => addComponent(starter, { key: "vm", first: "app" }), /single VM's key/);
    refused(() => addComponent(starter, { key: "web", first: "web" }), /has the key web already/);
    refused(() => addComponent(twoVms, { key: "db" }), /has the key db already/);
    refused(() => addComponent(twoVms, { key: "big", count: 17 }), /at most 20 VMs; these would be 21/);
    refused(() => addComponent(twoVms, { key: "big", count: 21 }), /A count from 0 to 20/);
  });

  it("gives a VM's first adapter a network, then a new adapter for the next", () => {
    const one = addNetwork(starter, "inputs.network", "component:vm");
    expect(blueprint(one).bindings).toEqual({ network: "inputs.network" });
    const two = addNetwork(one, '"dvpg-backup-vl900"', "component:vm");
    expect(blueprint(two).nics).toEqual([{ key: "nic2", network: '"dvpg-backup-vl900"' }]);
    expect(edgeIds(two)).toEqual(["nic:component:vm:nic1", "nic:component:vm:nic2"]);
    refused(() => addNetwork(two, "inputs.network", "component:vm"), /has an adapter on inputs.network already/);
  });

  it("gives a component its own adapters, copying those it took from the blueprint", () => {
    const shared = addNetwork(twoVms, "inputs.network", "component:web");
    expect(component(shared, "web").bindings).toEqual({ network: "inputs.network" });
    const bp = addNetwork(
      definition({
        kind: "vm",
        bindings: { network: "inputs.network" },
        nics: [{ network: "inputs.backup" }],
        components: [{ key: "a" }, { key: "b" }],
      }),
      '"dvpg-mgmt"',
      "component:b",
    );
    expect(component(bp, "a").nics).toBeUndefined();
    expect(component(bp, "b").nics).toEqual([{ network: "inputs.backup" }, { key: "nic3", network: '"dvpg-mgmt"' }]);
  });

  it("adds a segment and a security group, connected to a VM at once", () => {
    const seg = addSegment(twoVms, { key: "app", title: "App tier", to: "component:web" });
    expect(blueprint(seg).segments).toEqual([{ key: "app", title: "App tier" }]);
    expect(component(seg, "web").bindings).toEqual({ network: "segments.app" });
    const sg = addSecurityGroup(seg, { key: "websg", to: "component:web" });
    expect(blueprint(sg).security_groups).toEqual([{ key: "websg" }]);
    expect(component(sg, "web").groups).toEqual(["websg"]);
    expect(sg).toContain("groups: [websg]");
    refused(() => addSegment(seg, { key: "app" }), /Another segment has the key app/);
    refused(() => addSecurityGroup(sg, { key: "websg" }), /Another security group has the key websg/);
  });

  it("puts a VM in an existing NSX group by its ID", () => {
    const text = addNsxGroup(starter, "corp-monitoring", "component:vm");
    expect(blueprint(text).groups).toEqual(["nsx:corp-monitoring"]);
    refused(() => addNsxGroup(starter, "has space", "component:vm"), /An NSX group ID/);
  });

  it("adds a disk to a component's VMs, by size or CEL", () => {
    const one = addDisk(twoVms, { key: "data", size: 100, to: "component:db" });
    expect(component(one, "db").disks).toEqual([{ key: "data", size_gb: 100 }]);
    const two = addDisk(one, { size: "inputs.log_gb", to: "component:db" });
    expect(component(two, "db").disks).toEqual([{ key: "data", size_gb: 100 }, { size_gb: "inputs.log_gb" }]);
    expect(nodeIds(two)).toContain("disk:db:1");
    refused(() => addDisk(one, { key: "data", size: 10, to: "component:db" }), /disk with the key data already/);
    refused(() => addDisk(one, { size: 0, to: "component:db" }), /from 1 to 65536 GB/);
  });

  it("adds a custom resource only to an item with components", () => {
    refused(() => addResource(starter, { key: "lb", type: "f5.pool" }), /Only an item with components/);
    const text = addResource(twoVms, { key: "lb", type: "f5.pool", title: "Pool" });
    expect(blueprint(text).resources).toEqual([{ key: "lb", type: "f5.pool", title: "Pool" }]);
    refused(() => addResource(text, { key: "web", type: "f5.pool" }), /has the key web already/);
  });

  it("finds a free key", () => {
    expect(freeKey(twoVms, "vm", "component")).toBe("vm1");
    expect(freeKey(addComponent(twoVms, { key: "vm1" }), "vm", "component")).toBe("vm2");
  });
});

describe("connecting", () => {
  it("builds the second component after the first, and refuses a loop", () => {
    const text = connect(twoVms, "component:db", "component:web");
    expect(component(text, "web").depends_on).toEqual(["db"]);
    expect(edgeIds(text)).toContain("after:db>web");
    expect(connection(twoVms, "component:db", "component:web").says).toBe("Builds web after db.");
    refused(() => connect(text, "component:db", "component:web"), /web is built after db already/);
    refused(() => connect(text, "component:web", "component:db"), /would wait forever/);
    refused(() => connect(text, "component:web", "component:web"), /isn't connected to itself/);
  });

  it("refuses a loop through what a component's tags read", () => {
    const text = definition({
      kind: "vm",
      components: [{ key: "a", bindings: { tags: "[components.b.addresses[0]]" } }, { key: "b" }],
    });
    refused(() => connect(text, "component:a", "component:b"), /a is built after b: building b after it too/);
  });

  it("connects a VM to a network, a segment and groups, either way round", () => {
    const seg = addSegment(addSecurityGroup(twoVms, { key: "websg" }), { key: "app" });
    const text = connect(
      connect(connect(seg, "segment:app", "component:web"), "component:web", "sg:websg"),
      "component:db",
      "network:inputs.network",
    );
    expect(component(text, "web")).toMatchObject({ bindings: { network: "segments.app" }, groups: ["websg"] });
    expect(component(text, "db").bindings).toEqual({ network: "inputs.network" });
    expect(connection(seg, "component:web", "sg:websg").says).toBe("Puts web's VMs in websg.");
  });

  it("moves a disk from one component to another, and copies the blueprint's", () => {
    const own = addDisk(twoVms, { key: "data", size: 100, to: "component:db" });
    const moved = connection(own, "disk:db:0", "component:web");
    expect(moved.says).toBe("Moves data from db to web.");
    expect(component(moved.text, "web").disks).toEqual([{ key: "data", size_gb: 100 }]);
    expect(component(moved.text, "db").disks).toBeUndefined();
    const shared = definition({
      kind: "vm",
      disks: [{ size_gb: 20 }],
      components: [{ key: "a" }, { key: "b", disks: [] }],
    });
    expect(nodeIds(shared)).toContain("disk@0");
    const copied = connection(shared, "component:b", "disk@0");
    expect(copied.says).toBe("Gives b a copy of Disk 1, as its own disks.");
    expect(component(copied.text, "b").disks).toEqual([{ size_gb: 20 }]);
    refused(() => connect(shared, "disk@0", "component:a"), /a has Disk 1 already/);
  });

  it("refuses what the definition has no field for, with why, writing nothing", () => {
    const text = addResource(addSecurityGroup(addSecurityGroup(twoVms, { key: "one" }), { key: "two" }), {
      key: "lb",
      type: "f5.pool",
    });
    refused(() => connect(text, "component:web", "resource:lb"), /bind one of its inputs to a component/);
    refused(() => connect(text, "sg:one", "sg:two"), /needs its services/);
    refused(() => connect(text, "network:auto", "component:web"), /placement's network/);
    refused(
      () => connect(addDisk(text, { size: 5, to: "component:web" }), "disk:web:0", "sg:one"),
      /A disk is attached to a VM/,
    );
    refused(() => connect(text, "segment:none", "component:web"), /No segment has the key none/);
  });
});

describe("removing", () => {
  const built = connect(
    addDisk(
      addSecurityGroup(addSegment(twoVms, { key: "app", to: "component:web" }), { key: "websg", to: "component:web" }),
      { key: "data", size: 50, to: "component:db" },
    ),
    "component:db",
    "component:web",
  );

  it("removes a component, and its place in the others' depends_on", () => {
    const text = removeNode(built, "component:db");
    expect(components(text).map((c) => c.key)).toEqual(["web", "cache"]);
    expect(component(text, "web").depends_on).toBeUndefined();
    refused(() => removeNode(starter, "component:vm"), /at least one VM/);
  });

  it("removes a segment with the adapters on it, a group with its members", () => {
    const text = removeNode(removeNode(built, "segment:app"), "sg:websg");
    expect(blueprint(text).segments).toBeUndefined();
    expect(blueprint(text).security_groups).toBeUndefined();
    expect(component(text, "web").bindings).toBeUndefined();
    expect(component(text, "web").groups).toBeUndefined();
  });

  it("removes what an edge stands for", () => {
    expect(component(removeEdge(built, "after:db>web"), "web").depends_on).toBeUndefined();
    expect(component(removeEdge(built, "member:component:web:sg:websg"), "web").groups).toBeUndefined();
    expect(component(removeEdge(built, "nic:component:web:nic1"), "web").bindings).toBeUndefined();
    expect(component(removeEdge(built, "attach:disk:db:0>component:db"), "db").disks).toBeUndefined();
  });

  it("refuses the edges it can't remove, with why", () => {
    refused(() => removeEdge(built, "nic:component:db:nic1"), /Every VM has a first adapter/);
    const shared = definition({
      kind: "vm",
      bindings: { network: "inputs.network" },
      components: [{ key: "a" }, { key: "b" }],
    });
    refused(() => removeEdge(shared, "nic:component:a:nic1"), /takes its first adapter's network from the blueprint/);
    const reads = definition({
      kind: "vm",
      components: [{ key: "a", bindings: { tags: "[components.b.addresses[0]]" } }, { key: "b" }],
    });
    refused(() => removeEdge(reads, "after:b>a"), /reads components.b in its tags/);
  });

  it("leaves a component without the blueprint's disks when it loses its last own one", () => {
    const shared = definition({
      kind: "vm",
      disks: [{ size_gb: 20 }],
      components: [{ key: "a", disks: [{ size_gb: 5 }] }, { key: "b" }],
    });
    expect(component(removeNode(shared, "disk:a:0"), "a").disks).toEqual([]);
    expect(component(removeEdge(shared, "attach:disk@0>component:b"), "b").disks).toEqual([]);
  });

  it("removes a firewall rule's source, and the rule with its last", () => {
    const text = definition({
      kind: "vm",
      components: [
        { key: "web", groups: ["web"] },
        { key: "db", groups: ["db"] },
      ],
      security_groups: [{ key: "web" }, { key: "db", rules: [{ from: ["web", "nsx:jump"], services: ["SSH"] }] }],
    });
    const one = removeEdge(text, "rule:db:0:1");
    expect((blueprint(one).security_groups as Raw[])[1]!.rules).toEqual([{ from: ["web"], services: ["SSH"] }]);
    expect((blueprint(removeEdge(one, "rule:db:0:0")).security_groups as Raw[])[1]!.rules).toBeUndefined();
    const gone = removeNode(text, "sg:web");
    expect((blueprint(gone).security_groups as Raw[])[0]!.rules).toEqual([{ from: ["nsx:jump"], services: ["SSH"] }]);
  });
});

describe("properties", () => {
  it("renames a component, and what reads it", () => {
    const text = addResource(
      definition({
        kind: "vm",
        components: [
          { key: "db" },
          { key: "web", bindings: { tags: "[components.db.addresses[0]]" }, depends_on: ["db"] },
        ],
      }),
      { key: "lb", type: "f5.pool" },
    );
    const withInput = setField(text, "resource:lb", "with.members", "components.db.addresses");
    const renamed = rename(withInput, "component:db", "pg");
    expect(components(renamed).map((c) => c.key)).toEqual(["pg", "web"]);
    expect(component(renamed, "web")).toMatchObject({
      depends_on: ["pg"],
      bindings: { tags: "[components.pg.addresses[0]]" },
    });
    expect((blueprint(renamed).resources as Raw[])[0]!.with).toEqual({ members: "components.pg.addresses" });
    refused(() => rename(text, "component:db", "web"), /has the key web already/);
  });

  it("renames a segment and a group with the adapters and memberships that name them", () => {
    const text = addSecurityGroup(addSegment(twoVms, { key: "app", to: "component:web" }), {
      key: "websg",
      to: "component:web",
    });
    const renamed = rename(rename(text, "segment:app", "front"), "sg:websg", "front");
    expect(component(renamed, "web")).toMatchObject({ bindings: { network: "segments.front" }, groups: ["front"] });
  });

  it("sets and clears a component's fields, the blueprint's for a single VM", () => {
    const text = setField(setField(twoVms, "component:web", "count", 3), "component:web", "anti_affinity", true);
    expect(component(text, "web")).toEqual({ key: "web", count: 3, anti_affinity: true });
    expect(component(setField(text, "component:web", "anti_affinity", false), "web")).toEqual({ key: "web", count: 3 });
    const single = setField(starter, "component:vm", "guest.os_family", "windows");
    expect(blueprint(single).guest).toEqual({ os_family: "windows", domain: "" });
    refused(() => setField(text, "component:web", "count", 21), /A count from 0 to 20/);
  });

  it("points every adapter on a network at another", () => {
    const text = addNetwork(addNetwork(twoVms, "inputs.network", "component:web"), "inputs.network", "component:db");
    const next = setNetwork(text, "network:inputs.network", '"dvpg-web"');
    expect(component(next, "web").bindings).toEqual({ network: '"dvpg-web"' });
    expect(component(next, "db").bindings).toEqual({ network: '"dvpg-web"' });
  });
});

describe("the YAML", () => {
  const commented = `# A web tier, then its database.
form_schema: {type: object}
ui_schema: {layout: {kind: page, sections: []}, fields: {}}
initial_values: {}
blueprint:
  kind: vm
  template: tpl-rhel9 # patched monthly
  components:
    # Behind the load balancer.
    - key: web
      count: 2
    - key: db
`;

  it("keeps the comments and the order of what an edit doesn't touch", () => {
    const text = connect(commented, "component:db", "component:web");
    expect(text).toContain("# A web tier, then its database.");
    expect(text).toContain("template: tpl-rhel9 # patched monthly");
    expect(text).toContain("    # Behind the load balancer.\n    - key: web\n      count: 2\n      depends_on: [db]\n");
  });

  it("round-trips: what an edit writes and its removal give the text back", () => {
    for (const edit of [
      (t: string) => removeEdge(connect(t, "component:db", "component:web"), "after:db>web"),
      (t: string) => removeNode(addSegment(t, { key: "app" }), "segment:app"),
      (t: string) => removeNode(addResource(t, { key: "lb", type: "f5.pool" }), "resource:lb"),
      (t: string) => removeNode(addComponent(t, { key: "cache" }), "component:cache"),
    ])
      expect(edit(commented)).toBe(commented);
  });

  it("keeps a seeded item's definition as it was but for the edit", () => {
    const seeded = definition(seededBlueprint("three-tier")!);
    const text = removeNode(addSecurityGroup(seeded, { key: "extra" }), "sg:extra");
    expect(text).toBe(seeded);
  });

  it("says why it can't edit a text", () => {
    expect(designable(starter)).toBeNull();
    expect(designable(twoVms)).toBeNull();
    expect(designable("blueprint: [")).toMatch(/doesn't parse/);
    expect(designable(definition({ kind: "composite", steps: [] }))).toMatch(/isn't a VM item's/);
    expect(designable(definition({ kind: "vm", components: [{ key: "a" }, { key: "a" }] }))).toBe(
      "Two entries of components have the key a",
    );
    expect(designable(definition({ kind: "vm", components: { a: 1 } }))).toBe("components isn't a list");
    expect(designable(definition({ kind: "vm", components: [{ key: "a", depends_on: [{ b: 1 }] }] }))).toBe(
      "An entry of components.a.depends_on isn't a name",
    );
  });
});
