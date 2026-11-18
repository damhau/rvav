import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

describe("the vCenters rvav manages", () => {
  it("adds one in a side panel, then removes it once confirmed", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations/vcenter");
    const region = await screen.findByRole("region", { name: "vCenters" });
    await within(region).findByRole("row", { name: /vc01\.corp\.example/ });
    expect(within(region).queryByRole("button", { name: "Actions for vc01.corp.example" })).toBeNull();

    await user.click(within(region).getByRole("button", { name: "Add a vCenter" }));
    const sheet = await screen.findByRole("dialog", { name: "Add a vCenter" });
    expect(within(sheet).getByRole("textbox", { name: "Name" })).toHaveFocus();
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "vc02.corp.example");
    await user.type(within(sheet).getByRole("textbox", { name: "SDK address" }), "https://vc02.corp.example/sdk");
    await user.type(within(sheet).getByRole("textbox", { name: "Account" }), "svc-rvav@vsphere.local");
    await user.type(within(sheet).getByLabelText("Password"), "s3cret");
    await user.click(within(sheet).getByRole("button", { name: "Save vCenter" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await within(region).findByRole("row", { name: /vc02\.corp\.example/ })).toHaveTextContent("Not synced yet");

    const menu = within(region).getByRole("button", { name: "Actions for vc02.corp.example" });
    await user.click(menu);
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Stop managing vc02.corp.example?" });
    expect(confirm).toHaveAccessibleDescription("rvav stops syncing it and building in it, and forgets its inventory.");
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    expect(within(region).getByRole("row", { name: /vc02\.corp\.example/ })).toBeInTheDocument();

    await user.click(menu);
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    await user.click(within(await screen.findByRole("alertdialog")).getByRole("button", { name: "Stop managing it" }));
    expect(await screen.findByText("vc02.corp.example removed")).toBeInTheDocument();
    await waitFor(() => expect(within(region).queryByRole("row", { name: /vc02\.corp\.example/ })).toBeNull());
  });
});
