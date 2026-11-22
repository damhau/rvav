import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

describe("the NSX Managers rvav manages", () => {
  it("adds one serving a vCenter, moves the vCenter back, then removes it", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/integrations/nsx");
    const region = await screen.findByRole("region", { name: "NSX Managers" });
    expect(await within(region).findByRole("row", { name: /nsx\.corp\.example/ })).toHaveTextContent(
      "vc01.corp.example",
    );

    // The integration's is changed above: here only its vCenters.
    await user.click(within(region).getByRole("button", { name: "Actions for nsx.corp.example" }));
    expect(await screen.findByRole("menuitem", { name: "Link vCenters" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Change" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Remove" })).toBeNull();
    await user.keyboard("{Escape}");

    await user.click(within(region).getByRole("button", { name: "Add an NSX Manager" }));
    const sheet = await screen.findByRole("dialog", { name: "Add an NSX Manager" });
    expect(within(sheet).getByRole("textbox", { name: "Name" })).toHaveFocus();
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "nsx02.corp.example");
    await user.type(within(sheet).getByRole("textbox", { name: "Address" }), "https://nsx02.corp.example");
    await user.type(within(sheet).getByRole("textbox", { name: "Account" }), "svc-rvav");
    const vc01 = within(sheet).getByRole("checkbox", { name: /vc01\.corp\.example/ });
    expect(vc01).toHaveAccessibleName("vc01.corp.example Served by nsx.corp.example");
    await user.click(vc01);
    expect(vc01).toHaveAccessibleName("vc01.corp.example Moves here from nsx.corp.example");
    await user.click(within(sheet).getByRole("button", { name: "Save NSX Manager" }));
    expect(await within(sheet).findByText("Give the account's password.")).toBeInTheDocument();
    await user.type(within(sheet).getByLabelText("Password"), "s3cret-nsx-pass");
    await user.click(within(sheet).getByRole("button", { name: "Test connection" }));
    expect(await within(sheet).findByRole("list", { name: "Test" })).toHaveTextContent("Tier-1 gateways:");
    await user.click(within(sheet).getByRole("button", { name: "Save NSX Manager" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await within(region).findByRole("row", { name: /nsx02\.corp\.example/ })).toHaveTextContent(
      "vc01.corp.example",
    );
    expect(within(region).getByRole("row", { name: /nsx\.corp\.example The connection above/ })).toHaveTextContent(
      "None yet",
    );

    await user.click(within(region).getByRole("button", { name: "Actions for nsx.corp.example" }));
    await user.click(await screen.findByRole("menuitem", { name: "Link vCenters" }));
    const link = await screen.findByRole("dialog", { name: "vCenters nsx.corp.example serves" });
    await user.click(within(link).getByRole("checkbox", { name: /vc01\.corp\.example/ }));
    await user.click(within(link).getByRole("button", { name: "Save vCenters" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() =>
      expect(within(region).getByRole("row", { name: /nsx02\.corp\.example/ })).toHaveTextContent("None yet"),
    );

    await user.click(within(region).getByRole("button", { name: "Actions for nsx02.corp.example" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Stop managing nsx02.corp.example?" });
    await user.click(within(confirm).getByRole("button", { name: "Stop managing it" }));
    expect(await screen.findByText("nsx02.corp.example removed")).toBeInTheDocument();
    await waitFor(() => expect(within(region).queryByRole("row", { name: /nsx02\.corp\.example/ })).toBeNull());
  });
});
