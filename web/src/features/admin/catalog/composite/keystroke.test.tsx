// What a keystroke in a composite draft costs (web review finding 4): the
// text is parsed once for the graph, the designer, the step selected and
// their problems, and the graph is laid out again only when what it draws
// changes.
import { QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, within } from "@testing-library/react";
import { stringify } from "yaml";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { seededBlueprint } from "@/mocks/blueprints";
import { renderWithQuery } from "@/test/render";
import CompositeDesigner from "./CompositeDesigner";

const count = vi.hoisted(() => ({ parses: 0, layouts: 0 }));

vi.mock("yaml", async (importOriginal) => {
  const yaml = await importOriginal<typeof import("yaml")>();
  return {
    ...yaml,
    parse: (...a: Parameters<typeof yaml.parse>): unknown => {
      count.parses++;
      return yaml.parse(...a);
    },
    parseDocument: (...a: Parameters<typeof yaml.parseDocument>) => {
      count.parses++;
      return yaml.parseDocument(...a);
    },
  };
});

vi.mock("@/components/canvas/layout", async (importOriginal) => {
  const layout = await importOriginal<typeof import("@/components/canvas/layout")>();
  return {
    ...layout,
    layoutGraph: (...a: Parameters<typeof layout.layoutGraph>) => {
      count.layouts++;
      return layout.layoutGraph(...a);
    },
  };
});

const definition = (description: string, blueprint: Record<string, unknown>) =>
  stringify(
    {
      form_schema: { type: "object", description, properties: { role: { type: "string" } } },
      ui_schema: { layout: { kind: "page", sections: [] }, fields: {} },
      initial_values: {},
      blueprint,
    },
    { indent: 2, lineWidth: 0 },
  );
const pg = seededBlueprint("postgres-cluster")!;

const designer = (text: string) => (
  <CompositeDesigner
    name="PostgreSQL 16 cluster"
    design={{
      text,
      onEdit: () => {},
      history: { canUndo: false, canRedo: false },
      undo: () => {},
      redo: () => {},
    }}
  />
);

beforeEach(() => {
  count.parses = 0;
  count.layouts = 0;
});

describe("a keystroke in a composite draft", () => {
  it("parses the text once, and lays the graph out again only when the steps change", async () => {
    const { rerender: rerenderBare, client } = renderWithQuery(designer(definition("A", pg)));
    const rerender = (ui: React.ReactNode) =>
      rerenderBare(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
    const run = await screen.findByRole("application");
    fireEvent.click(
      within(run)
        .getAllByRole("button")
        .find((b) => /^Item Database nodes\./.test(b.getAttribute("aria-label") ?? ""))!,
    );
    expect(screen.getByRole("region", { name: "Selected: Database nodes" })).toBeInTheDocument();
    expect(count.layouts).toBe(1);

    // Typed in the form's part: parsed once, the graph not laid out again.
    count.parses = 0;
    rerender(designer(definition("AB", pg)));
    expect(screen.getByRole("region", { name: "Selected: Database nodes" })).toBeInTheDocument();
    expect(count.parses).toBe(1);
    expect(count.layouts).toBe(1);

    // Typed in a step: parsed once, laid out again.
    count.parses = 0;
    const renamed = structuredClone(pg);
    (renamed.steps as Record<string, unknown>[])[0]!.title = "Database servers";
    rerender(designer(definition("AB", renamed)));
    expect(await screen.findByRole("region", { name: "Selected: Database servers" })).toBeInTheDocument();
    expect(count.parses).toBe(1);
    expect(count.layouts).toBe(2);
  });
});
