import { render, screen, within } from "@testing-library/react";
import { stringify } from "yaml";
import { describe, expect, it } from "vitest";
import { blueprintGraph } from "@/components/canvas";
import { seededBlueprint } from "@/mocks/blueprints";
import { celCheck } from "@/mocks/cel";
import DefinitionGraph, { drawDefinition } from "./DefinitionGraph";

const definition = (blueprint: Record<string, unknown>) =>
  stringify(
    { form_schema: { type: "object" }, ui_schema: { layout: { sections: [] } }, initial_values: {}, blueprint },
    { indent: 2, lineWidth: 0 },
  );

const threeTier = definition(seededBlueprint("three-tier")!);

const nodeNames = () =>
  within(screen.getByRole("application"))
    .getAllByRole("button")
    .filter((b) => b.classList.contains("react-flow__node"))
    .map((b) => b.getAttribute("aria-label") ?? "");

describe("drawDefinition", () => {
  it("draws the blueprint of a definition that parses", () => {
    const { graph, reason } = drawDefinition(threeTier);
    expect(reason).toBeUndefined();
    expect(graph?.kind).toBe("vm");
    expect(graph?.nodes.map((n) => n.id)).toEqual([
      "component:web",
      "component:app",
      "component:db",
      "segment:app",
      "sg:web",
      "sg:app",
      "sg:db",
      "nsx:grp-backup-clients",
      "any",
    ]);
  });

  it("says why there is no graph: YAML that doesn't parse, no blueprint, a kind it doesn't draw", () => {
    expect(drawDefinition("blueprint:\n  kind: vm\n  bad: [\n")).toEqual({
      graph: null,
      reason: expect.stringMatching(/^The YAML doesn't parse at line \d+$/) as unknown,
    });
    expect(drawDefinition("form_schema: {}\n").reason).toBe("The definition has no blueprint");
    expect(drawDefinition(definition({ kind: "awx", job_template: "x" })).reason).toBe(
      'A blueprint of kind "awx" isn\'t drawn',
    );
  });
});

describe("DefinitionGraph", () => {
  it("follows the text as it is edited, and keeps the last graph while it doesn't parse", () => {
    const { rerender } = render(<DefinitionGraph text={threeTier} name="Three-tier application" composite={false} />);
    expect(screen.getByRole("heading", { name: "Topology" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Topology of Three-tier application" })).toBeInTheDocument();
    expect(nodeNames()).toContainEqual(expect.stringMatching(/^VMs Web\. web\. Count: inputs\.web_nodes\./));

    // A change is drawn.
    rerender(
      <DefinitionGraph
        text={threeTier.replace("title: Web\n", "title: Front end\n")}
        name="Three-tier application"
        composite={false}
      />,
    );
    expect(nodeNames()).toContainEqual(expect.stringMatching(/^VMs Front end\. web\./));

    // Broken: the last graph stays, and the note says why.
    rerender(<DefinitionGraph text={`${threeTier}  - [\n`} name="Three-tier application" composite={false} />);
    expect(
      screen.getByText(/^The YAML doesn't parse at line \d+: the graph shows the definition as it last parsed\.$/),
    ).toBeInTheDocument();
    expect(nodeNames()).toContainEqual(expect.stringMatching(/^VMs Front end\. web\./));

    // Fixed: drawn again, the note gone.
    rerender(<DefinitionGraph text={threeTier} name="Three-tier application" composite={false} />);
    expect(screen.queryByText(/doesn't parse/)).not.toBeInTheDocument();
    expect(nodeNames()).toContainEqual(expect.stringMatching(/^VMs Web\. web\./));
  });

  it("lists the graph's problems on their lines", () => {
    const text = threeTier.replace("network: segments.app", "network: segments.data");
    render(<DefinitionGraph text={text} name="Three-tier application" composite={false} />);
    const line = text.split("\n").findIndex((l) => l.includes("segments.data")) + 1;
    expect(screen.getByRole("list", { name: "Problems in the topology" })).toHaveTextContent(
      `Line ${line}: No segment has the key data.`,
    );
  });

  it("draws a composite's steps, and says when there is nothing to draw yet", () => {
    const { rerender } = render(
      <DefinitionGraph text={definition(seededBlueprint("postgres-cluster")!)} name="PostgreSQL cluster" composite />,
    );
    expect(screen.getByRole("heading", { name: "Steps, in the order they run" })).toBeInTheDocument();
    expect(screen.getByText("At the same time")).toBeInTheDocument();
    expect(nodeNames()).toContainEqual(
      expect.stringMatching(
        /^AWX Backups\. backups\. Runs: pgbackrest-stanza\. When: inputs\.backup_retention_days > 0\./,
      ),
    );
    rerender(<DefinitionGraph text={"form_schema: {}\n"} name="PostgreSQL cluster" composite />);
    // It had a graph: kept.
    expect(screen.getByText(/^The definition has no blueprint: the graph shows/)).toBeInTheDocument();
  });

  it("has nothing to keep when the first text has no graph", () => {
    render(<DefinitionGraph text={"blueprint: [\n"} name="New item" composite />);
    expect(screen.getByText(/^The YAML doesn't parse at line \d+\.$/)).toBeInTheDocument();
    expect(screen.queryByRole("application")).not.toBeInTheDocument();
  });
});

describe("the mock's seeded blueprints", () => {
  it.each(["rhel9-server", "ubuntu-server", "windows-server", "web-app", "three-tier", "postgres-cluster"])(
    "%s draws without a problem",
    (slug) => {
      const graph = blueprintGraph(seededBlueprint(slug)!);
      expect(graph).not.toBeNull();
      expect(graph!.problems).toEqual([]);
    },
  );

  it("binds a composite's steps with CEL that parses", () => {
    const steps = seededBlueprint("postgres-cluster")!.steps as Record<string, unknown>[];
    const all = steps.flatMap((s) => (Array.isArray(s.parallel) ? (s.parallel as Record<string, unknown>[]) : [s]));
    const exprs = all.flatMap((s) => [s.when, ...Object.values((s.with as Record<string, unknown>) ?? {})]);
    for (const e of exprs) if (typeof e === "string") expect(celCheck(e), e).toBeNull();
  });
});
