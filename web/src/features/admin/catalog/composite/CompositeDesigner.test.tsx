import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { parse, stringify } from "yaml";
import { describe, expect, it } from "vitest";
import type { CatalogEntitlements } from "@/lib/api/types";
import { seededBlueprint } from "@/mocks/blueprints";
import { workflowRegistrations } from "@/mocks/extensibility";
import { db } from "@/mocks/seed";
import { renderWithQuery } from "@/test/render";
import CompositeDesigner from "./CompositeDesigner";

const pg = stringify(
  {
    form_schema: { type: "object", properties: { role: { type: "string" }, size: { type: "string" } } },
    ui_schema: { layout: { kind: "page", sections: [] }, fields: { role: { label: "Database name" } } },
    initial_values: {},
    blueprint: seededBlueprint("postgres-cluster")!,
  },
  { indent: 2, lineWidth: 0 },
);

/** The editor's part: the text and a history of the designer's edits. */
function Harness({
  initial,
  refused,
  entitlements,
}: {
  initial: string;
  refused?: { field: string; message: string };
  entitlements?: CatalogEntitlements;
}) {
  const [history, setHistory] = useState([initial]);
  const [at, setAt] = useState(0);
  const text = history[at] ?? initial;
  return (
    <>
      <CompositeDesigner
        name="PostgreSQL 16 cluster"
        entitlements={entitlements}
        design={{
          text,
          onEdit: (next) => {
            setHistory([...history.slice(0, at + 1), next]);
            setAt(at + 1);
          },
          history: { canUndo: at > 0, canRedo: at < history.length - 1 },
          undo: () => setAt((a) => Math.max(0, a - 1)),
          redo: () => setAt((a) => Math.min(history.length - 1, a + 1)),
          serverProblems: refused && { text: initial, errors: [{ ...refused, code: "invalid" }] },
        }}
      />
      <pre data-testid="yaml">{text}</pre>
    </>
  );
}

const yaml = () => screen.getByTestId("yaml").textContent ?? "";
const steps = () => {
  const bp = (parse(yaml()) as { blueprint: { steps: Record<string, unknown>[] } }).blueprint;
  return bp.steps.map((s) => (Array.isArray(s.parallel) ? (s.parallel as { key: string }[]).map((p) => p.key) : s.key));
};
const step = (key: string) => {
  const bp = (parse(yaml()) as { blueprint: { steps: Record<string, unknown>[] } }).blueprint;
  return bp.steps
    .flatMap((s) => (Array.isArray(s.parallel) ? (s.parallel as Record<string, unknown>[]) : [s]))
    .find((s) => s.key === key)!;
};
// Nodes are clicked with fireEvent: user-event's mousedown reaches d3-zoom, which jsdom can't serve.
const node = (name: RegExp) =>
  within(screen.getByRole("application"))
    .getAllByRole("button")
    .find((b) => b.classList.contains("react-flow__node") && name.test(b.getAttribute("aria-label") ?? ""))!;

/** Picks an option of a select by its text. */
async function pick(user: ReturnType<typeof userEvent.setup>, select: HTMLElement, option: string | RegExp) {
  await user.click(select);
  await user.click(await screen.findByRole("option", { name: option }));
}

describe("the composite designer", () => {
  it("adds a step after the selected one, binds its inputs and sets its condition, all undone and redone", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness initial={pg} />);
    fireEvent.click(node(/^Item Database nodes\./));
    expect(screen.getByRole("region", { name: "Selected: Database nodes" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Add a step" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a step" });
    await waitFor(() => expect(within(dialog).getByRole("combobox", { name: "What it runs" })).toBeEnabled());
    await pick(user, within(dialog).getByRole("combobox", { name: "What it runs" }), /^Monitoring agent/);
    expect(within(dialog).getByRole("textbox", { name: "Key" })).toHaveValue("monitoring_agent");
    await user.clear(within(dialog).getByRole("textbox", { name: "Key" }));
    await user.type(within(dialog).getByRole("textbox", { name: "Key" }), "monitor");
    expect(within(dialog).getByRole("radio", { name: "After Database nodes" })).toBeChecked();
    await user.click(within(dialog).getByRole("button", { name: "Add step" }));

    expect(steps()).toEqual(["nodes", "monitor", ["owner", "backups"]]);
    expect(step("monitor")).toEqual({
      key: "monitor",
      title: "Monitoring agent",
      // By its ID in AWX too: it is launched by it.
      awx: { job_template: "monitoring-agent", template_id: 105 },
    });
    // The new step is selected, its details focused.
    const details = await screen.findByRole("region", { name: "Selected: Monitoring agent" });
    await waitFor(() => expect(within(details).getByRole("heading", { name: /Monitoring agent/ })).toHaveFocus());

    // Its inputs: what its template's survey asks, bound to what exists before it.
    const agent = within(details).getByRole("combobox", { name: /^Agent/ });
    await user.click(agent);
    const options = screen.getAllByRole("option").map((o) => o.textContent);
    expect(options).toContain("Database nodes: The first VM's FQDNsteps.nodes.fqdn");
    expect(options.some((o) => o?.includes("steps.owner"))).toBe(false);
    await user.click(screen.getByRole("option", { name: "Database nodes: The first VM's FQDN" }));
    expect(step("monitor").with).toEqual({ monitoring_agent: "steps.nodes.fqdn" });

    // A value written out: a number stays a number.
    await pick(user, within(details).getByRole("combobox", { name: /^Port/ }), "A value or CEL expression");
    const port = await within(details).findByRole("textbox", { name: "Port: value or CEL expression" });
    await waitFor(() => expect(port).toHaveFocus());
    await user.type(port, "9187{Enter}");
    expect(step("monitor").with).toEqual({ monitoring_agent: "steps.nodes.fqdn", listen_port: 9187 });

    await user.type(within(details).getByRole("textbox", { name: "When" }), "inputs.size == 'L'{Enter}");
    expect(step("monitor").when).toBe("inputs.size == 'L'");

    // Undo, from the button and from the keyboard, then redo.
    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(step("monitor").when).toBeUndefined();
    within(details)
      .getByRole("heading", { name: /Monitoring agent/ })
      .focus();
    await user.keyboard("{Control>}z{/Control}");
    expect(step("monitor").with).toEqual({ monitoring_agent: "steps.nodes.fqdn" });
    await user.keyboard("{Control>}{Shift>}z{/Shift}{/Control}");
    expect(step("monitor").with).toEqual({ monitoring_agent: "steps.nodes.fqdn", listen_port: 9187 });
    await user.click(screen.getByRole("button", { name: "Redo" }));
    expect(step("monitor").when).toBe("inputs.size == 'L'");
  });

  it("moves, joins and removes steps from the selected step's details", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness initial={pg} />);
    fireEvent.click(node(/^AWX Backups\./));
    const details = screen.getByRole("region", { name: "Selected: Backups" });
    await user.click(within(details).getByRole("button", { name: "Run after the others" }));
    expect(steps()).toEqual(["nodes", "owner", "backups"]);
    await user.click(await within(details).findByRole("button", { name: "Run with the step before" }));
    expect(steps()).toEqual(["nodes", ["owner", "backups"]]);
    await user.click(within(details).getByRole("button", { name: "Remove step" }));
    expect(steps()).toEqual(["nodes", "owner"]);
    // The step before it is selected.
    expect(await screen.findByRole("region", { name: "Selected: The database's owner" })).toBeInTheDocument();
  });

  it("says on a step what the server would refuse, and what it refused", async () => {
    const user = userEvent.setup();
    const vmWhen = pg.replace("      item: pg-node\n", "      item: pg-node\n      when: inputs.size == 'L'\n");
    renderWithQuery(
      <Harness
        initial={vmWhen}
        refused={{ field: "/definition/blueprint/steps/1/parallel/0/with/database", message: "Refused by the server." }}
      />,
    );
    await waitFor(() =>
      expect(node(/^Item Database nodes\./).getAttribute("aria-label")).toContain(
        "a VM step has no condition: its VMs are planned when the request is submitted.",
      ),
    );
    expect(node(/^Item The database's owner\./).getAttribute("aria-label")).toContain("Refused by the server.");
    expect(screen.getByRole("list", { name: "Problems in the steps" })).toHaveTextContent("Refused by the server.");
    // A VM step's condition is said, and can only be cleared.
    fireEvent.click(node(/^Item Database nodes\./));
    const details = screen.getByRole("region", { name: "Selected: Database nodes" });
    expect(within(details).getByRole("list", { name: "Problems with this step" })).toHaveTextContent(
      "a VM step has no condition",
    );
    expect(within(details).queryByRole("group", { name: "Undo" })).not.toBeInTheDocument();
    await user.clear(within(details).getByRole("textbox", { name: "When" }));
    await user.tab();
    expect(step("nodes").when).toBeUndefined();
    // Once the text changed, the server's refusal of the earlier text isn't shown.
    await waitFor(() =>
      expect(node(/^Item The database's owner\./).getAttribute("aria-label")).not.toContain("Refused by the server."),
    );
  });

  it("says a step's custom workflow isn't offered to a project the item is offered to", async () => {
    workflowRegistrations(db).find((w) => w.key === "register-monitoring")!.project_ids = ["prj_data"];
    const projects = db.projects.map((p) => ({ id: p.id, code: p.code, name: p.name }));
    const other = projects.find((p) => p.id !== "prj_data")!;
    const text = pg.replace("  steps:\n", "  steps:\n    - key: mon\n      workflow: register-monitoring\n");
    const { unmount } = renderWithQuery(
      <Harness initial={text} entitlements={{ all_projects: false, project_ids: ["prj_data", other.id], projects }} />,
    );
    const message = `mon: the custom workflow register-monitoring isn't offered to ${other.code}, which may request this item.`;
    await waitFor(() => expect(screen.getByRole("list", { name: "Problems in the steps" })).toHaveTextContent(message));
    unmount();
    // Offered to the data project only: nothing to say.
    renderWithQuery(
      <Harness initial={text} entitlements={{ all_projects: false, project_ids: ["prj_data"], projects }} />,
    );
    await waitFor(() => expect(node(/^Workflow mon\./)).toBeDefined());
    expect(screen.queryByText(message)).not.toBeInTheDocument();
  });

  it("toggles undoing what was built from the end node, and leaves a text that doesn't parse to the editor", async () => {
    const user = userEvent.setup();
    const { unmount } = renderWithQuery(<Harness initial={pg} />);
    fireEvent.click(node(/^Done\./));
    await user.click(screen.getByRole("checkbox", { name: "Undo what was built when a step fails" }));
    expect(yaml()).toContain("rollback_on_failure: false");
    unmount();

    renderWithQuery(<Harness initial={pg + "  steps: [\n"} />);
    expect(screen.getByText(/^The YAML doesn't parse at line \d+/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Add a step" })).not.toBeInTheDocument();
  });
});
