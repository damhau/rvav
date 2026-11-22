import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { EditorView } from "@codemirror/view";
import { useRef, useState } from "react";
import { parse, stringify } from "yaml";
import { describe, expect, it } from "vitest";
import { renderWithQuery } from "@/test/render";
import YamlEditor, { type EditorHistory, type YamlEditorHandle } from "../YamlEditor";
import VmDesigner, { partType } from "./VmDesigner";

const definition = (blueprint: Record<string, unknown>) =>
  stringify(
    {
      form_schema: { type: "object", properties: {} },
      ui_schema: { layout: { kind: "page", sections: [] }, fields: {} },
      initial_values: {},
      blueprint,
    },
    { indent: 2, lineWidth: 0 },
  );

// A new VM item's draft, as the server starts it.
const starter = definition({ kind: "vm", template: "tpl-rhel9", guest: { os_family: "linux" } });
const twoVms = definition({
  kind: "vm",
  template: "tpl-rhel9",
  components: [
    { key: "web", title: "Web servers", count: 2 },
    { key: "db", title: "Database" },
  ],
  security_groups: [{ key: "websg" }],
});

/** The editor's part: the text and a history of the designer's edits. */
function Harness({ initial, refused }: { initial: string; refused?: { field: string; message: string } }) {
  const [history, setHistory] = useState([initial]);
  const [at, setAt] = useState(0);
  const text = history[at] ?? initial;
  return (
    <>
      <VmDesigner
        name="Web shop"
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

/** The designer beside the real editor, as the Definition tab has them. */
function EditorHarness({ initial }: { initial: string }) {
  const [text, setText] = useState(initial);
  const editor = useRef<YamlEditorHandle>(null);
  const [history, setHistory] = useState<EditorHistory>({ canUndo: false, canRedo: false });
  return (
    <>
      <YamlEditor ref={editor} value={text} onChange={setText} onHistory={setHistory} label="Definition" />
      <VmDesigner
        name="Web shop"
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

type Raw = Record<string, unknown>;
const yaml = () => screen.getByTestId("yaml").textContent ?? "";
const blueprint = () => (parse(yaml()) as { blueprint: Raw }).blueprint;
const component = (key: string) => (blueprint().components as Raw[]).find((c) => c.key === key)!;
// Nodes are clicked with fireEvent: user-event's mousedown reaches d3-zoom, which jsdom can't serve.
const node = (name: RegExp) =>
  within(screen.getByRole("application"))
    .getAllByRole("button")
    .find((b) => b.classList.contains("react-flow__node") && name.test(b.getAttribute("aria-label") ?? ""))!;
const vm = (title: string) => node(new RegExp(`^\\d+ VMs? ${title}\\.`));
const palette = () => screen.getByRole("group", { name: "Add to the topology" });

async function pick(user: ReturnType<typeof userEvent.setup>, select: HTMLElement, option: string | RegExp) {
  await user.click(select);
  await user.click(await screen.findByRole("option", { name: option }));
}

/** Fills a dialog's text fields by label, then adds. */
async function fill(user: ReturnType<typeof userEvent.setup>, dialog: HTMLElement, fields: Record<string, string>) {
  for (const [label, value] of Object.entries(fields)) {
    const box = within(dialog).getByRole("textbox", { name: label });
    await user.clear(box);
    if (value) await user.type(box, value);
  }
  await user.click(within(dialog).getByRole("button", { name: "Add" }));
}

const longTests = { timeout: 30_000 };

describe("the VM designer", longTests, () => {
  it("turns the item's VM into a component when a second is added, its details then focused", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness initial={starter} />);
    await user.click(within(palette()).getByRole("button", { name: "VM" }));
    const dialog = await screen.findByRole("dialog", { name: "Add a VM" });
    expect(within(dialog).getByRole("textbox", { name: "Key of the VM there is" })).toHaveFocus();
    expect(within(dialog).getByRole("textbox", { name: "Key" })).toHaveValue("vm2");
    await fill(user, dialog, { "Key of the VM there is": "web", Key: "db", Title: "Database" });

    expect(blueprint().components).toEqual([{ key: "web" }, { key: "db", title: "Database" }]);
    expect(blueprint().template).toBe("tpl-rhel9");
    await waitFor(() => expect(screen.getByRole("heading", { name: /Database$/ })).toHaveFocus());
    expect(screen.getByRole("textbox", { name: "Key" })).toHaveValue("db");
  });

  it("adds a network, a disk and groups to the selected VM, and refuses what the definition can't hold", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness initial={twoVms} />);
    fireEvent.click(vm("Database"));

    await user.click(within(palette()).getByRole("button", { name: "Network" }));
    let dialog = await screen.findByRole("dialog", { name: "Add a network" });
    expect(within(dialog).getByRole("combobox", { name: "Connected to" })).toHaveTextContent("Database");
    await fill(user, dialog, { Network: "inputs.network" });
    expect(component("db").bindings).toEqual({ network: "inputs.network" });

    fireEvent.click(vm("Database"));
    await user.click(within(palette()).getByRole("button", { name: "Disk" }));
    dialog = await screen.findByRole("dialog", { name: "Add a disk" });
    await fill(user, dialog, { "Size in GB": "0" });
    expect(within(dialog).getByRole("alert")).toHaveTextContent("A size from 1 to 65536 GB.");
    expect(component("db").disks).toBeUndefined();
    await fill(user, dialog, { "Size in GB": "100", Key: "data" });
    expect(component("db").disks).toEqual([{ key: "data", size_gb: 100 }]);
    await waitFor(() => expect(screen.getByRole("heading", { name: /data$/ })).toHaveFocus());

    await user.click(within(palette()).getByRole("button", { name: "Custom resource" }));
    dialog = await screen.findByRole("dialog", { name: "Add a custom resource" });
    await pick(user, await within(dialog).findByRole("combobox", { name: "Resource type" }), /^Load balancer/);
    await fill(user, dialog, { Key: "web" });
    expect(within(dialog).getByRole("alert")).toHaveTextContent("A component or a resource has the key web already.");
    expect(blueprint().resources).toBeUndefined();
  });

  it("connects the selected node to another, saying what it writes or why it can't first", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness initial={twoVms} />);
    fireEvent.click(vm("Database"));
    const panel = screen.getByRole("region", { name: "Selected: Database" });
    const to = within(panel).getByRole("combobox", { name: "Connect to" });

    await pick(user, to, /^websg/);
    expect(within(panel).getByText("Puts Database's VMs in websg.")).toBeInTheDocument();
    await pick(user, to, /^Web servers/);
    expect(within(panel).getByText("Builds Web servers after Database.")).toBeInTheDocument();
    await user.click(within(panel).getByRole("button", { name: "Connect" }));
    expect(component("web").depends_on).toEqual(["db"]);
    expect(within(panel).getByText("Web servers built after Database")).toBeInTheDocument();

    const before = yaml();
    fireEvent.click(vm("Web servers"));
    const web = screen.getByRole("region", { name: "Selected: Web servers" });
    await pick(user, within(web).getByRole("combobox", { name: "Connect to" }), /^Database/);
    expect(
      within(web).getByText("Web servers is built after Database: building Database after it too would wait forever."),
    ).toBeInTheDocument();
    expect(within(web).getByRole("button", { name: "Connect" })).toBeDisabled();
    expect(yaml()).toBe(before);
  });

  it("removes a connection and a node, all undone and redone", async () => {
    const user = userEvent.setup();
    renderWithQuery(
      <Harness
        initial={definition({
          kind: "vm",
          components: [{ key: "web", groups: ["websg"], depends_on: ["db"] }, { key: "db" }],
          security_groups: [{ key: "websg" }],
        })}
      />,
    );
    fireEvent.click(vm("web"));
    const panel = screen.getByRole("region", { name: "Selected: web" });
    await user.click(within(panel).getByRole("button", { name: "Disconnect: web built after db" }));
    expect(component("web").depends_on).toBeUndefined();
    expect(within(panel).getByRole("button", { name: "Disconnect: web in websg" })).toHaveFocus();

    fireEvent.click(node(/^Group websg\./));
    await user.click(screen.getByRole("button", { name: "Remove" }));
    expect(blueprint().security_groups).toBeUndefined();
    expect(component("web").groups).toBeUndefined();

    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(component("web").groups).toEqual(["websg"]);
    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(component("web").depends_on).toEqual(["db"]);
    await user.click(screen.getByRole("button", { name: "Redo" }));
    expect(component("web").depends_on).toBeUndefined();
  });

  it("refuses to remove the item's last VM, saying why", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness initial={starter} />);
    fireEvent.click(within(screen.getByRole("application")).getAllByRole("button")[0]!);
    await user.click(screen.getByRole("button", { name: "Remove" }));
    expect(screen.getByRole("alert")).toHaveTextContent("A VM item builds at least one VM");
    expect(yaml()).toBe(starter);
  });

  it("edits a node's fields, renaming what names it, and says a refusal under its field", async () => {
    const user = userEvent.setup();
    renderWithQuery(
      <Harness initial={definition({ kind: "vm", components: [{ key: "web", depends_on: ["db"] }, { key: "db" }] })} />,
    );
    fireEvent.click(vm("db"));
    const key = screen.getByRole("textbox", { name: "Key" });
    await user.clear(key);
    await user.type(key, "pg{Enter}");
    expect(component("web").depends_on).toEqual(["pg"]);
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Key" })).toHaveFocus());

    const count = screen.getByRole("textbox", { name: "Count" });
    await user.type(count, "30{Enter}");
    expect(screen.getByText("A count from 0 to 20, or CEL over the inputs.")).toBeInTheDocument();
    expect(component("pg").count).toBeUndefined();
    await user.clear(count);
    await user.type(count, "3{Enter}");
    expect(component("pg").count).toBe(3);

    await user.click(screen.getByRole("checkbox", { name: "Keep its VMs on different hosts" }));
    expect(component("pg").anti_affinity).toBe(true);
  });

  it("opens the dialog of a part dropped on the canvas, connected to the VM it was dropped on", async () => {
    renderWithQuery(<Harness initial={twoVms} />);
    const dataTransfer = { types: [partType], getData: (t: string) => (t === partType ? "disk" : "") };
    fireEvent.drop(vm("Database"), { dataTransfer });
    const dialog = await screen.findByRole("dialog", { name: "Add a disk" });
    expect(within(dialog).getByRole("combobox", { name: "Connected to" })).toHaveTextContent("Database");
  });

  it("is read-only, saying why, while the YAML has what it can't tell apart", () => {
    renderWithQuery(
      <Harness initial={definition({ kind: "vm", components: [{ key: "web" }, { key: "web", count: 2 }] })} />,
    );
    expect(screen.getByRole("note")).toHaveTextContent(
      "The canvas can't edit this YAML: Two entries of components have the key web.",
    );
    expect(screen.queryByRole("group", { name: "Add to the topology" })).not.toBeInTheDocument();
    fireEvent.click(vm("web"));
    expect(screen.queryByRole("button", { name: "Remove" })).not.toBeInTheDocument();
  });

  it("shows the server's refusal on its node", () => {
    renderWithQuery(
      <Harness
        initial={twoVms}
        refused={{ field: "/definition/blueprint/components/1/template", message: "No template tpl-gone." }}
      />,
    );
    fireEvent.click(vm("Database"));
    expect(
      within(screen.getByRole("list", { name: "Problems" })).getByText("No template tpl-gone."),
    ).toBeInTheDocument();
  });

  it("writes into the editor's history, and follows what is typed there", async () => {
    const user = userEvent.setup();
    renderWithQuery(<EditorHarness initial={twoVms} />);
    fireEvent.click(vm("Database"));
    await pick(user, screen.getByRole("combobox", { name: "Connect to" }), /^Web servers/);
    await user.click(screen.getByRole("button", { name: "Connect" }));
    expect(component("web").depends_on).toEqual(["db"]);

    const view = EditorView.findFromDOM(document.querySelector<HTMLElement>(".cm-editor")!)!;
    act(() => {
      const doc = view.state.doc.toString();
      const at = doc.indexOf("key: db") + "key: db".length;
      view.dispatch({ changes: { from: at, insert: "\n      count: 3" }, userEvent: "input.type" });
    });
    await waitFor(() => expect(vm("Database").getAttribute("aria-label")).toMatch(/3/));
    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(component("db").count).toBeUndefined();
    await user.click(screen.getByRole("button", { name: "Undo" }));
    expect(component("web").depends_on).toBeUndefined();
  });
});
