import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

describe("the item's Sharing tab", () => {
  it("shares an item in a project with a group, saying who can request it before saving", async () => {
    const u = userEvent.setup();
    renderApp("/admin/catalog-items/ubuntu-server?tab=entitlements");
    const shares = () => screen.getByRole("table", { name: "Shares" });
    expect(
      within(await screen.findByRole("table", { name: "Shares" })).getByRole("row", { name: /Web Platform/ }),
    ).toHaveTextContent("All members");
    await u.click(screen.getByRole("button", { name: "Share" }));
    const sheet = await screen.findByRole("dialog", { name: "Share Ubuntu 24.04 server" });
    await u.click(within(sheet).getByRole("combobox", { name: "Project" }));
    await u.click(screen.getByRole("option", { name: /Web Platform/ }));
    expect(await within(sheet).findByText("2 people in Web Platform can request it.")).toBeInTheDocument();

    await u.click(within(sheet).getByRole("radio", { name: /Chosen people/ }));
    expect(within(sheet).getByRole("button", { name: "Share" })).toBeDisabled();
    await u.type(within(sheet).getByRole("textbox", { name: "OIDC group" }), "rvav-web{Enter}");
    expect(within(sheet).getByRole("list", { name: "Groups" })).toHaveTextContent("rvav-web");
    const can = await within(sheet).findByRole("list", { name: "Can request it" });
    await waitFor(() => expect(can).toHaveTextContent("Thomas Nguyen · in rvav-web"));
    expect(can).not.toHaveTextContent("Léa Martin");
    expect(within(sheet).getByText(/^Left out: .*Léa Martin/)).toBeInTheDocument();

    await u.click(within(sheet).getByRole("button", { name: "Share" }));
    expect(await screen.findByText("Shared in Web Platform")).toBeInTheDocument();
    await waitFor(() =>
      expect(within(shares()).getByRole("row", { name: /Web Platform/ })).toHaveTextContent("group rvav-web"),
    );
    expect(within(shares()).getByRole("row", { name: /Data Engineering/ })).toHaveTextContent("All members");
  }, 15_000);

  it("opens a share's people from its row, and puts it back to all members", async () => {
    const u = userEvent.setup();
    renderApp("/admin/catalog-items/ubuntu-server?tab=entitlements");
    await u.click(await screen.findByRole("button", { name: "Actions for Data Engineering" }));
    await u.click(await screen.findByRole("menuitem", { name: "Change who may request it" }));
    const sheet = await screen.findByRole("dialog", { name: "Share Ubuntu 24.04 server" });
    expect(within(sheet).getByRole("combobox", { name: "Project" })).toHaveTextContent("Data Engineering");
    await u.click(within(sheet).getByRole("radio", { name: /Chosen people/ }));
    await u.click(within(sheet).getByRole("checkbox", { name: "Project admin" }));
    await u.click(within(sheet).getByRole("button", { name: "Share" }));
    expect(await screen.findByText("Shared in Data Engineering")).toBeInTheDocument();
    const shares = () => screen.getByRole("table", { name: "Shares" });
    await waitFor(() =>
      expect(within(shares()).getByRole("row", { name: /Data Engineering/ })).toHaveTextContent("Project admin"),
    );

    await u.click(screen.getByRole("button", { name: "Actions for Data Engineering" }));
    await u.click(await screen.findByRole("menuitem", { name: "Change who may request it" }));
    const again = await screen.findByRole("dialog", { name: "Share Ubuntu 24.04 server" });
    expect(within(again).getByRole("checkbox", { name: "Project admin" })).toBeChecked();
    await u.click(within(again).getByRole("radio", { name: /All members/ }));
    await u.click(within(again).getByRole("button", { name: "Share" }));
    await waitFor(() =>
      expect(within(shares()).getByRole("row", { name: /Data Engineering/ })).toHaveTextContent("All members"),
    );
  }, 15_000);
});
