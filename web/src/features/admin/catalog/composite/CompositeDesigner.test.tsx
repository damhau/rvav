import { EditorView } from "@codemirror/view";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useRef, useState } from "react";
import { parse, stringify } from "yaml";
import { describe, expect, it } from "vitest";
import type { CatalogEntitlements } from "@/lib/api/types";
import { seededBlueprint } from "@/mocks/blueprints";
import { workflowRegistrations } from "@/mocks/extensibility";
import { db } from "@/mocks/seed";
import { renderWithQuery } from "@/test/render";
import YamlEditor, { type EditorHistory, type YamlEditorHandle } from "../YamlEditor";
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

/** The designer beside the real editor, as the Definition tab has them: one text, one history (CodeMirror's). */
function EditorHarness({ initial }: { initial: string }) {
  const [text, setText] = useState(initial);
  const editor = useRef<YamlEditorHandle>(null);
  const [history, setHistory] = useState<EditorHistory>({ canUndo: false, canRedo: false });
  return (
    <>
      <YamlEditor ref={editor} value={text} onChange={setText} onHistory={setHistory} label="Definition" />
      <CompositeDesigner
        name="PostgreSQL 16 cluster"
        design={{
          text,
          onEdit: (next) => editor.current?.apply(next),
          history,
          undo: () => editor.current?.undo(),
          redo: () => editor.current?.redo(),
        }}
      />
      <pre data-testid="yaml">{text}</pre>
    </>
  );
}

/** Types into the editor, as a keystroke does: an edit of its own in its history. */
function typeIn(edit: (doc: string) => { from: number; to?: number; insert: string }) {
  const view = EditorView.findFromDOM(document.querySelector<HTMLElement>(".cm-editor")!)!;
  act(() => view.dispatch({ changes: edit(view.state.doc.toString()), userEvent: "input.type" }));
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

// These type field by field into a large page: the two longest take 2 s alone and
// up to 12.5 s under load (40 runs of this file at once), so the default 5 s fails
// on a busy CI runner, with nothing wrong.
const longTests = { timeout: 30_000 };

describe("the composite designer", longTests, () => {
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

  it("adds a wait for a time and a poll, field by field, with no inputs or undo", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness initial={pg} />);
    const add = async (runs: string | RegExp, key: string) => {
      await user.click(await screen.findByRole("button", { name: "Add a step" }));
      const dialog = await screen.findByRole("dialog", { name: "Add a step" });
      await waitFor(() => expect(within(dialog).getByRole("combobox", { name: "What it runs" })).toBeEnabled());
      await pick(user, within(dialog).getByRole("combobox", { name: "What it runs" }), runs);
      await user.clear(within(dialog).getByRole("textbox", { name: "Key" }));
      await user.type(within(dialog).getByRole("textbox", { name: "Key" }), key);
      await user.click(within(dialog).getByRole("button", { name: "Add step" }));
      return screen.findByRole("region", { name: `Selected: ${key}` });
    };

    const wait = await add("A wait for a time", "keep");
    expect(step("keep")).toEqual({ key: "keep", wait: {} });
    expect(within(wait).getByRole("list", { name: "Problems with this step" })).toHaveTextContent(
      "Say how long it waits, or until when.",
    );
    expect(within(wait).queryByRole("group", { name: "Inputs" })).toBeNull();
    expect(within(wait).queryByRole("group", { name: "Undo" })).toBeNull();
    const how = within(within(wait).getByRole("group", { name: "How long it waits" }));
    await user.type(how.getByRole("textbox", { name: "For" }), "soon{Enter}");
    expect(within(wait).getByRole("list", { name: "Problems with this step" })).toHaveTextContent(
      "A duration such as 72h.",
    );
    await user.clear(how.getByRole("textbox", { name: "For" }));
    await user.type(how.getByRole("textbox", { name: "For" }), "72h{Enter}");
    expect(step("keep").wait).toEqual({ for: "72h" });
    expect(within(wait).queryByRole("list", { name: "Problems with this step" })).toBeNull();
    await user.type(how.getByRole("textbox", { name: "Or until" }), "inputs.role{Enter}");
    expect(step("keep").wait).toEqual({ until: "inputs.role" });
    expect(how.getByRole("textbox", { name: "For" })).toHaveValue("");

    const poll = await add(/^A wait for a condition/, "change");
    expect(step("change").poll).toEqual({ rest: { method: "GET", url: "" }, until: "", every: "10m", timeout: "72h" });
    expect(within(poll).queryByRole("group", { name: "Inputs" })).toBeNull();
    expect(within(poll).queryByRole("group", { name: "Undo" })).toBeNull();
    const call = within(within(poll).getByRole("group", { name: "The call it repeats" }));
    await user.click(call.getByRole("combobox", { name: "Method" }));
    // It may not repeat what may do something each time.
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual(["GET", "PUT", "DELETE"]);
    await user.keyboard("{Escape}");
    await user.type(call.getByRole("textbox", { name: "URL" }), "https://h.example/change/${{inputs.role}{Enter}");
    await user.type(call.getByRole("textbox", { name: "Keep" }), "$.state{Enter}");
    const waits = within(within(poll).getByRole("group", { name: "What it waits for" }));
    await user.clear(waits.getByRole("textbox", { name: "Every" }));
    await user.type(waits.getByRole("textbox", { name: "Every" }), "30s{Enter}");
    expect(within(poll).getByRole("list", { name: "Problems with this step" })).toHaveTextContent("At least 1m");
    await user.clear(waits.getByRole("textbox", { name: "Every" }));
    await user.type(waits.getByRole("textbox", { name: "Every" }), "5m{Enter}");
    await user.type(waits.getByRole("textbox", { name: "Until" }), "steps.change.json == 'ok'{Enter}");
    const then = within(within(poll).getByRole("group", { name: "If it never holds" }));
    await user.clear(then.getByRole("textbox", { name: "Deadline" }));
    await user.type(then.getByRole("textbox", { name: "Deadline" }), "1m{Enter}");
    expect(within(poll).getByRole("list", { name: "Problems with this step" })).toHaveTextContent(
      "At least as long as how often it checks.",
    );
    await user.clear(then.getByRole("textbox", { name: "Deadline" }));
    await user.type(then.getByRole("textbox", { name: "Deadline" }), "24h{Enter}");
    await pick(user, then.getByRole("combobox", { name: "Then" }), "It goes on");
    expect(step("change").poll).toEqual({
      rest: { method: "GET", url: "https://h.example/change/${inputs.role}", keep: "$.state" },
      until: "steps.change.json == 'ok'",
      every: "5m",
      timeout: "24h",
      on_timeout: "continue",
    });
    expect(within(poll).queryByRole("list", { name: "Problems with this step" })).toBeNull();
  });

  it("adds a REST call and its undo, field by field, and later steps read what it gives", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness initial={pg} />);
    fireEvent.click(await waitFor(() => node(/^Item Database nodes/)));
    await user.click(screen.getByRole("button", { name: "Add a step" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a step" });
    await waitFor(() => expect(within(dialog).getByRole("combobox", { name: "What it runs" })).toBeEnabled());
    await pick(user, within(dialog).getByRole("combobox", { name: "What it runs" }), "A REST call");
    expect(within(dialog).getByRole("textbox", { name: "Key" })).toHaveValue("rest_call");
    await user.click(within(dialog).getByRole("button", { name: "Add step" }));
    expect(step("rest_call")).toEqual({ key: "rest_call", rest: { method: "GET", url: "" } });

    // It has no inputs: its fields, and what is missing said on the step.
    const details = await screen.findByRole("region", { name: "Selected: rest_call" });
    expect(within(details).queryByRole("group", { name: "Inputs" })).toBeNull();
    const problems = within(details).getByRole("list", { name: "Problems with this step" });
    expect(problems).toHaveTextContent("Give the URL to call.");
    const rest = within(within(details).getByRole("group", { name: "REST call" }));
    expect(rest.getByRole("textbox", { name: "Body" })).toBeDisabled();
    await pick(user, rest.getByRole("combobox", { name: "Method" }), "POST");
    await user.type(rest.getByRole("textbox", { name: "URL" }), "https://h.example/ci/${{steps.nodes.name}{Enter}");
    await user.type(rest.getByRole("textbox", { name: "Body" }), "{{'n': inputs.role}{Enter}");
    await pick(user, rest.getByRole("combobox", { name: "Secret" }), /^webhook-signing/);
    await user.type(rest.getByRole("textbox", { name: "Keep" }), "$.result{Enter}");
    // What isn't a status isn't written.
    await user.type(rest.getByRole("textbox", { name: "Accepted statuses" }), "two{Enter}");
    expect(step("rest_call").rest).not.toHaveProperty("accept");
    await user.clear(rest.getByRole("textbox", { name: "Accepted statuses" }));
    await user.type(rest.getByRole("textbox", { name: "Accepted statuses" }), "200, 201{Enter}");
    expect(step("rest_call").rest).toEqual({
      method: "POST",
      url: "https://h.example/ci/${steps.nodes.name}",
      body: "{'n': inputs.role}",
      secret: "webhook-signing",
      accept: [200, 201],
      keep: "$.result",
    });
    expect(yaml()).toContain("accept: [200, 201]");
    // It says what it can read there: earlier steps, not itself or later ones.
    expect(rest.getByText("steps.nodes.fqdn")).toBeInTheDocument();
    expect(rest.queryByText("steps.rest_call.status")).toBeNull();
    await waitFor(() => expect(within(details).queryByRole("list", { name: "Problems with this step" })).toBeNull());

    // Its undo is another call, which may read this one's answer.
    await pick(user, within(details).getByRole("combobox", { name: "Undone by" }), "A REST call");
    expect(step("rest_call").undo).toEqual({ rest: { method: "DELETE", url: "" } });
    await user.type(
      within(details).getByRole("textbox", { name: "Undo's URL" }),
      "https://h.example/ci/${{steps.rest_call.json.id}{Enter}",
    );
    expect(step("rest_call").undo).toEqual({
      rest: { method: "DELETE", url: "https://h.example/ci/${steps.rest_call.json.id}" },
    });
    expect(
      within(within(details).getByRole("group", { name: "Undo" })).getByText("steps.rest_call.json"),
    ).toBeInTheDocument();
    expect(within(details).queryByRole("textbox", { name: "Add a variable to the undo" })).toBeNull();

    // On the canvas, with the node's name saying what it calls and undoes.
    const drawn = await waitFor(() => node(/^REST rest_call\. Calls: POST https:\/\/h\.example/));
    expect(drawn.getAttribute("aria-label")).toContain("Undo: DELETE https://h.example/ci/");

    // A step after it reads what it gave.
    await user.click(screen.getByRole("button", { name: "Add a step" }));
    const next = await screen.findByRole("dialog", { name: "Add a step" });
    await pick(user, within(next).getByRole("combobox", { name: "What it runs" }), /^Monitoring agent/);
    await user.click(within(next).getByRole("radio", { name: "At the end" }));
    await user.click(within(next).getByRole("button", { name: "Add step" }));
    const later = await screen.findByRole("region", { name: "Selected: Monitoring agent" });
    await user.click(within(later).getByRole("combobox", { name: /^Agent/ }));
    expect(screen.getByRole("option", { name: "rest_call: The status it answered" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "rest_call: What it keeps of its JSON answer" })).toBeInTheDocument();
    await user.keyboard("{Escape}");

    // Another target replaces it, its undo kept.
    fireEvent.click(node(/^REST rest_call/));
    const again = await screen.findByRole("region", { name: "Selected: rest_call" });
    await pick(user, within(again).getByRole("combobox", { name: "Runs" }), "Another AWX template, by its name");
    const named = await within(again).findByRole("textbox", { name: "AWX template's name" });
    await user.type(named, "register-ci{Enter}");
    expect(step("rest_call").rest).toBeUndefined();
    expect(step("rest_call").awx).toEqual({ job_template: "register-ci" });
    expect(step("rest_call").undo).toBeDefined();
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

  it("keeps focus in the designer when an undo removes what had it, so redo works from the keyboard", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness initial={pg} />);
    await user.click(screen.getByRole("button", { name: "Add a step" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a step" });
    await waitFor(() => expect(within(dialog).getByRole("combobox", { name: "What it runs" })).toBeEnabled());
    await pick(user, within(dialog).getByRole("combobox", { name: "What it runs" }), /^Monitoring agent/);
    await user.click(within(dialog).getByRole("button", { name: "Add step" }));
    const details = await screen.findByRole("region", { name: "Selected: Monitoring agent" });
    await waitFor(() => expect(within(details).getByRole("heading", { name: /Monitoring agent/ })).toHaveFocus());

    // Undone: its details go, with the focus they had.
    await user.keyboard("{Control>}z{/Control}");
    expect(steps()).not.toContain("monitoring_agent");
    expect(screen.queryByRole("region", { name: "Selected: Monitoring agent" })).not.toBeInTheDocument();
    await waitFor(() => expect(document.activeElement).not.toBe(document.body));
    expect(within(screen.getByRole("application")).getAllByRole("button")).toContain(document.activeElement);
    await user.keyboard("{Control>}y{/Control}");
    expect(steps()).toContain("monitoring_agent");
    expect(await screen.findByRole("region", { name: "Selected: Monitoring agent" })).toBeInTheDocument();
  });

  it("focuses the row of a variable added to a step or its undo", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness initial={pg} />);
    fireEvent.click(node(/^AWX Backups\./));
    const details = screen.getByRole("region", { name: "Selected: Backups" });
    await user.type(within(details).getByRole("textbox", { name: "Add a variable" }), "stanza{Enter}");
    await waitFor(() => expect(within(details).getByRole("combobox", { name: /^stanza/ })).toHaveFocus());
    await user.type(within(details).getByRole("textbox", { name: "Add a variable to the undo" }), "force{Enter}");
    await waitFor(() => expect(within(details).getByRole("combobox", { name: /^force/ })).toHaveFocus());
  });
});

describe("the composite designer beside the editor", longTests, () => {
  it("undoes and redoes typed and designed edits in the order they were made", async () => {
    const user = userEvent.setup();
    renderWithQuery(<EditorHarness initial={pg} />);
    typeIn(() => ({ from: 0, insert: "# one\n" }));
    fireEvent.click(node(/^AWX Backups\./));
    await user.click(
      within(screen.getByRole("region", { name: "Selected: Backups" })).getByRole("button", { name: "Remove step" }),
    );
    expect(steps()).toEqual(["nodes", "owner"]);
    typeIn(() => ({ from: 0, insert: "# two\n" }));
    const after = yaml();
    expect(after.startsWith("# two\n# one\n")).toBe(true);

    const undo = screen.getByRole("button", { name: "Undo" });
    await user.click(undo);
    expect(yaml().startsWith("# one\n")).toBe(true);
    expect(steps()).toEqual(["nodes", "owner"]);
    await user.click(undo);
    expect(yaml().startsWith("# one\n")).toBe(true);
    expect(steps()).toEqual(["nodes", ["owner", "backups"]]);
    await user.click(undo);
    expect(yaml()).toBe(pg);
    expect(undo).toBeDisabled();

    const redo = screen.getByRole("button", { name: "Redo" });
    for (let i = 0; i < 3; i++) await user.click(redo);
    expect(yaml()).toBe(after);
    expect(redo).toBeDisabled();
  });

  it("keeps the last graph while the text doesn't parse, and edits nothing from it", async () => {
    const user = userEvent.setup();
    renderWithQuery(<EditorHarness initial={pg} />);
    typeIn((doc) => ({ from: doc.length, insert: "  steps: [\n" }));
    expect(
      screen.getByText(/^The YAML doesn't parse at line \d+: the graph shows the definition as it last parsed\.$/),
    ).toBeInTheDocument();
    const add = screen.getByRole("button", { name: "Add a step" });
    expect(add).toBeDisabled();
    await user.click(add);
    expect(screen.queryByRole("dialog", { name: "Add a step" })).not.toBeInTheDocument();
    fireEvent.click(node(/^AWX Backups\./));
    expect(screen.getByRole("note")).toHaveTextContent("The YAML doesn't parse: fix it in the editor");
    expect(screen.queryByRole("button", { name: "Remove step" })).not.toBeInTheDocument();
  });
});
