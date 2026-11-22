import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "@/mocks/node";
import { renderApp } from "@/test/render";

/** A new draft of the RHEL 9 server, open in the form designer. */
async function openDesigner() {
  const user = userEvent.setup();
  renderApp("/admin/catalog-items/itm_rhel9");
  await user.click(await screen.findByRole("button", { name: "New draft" }));
  await user.click(await screen.findByRole("tab", { name: "Form designer" }));
  const fields = await screen.findByRole("region", { name: "Fields" }, { timeout: 5000 });
  const preview = screen.getByRole("region", { name: "Preview" });
  await within(preview).findByRole("group", { name: "Basics" }, { timeout: 5000 });
  return { user, fields, preview };
}

const pick = async (user: ReturnType<typeof userEvent.setup>, fields: HTMLElement, layout: string) => {
  await user.click(within(fields).getByRole("combobox", { name: "Layout" }));
  await user.click(screen.getByRole("option", { name: layout }));
};

const steps = (preview: HTMLElement) =>
  within(within(preview).getByRole("list", { name: "Steps" }))
    .getAllByRole("listitem")
    .map((li) => li.textContent);

describe("the form designer's preview", () => {
  it("shows a problem in a table's cell or a list's item on its field", async () => {
    const message = "Choose one of the listed values for AWX job templates.";
    let real: { errors: unknown[] } | undefined;
    const keep = async ({ request, response }: { request: Request; response: Response }) => {
      if (/\/versions\/[^/]+\/preview$/.test(new URL(request.url).pathname))
        real = (await response.clone().json()) as typeof real;
    };
    server.events.on("response:mocked", keep);
    const user = userEvent.setup();
    const { client } = renderApp("/admin/catalog-items/itm_rhel9");
    await user.click(await screen.findByRole("button", { name: "New draft" }));
    await user.click(await screen.findByRole("tab", { name: "Form designer" }));
    const preview = await screen.findByRole("region", { name: "Preview" }, { timeout: 5000 });
    await within(preview).findByRole("group", { name: "Basics" }, { timeout: 5000 });
    await waitFor(() => expect(real).toBeDefined());
    server.events.removeListener("response:mocked", keep);
    server.use(
      http.post("*/api/v1/admin/catalog/items/:id/versions/:vid/preview", () =>
        HttpResponse.json({
          ...real,
          errors: [...real!.errors, { field: "/inputs/post_config/1", code: "enum", message }],
        }),
      ),
    );
    await act(() => client.invalidateQueries());
    await user.click(await within(preview).findByRole("button", { name: "Show them" }, { timeout: 5000 }));
    expect(await within(preview).findByText(message)).toBeInTheDocument();
  }, 20_000);
});

describe("the form designer's layout", () => {
  it("lays the preview out as a wizard, in the pages' order, on the selection's page", async () => {
    const { user, fields, preview } = await openDesigner();
    await pick(user, fields, "Wizard");
    expect(within(fields).getByRole("combobox", { name: "Layout" })).toHaveAccessibleDescription(
      "A page per section, with Back and Next, then a review before submit.",
    );
    expect(within(fields).getByRole("group", { name: "Page Size" })).toBeInTheDocument();
    expect(within(fields).getByRole("button", { name: "Add page" })).toBeInTheDocument();
    await waitFor(() => expect(within(preview).getByRole("list", { name: "Steps" })).toBeInTheDocument(), {
      timeout: 5000,
    });
    expect(steps(preview)).toEqual([
      "1Basics",
      "2Size",
      "3Placement and network",
      "4Configuration after boot",
      "5Review",
    ]);

    await user.click(within(fields).getByRole("button", { name: "Move page Size up" }));
    // The selection, Role, stays on its page: Basics, now the second.
    await waitFor(() => expect(steps(preview).slice(0, 2)).toEqual(["Size, done", "2Basics"]), { timeout: 5000 });

    // Choosing a page, or a field on it, shows it in the preview.
    await user.click(within(fields).getByRole("button", { name: "Placement and network" }));
    expect(screen.getByRole("region", { name: "Page" })).toBeInTheDocument();
    await waitFor(() => expect(within(preview).getByRole("group", { name: /^Placement and network/ })).toBeVisible());
    expect(within(preview).queryByRole("group", { name: /^Size/ })).not.toBeInTheDocument();
    await user.click(within(fields).getByRole("button", { name: /^Role/ }));
    await waitFor(() => expect(within(preview).getByRole("group", { name: /^Basics/ })).toBeVisible());

    // Next checks the page in the preview: an empty role keeps it.
    const role = within(preview).getByLabelText("Role");
    await user.clear(role);
    await user.click(within(preview).getByRole("button", { name: "Next" }));
    await waitFor(() => expect(role).toHaveFocus());
    expect(role).toHaveAttribute("aria-invalid", "true");
  }, 15_000);

  it("lays the preview out as tabs", async () => {
    const { user, fields, preview } = await openDesigner();
    await pick(user, fields, "Tabs");
    expect(within(fields).getByRole("group", { name: "Tab Basics" })).toBeInTheDocument();
    const tablist = await within(preview).findByRole("tablist", { name: "Form sections" }, { timeout: 5000 });
    expect(
      within(tablist)
        .getAllByRole("tab")
        .map((t) => t.textContent),
    ).toEqual(["Basics", "Size", "Placement and network", "Configuration after boot"]);
    await user.click(within(fields).getByRole("button", { name: "Size" }));
    await waitFor(() =>
      expect(within(preview).getByRole("tab", { name: "Size" })).toHaveAttribute("aria-selected", "true"),
    );
    await pick(user, fields, "One page");
    await waitFor(() => expect(within(preview).queryByRole("tablist")).not.toBeInTheDocument(), { timeout: 5000 });
    expect(within(preview).getByRole("group", { name: "Size" })).toBeInTheDocument();
  }, 15_000);
});

describe("the form designer's fields", () => {
  it("leaves the focus on a field moved to the top by keyboard, on the arrow that still moves it", async () => {
    const { user, fields } = await openDesigner();
    const up = within(fields).getByRole("button", { name: "Move VM name up" });
    act(() => up.focus());
    await user.keyboard("{Enter}");
    expect(await within(fields).findByText("VM name moved to position 1 of 2 in Basics.")).toBeInTheDocument();
    await waitFor(() => expect(within(fields).getByRole("button", { name: "Move VM name down" })).toHaveFocus());
    expect(up).toBeDisabled();
  }, 15_000);
});
