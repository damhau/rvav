import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

describe("Day-2 actions", () => {
  it("changes an AWX action in a side panel, its CEL error under the condition", async () => {
    const user = userEvent.setup();
    renderApp("/admin/day2-actions");
    const table = await screen.findByRole("table", { name: "AWX day-2 actions" });
    const row = await within(table).findByRole("row", { name: /Install monitoring agent/ });
    expect(within(row).getByText("Offered")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "day-2 policies" })).toHaveAttribute("href", "/admin/policies?tab=day2");

    await user.click(within(row).getByRole("button", { name: "Actions for Install monitoring agent" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    const sheet = await screen.findByRole("dialog", { name: "Change Install monitoring agent" });
    const condition = within(sheet).getByRole("textbox", { name: "Condition" });
    await user.clear(condition);
    await user.type(condition, "resource.os_family == 'linux' && {(}environment.production");
    await user.click(within(sheet).getByRole("button", { name: "Save action" }));
    expect(await within(sheet).findByText(/This condition is not valid/)).toBeInTheDocument();
    expect(condition).toHaveAccessibleDescription(/This condition is not valid/);

    await user.clear(condition);
    await user.type(condition, "resource.os_family == 'windows'");
    await user.click(within(sheet).getByRole("button", { name: "Save action" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(within(row).getByText("resource.os_family == 'windows'")).toBeInTheDocument();
  });

  it("offers an action that is off from its row menu", async () => {
    const user = userEvent.setup();
    renderApp("/admin/day2-actions");
    const row = await screen.findByRole("row", { name: /Patch now/ });
    expect(within(row).getByText("Off")).toBeInTheDocument();
    await user.click(within(row).getByRole("button", { name: "Actions for Patch now" }));
    await user.click(await screen.findByRole("menuitem", { name: "Offer on deployments" }));
    expect(await screen.findByText("Patch now is offered")).toBeInTheDocument();
    expect(within(row).getByText("Check settings")).toBeInTheDocument();
  });

  it("can't save an action that runs nowhere", async () => {
    const user = userEvent.setup();
    renderApp("/admin/day2-actions");
    const row = await screen.findByRole("row", { name: /Install monitoring agent/ });
    await user.click(within(row).getByRole("button", { name: "Actions for Install monitoring agent" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    const sheet = await screen.findByRole("dialog", { name: "Change Install monitoring agent" });
    for (const box of within(sheet).getAllByRole("checkbox", { checked: true })) {
      if (box.id !== "awx-action-enabled") await user.click(box);
    }
    expect(within(sheet).getByText("Choose where it runs.")).toBeInTheDocument();
    expect(within(sheet).getByRole("button", { name: "Save action" })).toBeDisabled();
  });
});
