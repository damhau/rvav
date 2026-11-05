import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { blueprintGraph } from "@/components/canvas";
import type { PreviewComponent } from "@/lib/api/types";
import { seededBlueprint, topologyOf } from "@/mocks/blueprints";
import { renderApp } from "@/test/render";
import { withPreview } from "./RequestTopology";

const preview = (key: string, count: number, names: string[]) => ({ key, count, names }) as PreviewComponent;

const nodeNames = (region: HTMLElement) =>
  within(region)
    .getAllByRole("button")
    .filter((b) => b.classList.contains("react-flow__node"))
    .map((b) => b.getAttribute("aria-label") ?? "");

describe("what a request form gets of a blueprint", () => {
  it.each(["rhel9-server", "web-app", "three-tier", "postgres-cluster"])(
    "%s: its topology draws as its blueprint does",
    (slug) => {
      const full = blueprintGraph(seededBlueprint(slug)!)!;
      const topology = blueprintGraph(topologyOf(seededBlueprint(slug))!)!;
      expect(topology.problems).toEqual([]);
      expect(topology.nodes.map((n) => [n.id, n.badge, n.lines])).toEqual(
        full.nodes.map((n) => [n.id, n.badge, n.lines]),
      );
      expect(topology.edges).toEqual(full.edges);
    },
  );

  it("leaves out what only administrators see", () => {
    const json = JSON.stringify(topologyOf(seededBlueprint("three-tier")));
    for (const hidden of ["naming", "deployment_name", "cluster", "vcpu", "memory_gb"])
      expect(json).not.toContain(hidden);
    expect(topologyOf({ kind: "awx", job_template: "x" })).toBeUndefined();
  });
});

describe("withPreview", () => {
  it("puts the preview's counts and names on a VM item's components", () => {
    const graph = blueprintGraph(seededBlueprint("three-tier"))!;
    const web = graph.nodes.find((n) => n.id === "component:web")!;
    expect(web.badge).toBe("VMs");
    expect(web.lines).toContainEqual(expect.objectContaining({ label: "Count" }));

    const drawn = withPreview(graph, [preview("web", 3, ["a01", "a02", "a03"]), preview("db", 0, [])]);
    const after = drawn.nodes.find((n) => n.id === "component:web")!;
    expect(after.badge).toBe("3 VMs");
    expect(after.lines).not.toContainEqual(expect.objectContaining({ label: "Count" }));
    expect(after.lines).toContainEqual({ label: "Names", text: "a01, a02, a03", mono: true });
    expect(drawn.nodes.find((n) => n.id === "component:db")!.badge).toBe("No VMs");
    // The preview says nothing of it: as drawn.
    expect(drawn.nodes.find((n) => n.id === "component:app")).toBe(graph.nodes.find((n) => n.id === "component:app"));
    expect(withPreview(graph, undefined)).toBe(graph);
  });

  it("says what a composite's step builds", () => {
    const graph = blueprintGraph(seededBlueprint("postgres-cluster"))!;
    const drawn = withPreview(graph, [preview("nodes/node", 3, ["pg-a", "pg-b", "pg-c"])]);
    expect(drawn.nodes.find((n) => n.id === "step:nodes")!.lines).toEqual(
      expect.arrayContaining([
        { label: "Builds", text: "3 VMs" },
        { label: "Names", text: "pg-a, pg-b, pg-c", mono: true },
      ]),
    );
    expect(drawn.nodes.find((n) => n.id === "step:owner")!.lines).not.toContainEqual(
      expect.objectContaining({ label: "Builds" }),
    );
  });
});

describe("the request form", () => {
  it("draws a multi-VM item's topology, its counts following the form", async () => {
    const user = userEvent.setup();
    renderApp("/catalog/three-tier/request");
    const region = await screen.findByRole("region", { name: "Topology of Three-tier application" }, { timeout: 5000 });
    expect(screen.getByRole("heading", { name: "Topology", level: 2 })).toBeInTheDocument();
    await waitFor(() =>
      expect(nodeNames(region)).toContainEqual(
        expect.stringMatching(/^2 VMs Web\. web\..*Names: web-dev-ttweb001, web-dev-ttweb002\./),
      ),
    );
    expect(nodeNames(region)).toContainEqual(expect.stringMatching(/^Group Database\..*Allows PostgreSQL from/));

    const nodes = screen.getByRole("spinbutton", { name: "Web servers" });
    await user.clear(nodes);
    await user.type(nodes, "3");
    await waitFor(() => expect(nodeNames(region)).toContainEqual(expect.stringMatching(/^3 VMs Web\. web\./)), {
      timeout: 3000,
    });
  });

  it("draws a composite's steps, and nothing for a single VM", async () => {
    const { unmount } = renderApp("/catalog/postgres-cluster/request");
    const region = await screen.findByRole("region", { name: "Steps of PostgreSQL 16 cluster" }, { timeout: 5000 });
    await waitFor(() =>
      expect(nodeNames(region)).toContainEqual(expect.stringMatching(/^Item Database nodes\..*Builds: 3 VMs\./)),
    );
    unmount();

    renderApp("/catalog/rhel9-server/request");
    await screen.findByRole("heading", { name: "Request RHEL 9 server" }, { timeout: 5000 });
    await screen.findByText("No approval needed", {}, { timeout: 5000 });
    expect(screen.queryByRole("application")).not.toBeInTheDocument();
  });
});
