import { EditorView } from "@codemirror/view";
import { act, screen, waitFor } from "@testing-library/react";
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
});
