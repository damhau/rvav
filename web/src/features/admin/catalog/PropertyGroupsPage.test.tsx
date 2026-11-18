import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "@/mocks/node";
import { renderApp } from "@/test/render";

describe("Property groups", () => {
  it("adds a group with an input and a constant", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/property-groups");
    const table = await screen.findByRole("table", { name: "Property groups" });
    expect(await within(table).findByRole("row", { name: /Corporate operations/ })).toHaveTextContent(
      "cost_center, backup",
    );

    await user.click(screen.getByRole("button", { name: "New property group" }));
    const sheet = await screen.findByRole("dialog", { name: "New property group" });
    await user.type(within(sheet).getByRole("textbox", { name: "Key" }), "network");
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Network");
    await user.click(within(sheet).getByRole("button", { name: "Add input" }));
    await user.type(within(sheet).getByLabelText("Name", { selector: "#pg-input-0-name" }), "zone");
    await user.type(within(sheet).getByLabelText("Label"), "Zone");
    await user.type(within(sheet).getByLabelText("Options"), "dmz, internal");
    await user.click(within(sheet).getByRole("checkbox", { name: "Required" }));
    await user.click(within(sheet).getByRole("button", { name: "Add constant" }));
    await user.type(within(sheet).getByLabelText("Name", { selector: "#pg-const-0-key" }), "dns_domain");
    await user.type(within(sheet).getByLabelText("Value"), "corp.example");
    expect(within(sheet).getByText("propgroup.network.<name>")).toBeInTheDocument();
    await user.click(within(sheet).getByRole("button", { name: "Add" }));

    expect(await screen.findByText("Network saved as version 1")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const row = await within(table).findByRole("row", { name: /Network/ });
    expect(row).toHaveTextContent("zone");
    expect(row).toHaveTextContent("dns_domain");
  });

  it("saves a change as the next version", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/property-groups");
    const row = await screen.findByRole("row", { name: /Corporate operations/ });
    await user.click(within(row).getByRole("button", { name: "Actions for Corporate operations" }));
    await user.click(await screen.findByRole("menuitem", { name: "Change" }));
    const sheet = await screen.findByRole("dialog", { name: "Change Corporate operations" });
    expect(within(sheet).getByText(/Saving makes version 2/)).toBeInTheDocument();
    expect(within(sheet).getByRole("textbox", { name: "Key" })).toBeDisabled();
    const value = within(sheet).getByLabelText("Value", { selector: "#pg-const-1-value" });
    await user.clear(value);
    await user.type(value, "ops-l3");
    await user.click(within(sheet).getByRole("button", { name: "Save as a new version" }));
    expect(await screen.findByText("Corporate operations saved as version 2")).toBeInTheDocument();
    await waitFor(() => expect(within(row).getAllByRole("cell")[1]).toHaveTextContent("2"));
  });

  it("can't delete a group an item uses", async () => {
    const user = userEvent.setup();
    server.use(
      http.get("*/api/v1/admin/property-groups", () =>
        HttpResponse.json({
          items: [
            {
              key: "net",
              name: "Network",
              description: "",
              version: 1,
              versions: [1],
              inputs: [],
              constants: { dns_domain: "corp.example" },
              used_by: ["rhel9-server"],
              updated_at: new Date().toISOString(),
            },
          ],
        }),
      ),
    );
    renderApp("/admin/property-groups");
    const row = await screen.findByRole("row", { name: /Network/ });
    expect(row).toHaveTextContent("rhel9-server");
    await user.click(within(row).getByRole("button", { name: "Actions for Network" }));
    expect(await screen.findByRole("menuitem", { name: "Delete: in use" })).toHaveAttribute("aria-disabled", "true");
  });

  it("deletes a group nothing uses", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/property-groups");
    const row = await screen.findByRole("row", { name: /Corporate operations/ });
    await user.click(within(row).getByRole("button", { name: "Actions for Corporate operations" }));
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const confirm = await screen.findByRole("alertdialog", { name: "Delete Corporate operations?" });
    await user.click(within(confirm).getByRole("button", { name: "Delete property group" }));
    expect(await screen.findByText("Corporate operations deleted")).toBeInTheDocument();
    expect(await screen.findByText("No property groups yet")).toBeInTheDocument();
  });
});
