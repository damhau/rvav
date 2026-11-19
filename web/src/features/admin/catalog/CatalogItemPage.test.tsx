import { EditorView } from "@codemirror/view";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ApiError } from "@/lib/api/client";
import type { CatalogItemVersionDetail } from "@/lib/api/types";
import { keys } from "@/lib/query";
import { renderApp } from "@/test/render";
import { editDraft, openDraft, type Saver } from "./designer/draftStore";
import type { Def } from "./designer/model";

const view = () => EditorView.findFromDOM(document.querySelector<HTMLElement>(".cm-editor")!)!;
const shown = () => view().state.doc.toString();

/** A new draft of the RHEL 9 server, open on its Definition tab. */
async function openDefinition() {
  const user = userEvent.setup();
  const app = renderApp("/admin/catalog-items/itm_rhel9");
  await user.click(await screen.findByRole("button", { name: "New draft" }));
  await user.click(await screen.findByRole("tab", { name: "Definition" }));
  await screen.findByRole("textbox", { name: /^Definition of RHEL 9 server/ }, { timeout: 5000 });
  const draft = app.client
    .getQueryCache()
    .findAll({ queryKey: keys.catalogAdmin.all })
    .map((q) => q.state.data as CatalogItemVersionDetail | undefined)
    .find((v) => v?.state === "draft")!;
  await waitFor(() => expect(shown()).toContain("blueprint:"));
  return { ...app, draft };
}

/** The form designer's copy of the draft, changed, its save waiting on the test. */
function designerChange(draft: CatalogItemVersionDetail, save: Saver) {
  const def = structuredClone<Def>(draft.definition);
  def.ui_schema = { ...def.ui_schema, title: "Changed in the form designer" } as Def["ui_schema"];
  act(() => {
    openDraft(draft.id, draft.definition, draft.revision);
    editDraft(draft.id, def, save);
  });
  return def;
}

/** Types in the editor, as a keystroke does. */
const type = (insert: string) => act(() => view().dispatch({ changes: { from: 0, insert }, userEvent: "input.type" }));

describe("the Definition tab", () => {
  it("keeps what is typed when the form designer's copy is saved meanwhile", async () => {
    const { client, draft } = await openDefinition();
    let answer = (_revision: number) => {};
    const def = designerChange(draft, () => new Promise<number>((r) => (answer = r)));
    await waitFor(() => expect(shown()).toContain("Changed in the form designer"));

    type("# mine\n");
    // The save answers (after its pause), as the form designer's does: the draft as stored, then its revision.
    await new Promise((r) => setTimeout(r, 700));
    act(() => {
      client.setQueryData(keys.catalogAdmin.version("itm_rhel9", draft.id), {
        ...draft,
        definition: def,
        revision: draft.revision + 1,
      });
      answer(draft.revision + 1);
    });
    await waitFor(() => expect(screen.queryByText(/This shows the designer's changes/)).not.toBeInTheDocument());
    expect(shown().startsWith("# mine\n")).toBe(true);
    expect(shown()).toContain("Changed in the form designer");
    expect(screen.getByRole("button", { name: "Save draft" })).toBeEnabled();
  });

  it("keeps what is typed when the server refuses the form designer's copy meanwhile", async () => {
    const { draft } = await openDefinition();
    let refuse = () => {};
    designerChange(
      draft,
      () =>
        new Promise<number>(
          (_, reject) =>
            (refuse = () =>
              reject(
                new ApiError(422, {
                  type: "about:blank",
                  title: "Invalid",
                  status: 422,
                  request_id: "req",
                  errors: [{ field: "/definition/ui_schema/title", code: "invalid", message: "No." }],
                }),
              )),
        ),
    );
    await waitFor(() => expect(shown()).toContain("Changed in the form designer"));
    type("# mine\n");
    await new Promise((r) => setTimeout(r, 700));
    await act(async () => {
      refuse();
      await Promise.resolve();
    });
    expect(shown().startsWith("# mine\n")).toBe(true);
  });

  it("says which constraint names a tag vCenter doesn't have, once saved", async () => {
    await openDefinition();
    const at = shown().indexOf("blueprint:\n") + "blueprint:\n".length;
    act(() =>
      view().dispatch({
        changes: { from: at, insert: "  constraints:\n    compute: [rvav-env/test, rvav-tier/gold]\n" },
        userEvent: "input.type",
      }),
    );
    await userEvent.setup().click(screen.getByRole("button", { name: "Save draft" }));
    const warnings = await screen.findByRole("list", { name: "Warnings" });
    expect(warnings).toHaveTextContent(
      /^Line \d+: No cluster in vCenter is tagged rvav-env\/test yet: requests fail until one is\.$/,
    );
  });
});

describe("the item's header and versions", () => {
  it("publishes the draft from the header once the YAML typed is saved or undone", async () => {
    const u = userEvent.setup();
    await openDefinition();
    type("# reviewed\n");
    const publish = await screen.findByRole("button", { name: "Publish 3.3" });
    await waitFor(() => expect(publish).toBeDisabled());
    expect(publish).toHaveAccessibleDescription("Save or undo your changes to the YAML first.");
    await u.click(screen.getByRole("button", { name: "Undo changes" }));
    await waitFor(() => expect(publish).toBeEnabled());
    await u.click(publish);
    expect(await screen.findByText("3.3 published")).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "New draft" })).toBeInTheDocument();
  });

  it("asks before leaving the YAML typed, and stays when told to", async () => {
    const u = userEvent.setup();
    await openDefinition();
    type("# reviewed\n");
    await u.click(screen.getByRole("tab", { name: "Versions" }));
    const ask = await screen.findByRole("alertdialog", { name: "Leave the YAML without saving?" });
    await u.click(within(ask).getByRole("button", { name: "Cancel" }));
    expect(await screen.findByRole("tab", { name: "Definition" })).toHaveAttribute("aria-selected", "true");
    await u.click(screen.getByRole("tab", { name: "Versions" }));
    await u.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Leave without saving" }));
    expect(await screen.findByRole("region", { name: "Versions" })).toBeInTheDocument();
  });

  it("says in the Versions menu why Publish waits", async () => {
    const u = userEvent.setup();
    const { draft } = await openDefinition();
    designerChange(draft, () => new Promise<number>(() => {}));
    await u.click(screen.getByRole("tab", { name: "Versions" }));
    await u.click(await screen.findByRole("button", { name: "Actions for version 3.3" }));
    expect(
      await screen.findByRole("menuitem", { name: "Publish 3.3: save the draft's changes first" }),
    ).toHaveAttribute("data-disabled");
  });

  it("discards a draft from its row after asking", async () => {
    const u = userEvent.setup();
    renderApp("/admin/catalog-items/rhel9-server");
    await u.click(await screen.findByRole("button", { name: "New draft" }));
    await u.click(await screen.findByRole("button", { name: "Actions for version 3.3" }));
    await u.click(await screen.findByRole("menuitem", { name: "Discard" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Discard draft 3.3?" });
    expect(confirm).toHaveTextContent("People keep getting 3.2.");
    await u.click(within(confirm).getByRole("button", { name: "Discard draft" }));
    expect(await screen.findByText("Draft 3.3 discarded")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("button", { name: "Actions for version 3.3" })).toBeNull());
  });

  it("withdraws the published version after saying who loses it", async () => {
    const u = userEvent.setup();
    renderApp("/admin/catalog-items/rhel9-server");
    await u.click(await screen.findByRole("button", { name: "Actions for version 3.2" }));
    await u.click(await screen.findByRole("menuitem", { name: "Withdraw" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Withdraw RHEL 9 server?" });
    expect(confirm).toHaveTextContent("It leaves the catalog of 2 projects.");
    await u.click(within(confirm).getByRole("button", { name: "Withdraw" }));
    expect(await screen.findByText("RHEL 9 server withdrawn")).toBeInTheDocument();
    expect(await screen.findByText("Not published")).toBeInTheDocument();
  });

  it("publishes a draft beside the version offered now, retires that one, and withdraws both", async () => {
    const u = userEvent.setup();
    renderApp("/admin/catalog-items/rhel9-server");
    await u.click(await screen.findByRole("button", { name: "New draft" }));
    await u.click(await screen.findByRole("button", { name: "Actions for version 3.3" }));
    await u.click(await screen.findByRole("menuitem", { name: "Publish 3.3, keeping 3.2 requestable" }));
    expect(await screen.findByText("3.3 published beside the others")).toBeInTheDocument();
    expect(await screen.findByText("Published 3.3, 3.2")).toBeInTheDocument();
    expect(within(screen.getByRole("region", { name: "Versions" })).getByText("Default")).toBeInTheDocument();

    await u.click(screen.getByRole("button", { name: "Actions for version 3.2" }));
    expect(screen.queryByRole("menuitem", { name: "Withdraw" })).toBeNull();
    await u.click(await screen.findByRole("menuitem", { name: "Retire 3.2" }));
    expect(await screen.findByText("3.2 retired")).toBeInTheDocument();
    expect(await screen.findByText("Published 3.3")).toBeInTheDocument();

    await u.click(screen.getByRole("button", { name: "Actions for version 3.2" }));
    await u.click(await screen.findByRole("menuitem", { name: "Offer 3.2 again, beside 3.3" }));
    expect(await screen.findByText("3.2 offered again")).toBeInTheDocument();
    await u.click(await screen.findByRole("button", { name: "More actions for RHEL 9 server" }));
    await u.click(await screen.findByRole("menuitem", { name: "Withdraw" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Withdraw RHEL 9 server?" });
    expect(confirm).toHaveTextContent("Versions 3.3 and 3.2 stay, to roll back to.");
    await u.click(within(confirm).getByRole("button", { name: "Withdraw" }));
    expect(await screen.findByText("Not published")).toBeInTheDocument();
  });

  it("deletes an item nobody requested from its menu, then goes back to the list", async () => {
    const u = userEvent.setup();
    const { router } = renderApp("/admin/catalog-items/windows-server");
    await u.click(await screen.findByRole("button", { name: "More actions for Windows Server 2025" }));
    await u.click(await screen.findByRole("menuitem", { name: "Delete item" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Delete Windows Server 2025?" });
    await u.click(within(confirm).getByRole("button", { name: "Delete item" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/admin/catalog-items"));
    expect(await screen.findByText("Windows Server 2025 deleted")).toBeInTheDocument();
  });

  it("opens the tab the address names", async () => {
    renderApp("/admin/catalog-items/rhel9-server?tab=entitlements");
    expect(await screen.findByRole("region", { name: "Who may request it" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Sharing" })).toHaveAttribute("aria-selected", "true");
  });
});

describe("the item's Git tab", () => {
  it("asks before the repository's change replaces an open draft", async () => {
    const u = userEvent.setup();
    renderApp("/admin/catalog-items/ubuntu-server");
    await u.click(await screen.findByRole("button", { name: "New draft" }));
    expect(await screen.findByText(/Draft 2\.5 made/)).toBeInTheDocument();
    await u.click(screen.getByRole("tab", { name: "Git" }));
    const git = await screen.findByRole("region", { name: "Git" });
    await u.click(await within(git).findByRole("button", { name: "Take the repository's" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Discard draft 2.5?" });
    expect(confirm).toHaveTextContent("What draft 2.5 changed is lost.");
    await u.click(within(confirm).getByRole("button", { name: "Take the repository's" }));
    expect(await screen.findByText("The repository's change is a draft")).toBeInTheDocument();
    await waitFor(() => expect(within(git).queryByRole("button", { name: "Keep rvav's" })).toBeNull());
  });
});
