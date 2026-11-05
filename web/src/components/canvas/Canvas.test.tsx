import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { Canvas } from "./Canvas";
import { compositeGraph, vmGraph } from "./blueprint";

const graph = compositeGraph({
  kind: "composite",
  rollback_on_failure: true,
  steps: [
    { key: "vm", title: "The VM", item: "rhel9-server" },
    {
      parallel: [
        {
          key: "db_user",
          awx: { job_template: "pg-create-role", title: "Database user" },
          undo: { job_template: "pg-drop-role" },
        },
        { key: "monitoring", title: "Monitoring", workflow: "register-monitoring" },
      ],
    },
    { key: "notice", title: "Notice", awx: { job_template: "dba-notice" }, when: "inputs.notify" },
  ],
});

const node = (name: RegExp) => screen.getByRole("button", { name });

/**
 * What each node shows is the start of its name, word for word (WCAG 2.5.3,
 * axe's label-content-name-mismatch): its words apart ("1 VM Web", not
 * "1 VMWeb") and in the same order.
 */
function expectNamesHoldText() {
  const words = (s: string) => s.replace(/[^\p{L}\p{N}]+/gu, " ").trim();
  const cards = screen.getAllByRole("button").filter((b) => b.classList.contains("react-flow__node"));
  for (const b of cards)
    expect(words(b.getAttribute("aria-label") ?? "")).toMatch(new RegExp(`^${words(b.textContent ?? "")}`));
}

describe("Canvas", () => {
  it("draws every node as a button that says all it shows, and the graph is one Tab stop", () => {
    render(<Canvas graph={graph} label="Steps" />);
    const app = screen.getByRole("application", { name: "Steps" });
    const nodes = within(app)
      .getAllByRole("button")
      .filter((b) => b.classList.contains("react-flow__node"));
    const names = nodes.map((b) => b.getAttribute("aria-label"));
    expect(names).toEqual([
      "Request. The form's values, as inputs",
      "Item The VM. vm. Runs: rhel9-server. Runs first",
      "AWX Database user. db_user. Runs: pg-create-role. Undo: pg-drop-role. Runs after The VM. At the same time as Monitoring",
      "Workflow Monitoring. monitoring. Runs: register-monitoring. Runs after The VM. At the same time as Database user",
      "AWX Notice. notice. Runs: dba-notice. When: inputs.notify. Runs after Database user and Monitoring",
      "Done. On a failure: undo what was built. After Notice",
    ]);
    expectNamesHoldText();
    expect(nodes.filter((b) => b.getAttribute("tabindex") === "0").map((b) => b.dataset.id)).toEqual(["inputs"]);
    // Zooming has buttons of its own.
    expect(within(app).getByRole("button", { name: "Fit to view" })).toBeInTheDocument();
    // The parallel group is a frame, not a node to stop at.
    expect(screen.getByText("At the same time")).toBeInTheDocument();
  });

  it("moves between nodes with the arrow keys, Home and End, and selects with Enter", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Canvas graph={graph} label="Steps" onSelect={onSelect} />);
    await user.tab(); // the view switch
    await user.tab();
    await user.tab();
    expect(node(/^Request/)).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    expect(node(/^Item The VM/)).toHaveFocus();
    await user.keyboard("{ArrowDown}");
    const parallel = [node(/^AWX Database user/), node(/^Workflow Monitoring/)];
    expect(parallel).toContain(document.activeElement);
    await user.keyboard("{ArrowRight}");
    expect(node(/^Workflow Monitoring/)).toHaveFocus();
    await user.keyboard("{ArrowLeft}");
    expect(node(/^AWX Database user/)).toHaveFocus();
    // The node moved to is the Tab stop now.
    expect(node(/^AWX Database user/)).toHaveAttribute("tabindex", "0");
    expect(node(/^Request/)).not.toHaveAttribute("tabindex");
    await user.keyboard("{End}");
    expect(node(/^Done/)).toHaveFocus();
    await user.keyboard("{Home}");
    expect(node(/^Request/)).toHaveFocus();
    await user.keyboard("{End}{ArrowUp}{Enter}");
    expect(onSelect).toHaveBeenLastCalledWith("step:notice");
    expect(node(/^AWX Notice/)).toHaveAttribute("aria-pressed", "true");
    // What it shows, in full, under the canvas.
    const details = screen.getByRole("region", { name: "Selected: Notice" });
    expect(details).toHaveTextContent("When");
    expect(details).toHaveTextContent("inputs.notify");
    await user.keyboard("{Escape}");
    expect(onSelect).toHaveBeenLastCalledWith(null);
    expect(screen.queryByRole("region", { name: /^Selected/ })).not.toBeInTheDocument();
    await user.keyboard(" ");
    expect(onSelect).toHaveBeenLastCalledWith("step:notice");
  });

  it("selects a node clicked, and follows the selection it is given", async () => {
    const user = userEvent.setup();
    function Controlled() {
      const [selected, setSelected] = useState<string | null>("step:vm");
      return (
        <>
          <Canvas
            graph={graph}
            label="Steps"
            selected={selected}
            onSelect={setSelected}
            renderDetails={(n) => <p>Editing {n.id}</p>}
          />
          <button type="button" onClick={() => setSelected("end")}>
            Pick the end
          </button>
        </>
      );
    }
    render(<Controlled />);
    expect(screen.getByText("Editing step:vm")).toBeInTheDocument();
    // A click (d3's pointer handling needs a real browser's events: fireEvent's click is enough here).
    fireEvent.click(node(/^Workflow Monitoring/));
    expect(screen.getByText("Editing step:monitoring")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Pick the end" }));
    expect(node(/^Done/)).toHaveAttribute("aria-pressed", "true");
    expect(node(/^Workflow Monitoring/)).toHaveAttribute("aria-pressed", "false");
  });

  it("shows a run's status on each node", () => {
    render(
      <Canvas
        graph={graph}
        label="Run"
        status={{ "step:vm": "done", "step:db_user": "failed", "step:monitoring": "undone", "step:notice": "skipped" }}
      />,
    );
    // Where the card shows it: after the title.
    expect(node(/^Item The VM/).getAttribute("aria-label")).toMatch(/^Item The VM\. Done\. vm\./);
    expect(node(/^AWX Database user/).getAttribute("aria-label")).toMatch(/^AWX Database user\. Failed\./);
    expect(node(/^Workflow Monitoring/).getAttribute("aria-label")).toMatch(/^Workflow Monitoring\. Undone\./);
    expect(node(/^AWX Notice/).getAttribute("aria-label")).toMatch(/^AWX Notice\. Skipped\./);
    expectNamesHoldText();
    expect(screen.getByRole("img", { name: "Failed" })).toBeInTheDocument();
  });
});

describe("the list view", () => {
  it("lists a composite's steps in order, a parallel group within it", async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<Canvas graph={graph} label="Steps" onSelect={onSelect} status={{ "step:vm": "running" }} />);
    await user.click(screen.getByRole("button", { name: "List" }));
    expect(screen.getByRole("button", { name: "List" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.queryByRole("application")).not.toBeInTheDocument();

    const list = screen.getByRole("list", { name: "Steps" });
    expect(list.tagName).toBe("OL");
    const top = within(list)
      .getAllByRole("listitem")
      .filter((li) => li.parentElement === list);
    expect(top).toHaveLength(5);
    [
      /^Request The form's values/,
      /^Item The VM Runningvm/,
      /^At the same timeAWX Database user/,
      /^AWX Notice notice/,
      /^Done/,
    ].forEach((text, i) => expect(top[i]?.textContent).toMatch(text));
    const inGroup = within(top[2]!).getByRole("list", { name: "At the same time" });
    const group = within(inGroup)
      .getAllByRole("listitem")
      .filter((li) => li.parentElement === inGroup);
    expect(group).toHaveLength(2);
    expect(group[0]).toHaveTextContent("Undopg-drop-role");
    expect(group[0]).toHaveTextContent("At the same time as Monitoring");
    expect(within(top[1]!).getByRole("img", { name: "Running" })).toBeInTheDocument();

    await user.click(within(group[1]!).getByRole("button", { name: /Monitoring/ }));
    expect(onSelect).toHaveBeenLastCalledWith("step:monitoring");
    expect(within(group[1]!).getByRole("button", { name: /Monitoring/ })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByRole("region", { name: "Selected: Monitoring" })).toBeInTheDocument();
    // Back to the graph, with the same node selected.
    await user.click(screen.getByRole("button", { name: "Graph" }));
    expect(node(/^Workflow Monitoring/)).toHaveAttribute("aria-pressed", "true");
  });

  it("lists a VM item's nodes with how they relate", async () => {
    const user = userEvent.setup();
    const g = vmGraph({
      kind: "vm",
      segments: [{ key: "app", title: "Application" }],
      bindings: { network: "segments.app" },
      security_groups: [{ key: "web", title: "Web servers", rules: [{ from: ["any"], services: ["HTTPS"] }] }],
      components: [{ key: "web", title: "Web", count: 2, groups: ["web"] }],
    });
    render(<Canvas graph={g} label="Topology" defaultView="list" />);
    const list = screen.getByRole("list", { name: "Topology" });
    expect(list.tagName).toBe("UL");
    const items = within(list)
      .getAllByRole("listitem")
      .filter((li) => li.parentElement === list);
    expect(items.map((li) => within(li).getAllByRole("button")[0]!.textContent?.trim())).toEqual([
      "2 VMs Web",
      "Segment Application",
      "Group Web servers",
      "Any",
    ]);
    expect(items[0]).toHaveTextContent("Adapter nic1 on Application");
    expect(items[0]).toHaveTextContent("In Web servers");
    expect(items[2]).toHaveTextContent("Allows HTTPS from Any");
    await user.click(screen.getByRole("button", { name: "Graph" }));
    expect(screen.getByRole("application", { name: "Topology" })).toBeInTheDocument();
  });
});
