import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "@/mocks/node";
import { renderApp } from "@/test/render";

const opsGroup = {
  key: "ops",
  name: "Operations",
  description: "",
  version: 1,
  versions: [1],
  inputs: [],
  constants: { ttl: 300, ipv6: true, zone: "corp.example" },
  used_by: [],
  updated_at: new Date().toISOString(),
};

async function openChange(user: ReturnType<typeof userEvent.setup>, name: string) {
  const row = await screen.findByRole("row", { name: new RegExp(name) });
  await user.click(within(row).getByRole("button", { name: `Actions for ${name}` }));
  await user.click(await screen.findByRole("menuitem", { name: "Change" }));
  return screen.findByRole("dialog", { name: `Change ${name}` });
}

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
    await user.click(within(sheet).getByRole("button", { name: "Create property group" }));

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

  it(
    "keeps a constant's type: a number stays one when changed, and yes or no is a boolean",
    { timeout: 15_000 },
    async () => {
      const user = userEvent.setup();
      const sent: unknown[] = [];
      server.use(
        http.get("*/api/v1/admin/property-groups", () => HttpResponse.json({ items: [opsGroup] })),
        http.put("*/api/v1/admin/property-groups/:key", async ({ request }) => {
          sent.push(await request.json());
          return HttpResponse.json({ ...opsGroup, version: 2, versions: [1, 2] });
        }),
      );
      renderApp("/admin/property-groups");
      const sheet = await openChange(user, "Operations");
      const type = (i: number) => within(sheet).getByLabelText("Type", { selector: `#pg-const-${i}-type` });
      expect(type(0)).toHaveTextContent("Number");
      expect(type(1)).toHaveTextContent("Yes or no");
      expect(type(2)).toHaveTextContent("Text");
      const ttl = within(sheet).getByLabelText("Value", { selector: "#pg-const-0-value" });
      await user.clear(ttl);
      await user.type(ttl, "600");

      await user.click(within(sheet).getByRole("button", { name: "Add constant" }));
      await user.type(within(sheet).getByLabelText("Name", { selector: "#pg-const-3-key" }), "retries");
      await user.click(type(3));
      await user.click(await screen.findByRole("option", { name: "Number" }));
      await user.type(within(sheet).getByLabelText("Value", { selector: "#pg-const-3-value" }), "3");
      await user.click(within(sheet).getByLabelText("Value", { selector: "#pg-const-1-value" }));
      await user.click(await screen.findByRole("option", { name: "No" }));
      await user.click(within(sheet).getByRole("button", { name: "Save as a new version" }));

      await waitFor(() => expect(sent).toHaveLength(1));
      expect((sent[0] as { constants: unknown }).constants).toEqual({
        ttl: 600,
        ipv6: false,
        zone: "corp.example",
        retries: 3,
      });
    },
  );

  it("says what is wrong with the constants before sending them", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    const sent: unknown[] = [];
    server.use(
      http.get("*/api/v1/admin/property-groups", () => HttpResponse.json({ items: [opsGroup] })),
      http.put("*/api/v1/admin/property-groups/:key", async ({ request }) => {
        sent.push(await request.json());
        return HttpResponse.json(opsGroup);
      }),
    );
    renderApp("/admin/property-groups");
    const sheet = await openChange(user, "Operations");
    const ttl = within(sheet).getByLabelText("Value", { selector: "#pg-const-0-value" });
    await user.clear(ttl);
    await user.type(ttl, "five minutes");
    await user.click(within(sheet).getByRole("button", { name: "Add constant" }));
    await user.type(within(sheet).getByLabelText("Name", { selector: "#pg-const-3-key" }), "zone");
    await user.click(within(sheet).getByRole("button", { name: "Add constant" }));
    await user.type(within(sheet).getByLabelText("Value", { selector: "#pg-const-4-value" }), "orphan");
    await user.click(within(sheet).getByRole("button", { name: "Add constant" }));
    await user.click(within(sheet).getByRole("button", { name: "Save as a new version" }));

    expect(within(sheet).getByLabelText("Value", { selector: "#pg-const-0-value" })).toHaveAccessibleDescription(
      "Enter a number, such as 300.",
    );
    expect(within(sheet).getByLabelText("Name", { selector: "#pg-const-3-key" })).toHaveAccessibleDescription(
      "zone is given twice.",
    );
    expect(within(sheet).getByLabelText("Name", { selector: "#pg-const-4-key" })).toHaveAccessibleDescription(
      "Give the constant a name.",
    );
    // The blank row is left out without a word.
    expect(within(sheet).getByLabelText("Name", { selector: "#pg-const-5-key" })).not.toHaveAccessibleDescription();
    expect(sent).toHaveLength(0);
  });

  it("says a problem the server finds outside the sheet's fields", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    server.use(
      http.get("*/api/v1/admin/property-groups", () => HttpResponse.json({ items: [opsGroup] })),
      http.put("*/api/v1/admin/property-groups/:key", () =>
        HttpResponse.json(
          {
            type: "about:blank",
            title: "Some inputs need attention",
            status: 422,
            errors: [{ field: "/inputs", code: "max", message: "A group has at most 50 inputs." }],
          },
          { status: 422, headers: { "Content-Type": "application/problem+json" } },
        ),
      ),
    );
    renderApp("/admin/property-groups");
    const sheet = await openChange(user, "Operations");
    await user.click(within(sheet).getByRole("button", { name: "Save as a new version" }));
    expect(await within(sheet).findByRole("alert")).toHaveTextContent("A group has at most 50 inputs.");
  });

  it("sends an input's help, and drops what a type no longer has", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    const sent: unknown[] = [];
    server.use(
      http.post("*/api/v1/admin/property-groups", async ({ request }) => {
        sent.push(await request.json());
        return HttpResponse.json({ ...opsGroup, key: "sizing", name: "Sizing" }, { status: 201 });
      }),
    );
    renderApp("/admin/property-groups");
    await user.click(await screen.findByRole("button", { name: "New property group" }));
    const sheet = await screen.findByRole("dialog", { name: "New property group" });
    await user.type(within(sheet).getByRole("textbox", { name: "Key" }), "sizing");
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Sizing");
    await user.click(within(sheet).getByRole("button", { name: "Add input" }));
    await user.type(within(sheet).getByLabelText("Name", { selector: "#pg-input-0-name" }), "disk_gb");
    await user.type(within(sheet).getByLabelText("Label"), "Disk");
    await user.click(within(sheet).getByRole("checkbox", { name: "A host variable in the AWX inventory" }));
    await user.click(within(sheet).getByRole("checkbox", { name: "Secret" }));
    expect(within(sheet).queryByLabelText("Default")).toBeNull();
    expect(within(sheet).queryByRole("checkbox", { name: "A host variable in the AWX inventory" })).toBeNull();
    await user.click(within(sheet).getByLabelText("Type", { selector: "#pg-input-0-type" }));
    await user.click(await screen.findByRole("option", { name: "Whole number" }));
    await user.type(within(sheet).getByLabelText("Default"), "40");
    await user.type(within(sheet).getByLabelText("Help"), "In GB, on top of the template's disk.");
    await user.click(within(sheet).getByRole("button", { name: "Add input" }));
    await user.type(within(sheet).getByLabelText("Name", { selector: "#pg-input-1-name" }), "site");
    await user.type(within(sheet).getByLabelText("Label", { selector: "#pg-input-1-label" }), "Site");
    await user.click(
      within(sheet).getByLabelText("A host variable in the AWX inventory", { selector: "#pg-input-1-inventory" }),
    );
    await user.click(within(sheet).getByRole("button", { name: "Create property group" }));

    await waitFor(() => expect(sent).toHaveLength(1));
    expect((sent[0] as { inputs: unknown }).inputs).toEqual([
      { name: "disk_gb", label: "Disk", type: "integer", default: 40, help: "In GB, on top of the template's disk." },
      { name: "site", label: "Site", type: "string", expose_to_inventory: true },
    ]);
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
