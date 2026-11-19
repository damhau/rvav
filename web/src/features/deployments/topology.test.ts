import { describe, expect, it } from "vitest";
import type { DeploymentDetail, Resource, VmNic, VmResource } from "@/lib/api/types";
import { layoutGraph } from "@/components/canvas/layout";
import { db } from "@/mocks/seed";
import { deploymentGraph, ONE_BY_ONE } from "./topology";

const base = db.deployments.find((d) => d.name === "web-prd-front")!;
const deployment = (resources: Resource[], name = "web-dev-app"): DeploymentDetail => ({ ...base, name, resources });

const nic = (n: number, network: string, dhcp?: boolean): VmNic => ({
  label: `Network adapter ${n}`,
  network,
  ip_addresses: dhcp ? [] : [`10.31.10.${n}`],
  ...(dhcp === undefined ? {} : { dhcp }),
});

function vm(name: string, over: Partial<VmResource["vm"]> = {}): VmResource {
  return {
    id: `res_${name}`,
    type: "vsphere.vm",
    name,
    sync_state: "in_sync",
    vm: {
      moref: `vm-${name}`,
      instance_uuid: `uuid-${name}`,
      vcenter: "vc01.corp.example",
      power_state: "on",
      guest_state: "running",
      cpu_count: 2,
      memory_mb: 4096,
      disks: [{ label: "Hard disk 1", capacity_gb: 80 }],
      nics: [nic(1, "dvpg-app-dev-vl310", false)],
      ...over,
    },
  };
}

const ids = (g: ReturnType<typeof deploymentGraph>) => g.nodes.map((n) => n.id);
const edge = (g: ReturnType<typeof deploymentGraph>, id: string) => g.edges.find((e) => e.id === id);

describe("a deployment's topology", () => {
  it("draws each VM of a small component on the networks its adapters are on", () => {
    const g = deploymentGraph(
      deployment([
        vm("web-dev-app001", { nics: [nic(1, "dvpg-app-dev-vl310", false), nic(2, "dvpg-db-dev-vl320", true)] }),
        vm("web-dev-app002", { power_state: "off", guest_state: "not_running" }),
      ]),
    );
    expect(g).toMatchObject({ kind: "vm", direction: "LR", groups: [] });
    expect(ids(g)).toEqual([
      "vm:res_web-dev-app001",
      "vm:res_web-dev-app002",
      "network:dvpg-app-dev-vl310",
      "network:dvpg-db-dev-vl320",
    ]);
    const first = g.nodes[0]!;
    expect(first).toMatchObject({ kind: "vm", title: "web-dev-app001" });
    expect(first.lines).toEqual([
      { label: "Power", text: "On" },
      { label: "Address", text: "10.31.10.1", mono: true },
      { label: "Size", text: "2 vCPU · 4 GB" },
      { label: "Disks", text: "80 GB" },
    ]);
    expect(g.nodes[1]!.lines[0]).toEqual({ label: "Power", text: "Off" });
    expect(edge(g, "nic:vm:res_web-dev-app001:2:dvpg-db-dev-vl320")).toMatchObject({
      source: "network:dvpg-db-dev-vl320",
      target: "vm:res_web-dev-app001",
      kind: "network",
      label: "Adapter 2 · DHCP",
      description: "web-dev-app001: adapter 2 on network dvpg-db-dev-vl320, by DHCP",
    });
    expect(first.relations).toContain("Adapter 1 on network dvpg-app-dev-vl310");
  });

  it("lays a component's VMs out in name order, its first on top", () => {
    const g = deploymentGraph(base);
    const { boxes } = layoutGraph(g);
    const vms = g.nodes.filter((n) => n.kind === "vm");
    expect(vms.map((n) => n.title)).toEqual(["web-prd-front001", "web-prd-front002", "web-prd-front003"]);
    const tops = vms.map((n) => boxes.get(n.id)!.y);
    expect(tops).toEqual([...tops].sort((a, b) => a - b));
  });

  it(`draws a component of more than ${ONE_BY_ONE} VMs as one node with their count`, () => {
    const four = Array.from({ length: ONE_BY_ONE }, (_, i) => vm(`web-dev-app00${i + 1}`));
    expect(deploymentGraph(deployment(four)).nodes.filter((n) => n.kind === "vm")).toHaveLength(ONE_BY_ONE);

    const five = [...four, vm("web-dev-app005", { cpu_count: 4, power_state: "off" })];
    five[4]!.sync_state = "drifted";
    const g = deploymentGraph(deployment(five));
    expect(ids(g)).toEqual(["component:vm", "network:dvpg-app-dev-vl310"]);
    expect(g.nodes[0]).toMatchObject({
      kind: "component",
      title: "web-dev-app",
      subtitle: "vm",
      badge: "5 VMs",
      lines: [
        { label: "Names", text: "web-dev-app001 to web-dev-app005", mono: true },
        { label: "Power", text: "4 of 5 on" },
        { label: "Size", text: "Not all the same" },
      ],
      problems: ["web-dev-app005: Changed outside rvav."],
    });
    expect(g.edges.filter((e) => e.kind === "network")).toHaveLength(1);
  });

  it("frames each component's VMs and draws the order they were built in", () => {
    const web = Array.from({ length: 2 }, (_, i) => vm(`shop-dev-web0${i + 1}`, { component: "web" }));
    const workers = Array.from({ length: 6 }, (_, i) => vm(`shop-dev-worker0${i + 1}`, { component: "worker" }));
    const g = deploymentGraph(deployment([vm("shop-dev-db01", { component: "db" }), ...web, ...workers]), {
      kind: "vm",
      components: [
        { key: "db", title: "Database" },
        { key: "web", title: "Web servers", depends_on: ["db"] },
        { key: "worker", title: "Workers", depends_on: ["db", "web"] },
      ],
    });
    expect(g.groups).toEqual([
      { id: "frame:db", label: "Database · 1 VM", nodes: ["vm:res_shop-dev-db01"], problems: [] },
      {
        id: "frame:web",
        label: "Web servers · 2 VMs",
        nodes: ["vm:res_shop-dev-web01", "vm:res_shop-dev-web02"],
        problems: [],
      },
    ]);
    expect(g.nodes.find((n) => n.id === "vm:res_shop-dev-web01")).toMatchObject({ group: "frame:web" });
    expect(g.nodes.find((n) => n.id === "component:worker")).toMatchObject({ title: "Workers", badge: "6 VMs" });
    expect(g.edges.filter((e) => e.kind === "sequence").map((e) => [e.source, e.target, e.description])).toEqual([
      ["frame:db", "frame:web", "Web servers built after Database"],
      ["frame:db", "component:worker", "Workers built after Database"],
      ["frame:web", "component:worker", "Workers built after Web servers"],
    ]);
    expect(g.nodes.find((n) => n.id === "vm:res_shop-dev-web02")!.relations).toEqual([
      "In Web servers",
      "Adapter 1 on network dvpg-app-dev-vl310",
      "Built after Database",
    ]);
    expect(g.nodes.find((n) => n.id === "component:worker")!.relations).toContain(
      "Built after Database and Web servers",
    );
  });

  it("names components by their keys, with no build order, when the request's topology is unknown", () => {
    const g = deploymentGraph(
      deployment([vm("shop-dev-db01", { component: "db" }), vm("shop-dev-web01", { component: "web" })]),
    );
    expect(g.groups.map((f) => f.label)).toEqual(["db · 1 VM", "web · 1 VM"]);
    expect(g.edges.filter((e) => e.kind === "sequence")).toEqual([]);
  });

  it("draws the NSX segment, the groups the VMs are in and the rules into them", () => {
    const search = db.deployments.find((d) => d.name === "web-tst-search")!;
    const g = deploymentGraph(search);
    const seg = g.nodes.find((n) => n.kind === "segment")!;
    expect(seg).toMatchObject({
      badge: "Segment",
      lines: [{ label: "Subnet", text: "10.41.10.0/26", mono: true }, {}],
    });
    const group = g.nodes.find((n) => n.kind === "security_group")!;
    expect(group).toMatchObject({ title: "Search", lines: [{ text: "Made for the deployment" }] });
    expect(g.nodes.filter((n) => n.kind === "nsx_group").map((n) => n.title)).toEqual([
      "grp-web-servers",
      "grp-backup-clients",
    ]);
    expect(g.nodes.at(-1)).toMatchObject({ id: "any", kind: "any" });

    const [one, two] = g.nodes.filter((n) => n.kind === "vm");
    expect(g.edges.filter((e) => e.kind === "member" && e.source === one!.id).map((e) => e.target)).toEqual([
      group.id,
      "nsx:grp-web-servers",
    ]);
    expect(one!.problems).toEqual([]);
    expect(two!.problems).toEqual(["Its NSX tags changed outside rvav."]);
    expect(g.edges.filter((e) => e.kind === "network" && e.source === seg.id)).toHaveLength(2);

    const rules = g.edges.filter((e) => e.kind === "rule");
    expect(rules.map((e) => [e.source, e.target, e.label, e.description])).toEqual([
      ["any", group.id, "HTTPS", "Allows HTTPS from Any to Search"],
      ["nsx:grp-backup-clients", group.id, "SSH", "Allows SSH from grp-backup-clients to Search"],
    ]);
    expect(group.relations).toContain("Allows SSH from grp-backup-clients");
  });

  it("draws a rule that drops, and one out of a group to anywhere", () => {
    const g = deploymentGraph(
      deployment([
        vm("web-dev-app001"),
        {
          id: "res_policy",
          type: "nsx.policy",
          name: "rvav-dep",
          sync_state: "in_sync",
          rules: [
            { id: "r1", from: ["ANY"], to: ["grp-db"], services: ["ANY"], action: "DROP" },
            { id: "r2", from: ["grp-db"], to: ["ANY"], services: ["DNS"], action: "ALLOW" },
          ],
        },
      ]),
    );
    expect(g.edges.filter((e) => e.kind === "rule")).toMatchObject([
      { source: "any", target: "nsx:grp-db", label: "any service · drop", deny: true },
      { source: "nsx:grp-db", target: "any", label: "DNS", description: "Allows DNS from grp-db to Any" },
    ]);
  });
});
