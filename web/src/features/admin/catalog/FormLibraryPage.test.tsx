import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { server } from "@/mocks/node";
import { renderApp } from "@/test/render";

const used = {
  key: "cmdbApps",
  name: "CMDB applications",
  description: "",
  use: "source",
  kind: "rest",
  params: { url: "https://cmdb.corp.example/api/apps?env=${params.env}", items: "$[*]", value: "$.id" },
  search: false,
  parameters: [{ name: "env", label: "Environment", required: true }],
  version: 2,
  versions: [1, 2],
  used_by: [
    {
      item_id: "itm_1",
      slug: "rhel9-server",
      name: "RHEL 9 server",
      version_id: "ver_1",
      number: "1.4",
      state: "published",
      entry_version: 1,
    },
    {
      item_id: "itm_1",
      slug: "rhel9-server",
      name: "RHEL 9 server",
      version_id: "ver_2",
      number: "1.5",
      state: "draft",
      entry_version: 2,
    },
  ],
  updated_at: new Date().toISOString(),
};

async function openRow(user: ReturnType<typeof userEvent.setup>, name: string, action: string) {
  const row = await screen.findByRole("row", { name: new RegExp(name) });
  await user.click(within(row).getByRole("button", { name: `Actions for ${name}` }));
  await user.click(await screen.findByRole("menuitem", { name: action }));
}

const tree = {
  ...used,
  key: "cmdbTree",
  name: "CMDB tree",
  params: {
    url: "https://cmdb.corp.example/api/tree",
    items: "$[*]",
    value: "$.id",
    label: "$.name",
    parent: "$.parent",
    cache_seconds: 120,
  },
  parameters: [],
  used_by: [],
};

const pickIn = async (user: ReturnType<typeof userEvent.setup>, at: HTMLElement, name: string, option: string) => {
  await user.click(within(at).getByRole("combobox", { name }));
  await user.click(await screen.findByRole("option", { name: option }));
};

describe("Form library", () => {
  it("keeps a tree's parent and what the sheet doesn't show", { timeout: 20_000 }, async () => {
    const bodies: { params: Record<string, unknown> }[] = [];
    server.use(
      http.get("*/api/v1/admin/form-library", () => HttpResponse.json({ items: [tree] })),
      http.put("*/api/v1/admin/form-library/:key", async ({ request }) => {
        bodies.push((await request.json()) as (typeof bodies)[number]);
        return HttpResponse.json({ ...tree, version: 2, versions: [1, 2], drafts: [] });
      }),
    );
    const user = userEvent.setup();
    renderApp("/admin/form-library");
    await openRow(user, "CMDB tree", "Change or test");
    const sheet = await screen.findByRole("dialog", { name: "Change CMDB tree" });
    const label = within(sheet).getByRole("textbox", { name: "Label" });
    expect(within(sheet).getByRole("textbox", { name: "Parent" })).toHaveValue("$.parent");
    await user.clear(label);
    await user.type(label, "$.title");
    await user.click(within(sheet).getByRole("button", { name: "Save as a new version" }));
    await waitFor(() => expect(bodies).toHaveLength(1));
    expect(bodies[0]!.params).toEqual({ ...tree.params, label: "$.title" });
  });

  it("tests a REST source for its options and its value, as the server does", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/form-library");
    await openRow(user, "CMDB services", "Change or test");
    const sheet = await screen.findByRole("dialog", { name: "Change CMDB services" });
    const testing = within(sheet).getByRole("group", { name: "Test" });
    for (const box of within(testing).queryAllByRole("textbox")) await user.type(box, "dev");
    await user.click(within(sheet).getByRole("button", { name: "Run test" }));
    expect(await within(testing).findByText("2 options")).toBeInTheDocument();
    expect(within(testing).getByText(/^Value:/)).toHaveTextContent('Value: "svc-01"');
  });

  it("puts a problem with a parameter on its row, past a row left out", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/form-library");
    await user.click(await screen.findByRole("button", { name: "New entry" }));
    const sheet = await screen.findByRole("dialog", { name: "New form library entry" });
    await user.type(within(sheet).getByRole("textbox", { name: "Key" }), "twoRows");
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Two rows");
    await user.click(within(sheet).getByRole("button", { name: "Add parameter" }));
    await user.click(within(sheet).getByRole("button", { name: "Add parameter" }));
    await user.type(within(sheet).getByLabelText("Name", { selector: "#fl-prm-1-name" }), "env");
    await user.click(within(sheet).getByRole("button", { name: "Create entry" }));
    const second = within(sheet).getByLabelText("Label", { selector: "#fl-prm-1-label" });
    await waitFor(() => expect(second).toHaveAccessibleDescription(/label/i));
    expect(second).toHaveAttribute("aria-invalid", "true");
    expect(within(sheet).getByLabelText("Label", { selector: "#fl-prm-0-label" })).not.toHaveAttribute(
      "aria-invalid",
      "true",
    );
  });

  it(
    "tests with a parameter named with a space, as a project's form in an environment",
    { timeout: 20_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/form-library");
      await user.click(await screen.findByRole("button", { name: "New entry" }));
      const sheet = await screen.findByRole("dialog", { name: "New form library entry" });
      await user.type(within(sheet).getByRole("textbox", { name: "Key" }), "where");
      await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Where");
      await pickIn(user, sheet, "Kind", "CEL expression");
      await user.click(within(sheet).getByRole("button", { name: "Add parameter" }));
      await user.type(within(sheet).getByLabelText("Name", { selector: "#fl-prm-0-name" }), "tier ");
      await user.type(within(sheet).getByLabelText("Label", { selector: "#fl-prm-0-label" }), "Tier");
      await user.click(within(sheet).getByRole("textbox", { name: "Options" }));
      await user.paste("[params.tier, environment, project.code]");
      const testing = within(sheet).getByRole("group", { name: "Test" });
      const tier = within(testing).getByRole("textbox", { name: "Tier" });
      await user.type(tier, "gold");
      expect(tier).toHaveValue("gold");
      await pickIn(user, testing, "Project", "Web Platform");
      await pickIn(user, testing, "Environment", "Development");
      await user.click(within(sheet).getByRole("button", { name: "Run test" }));
      expect(await within(sheet).findByText("3 options")).toBeInTheDocument();
      for (const o of ["gold", "dev", "web"]) expect(within(testing).getAllByText(o).length).toBeGreaterThan(0);
    },
  );
  it("adds a validation after testing it", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/form-library");
    const table = await screen.findByRole("table", { name: "Form library entries" });
    expect(await within(table).findByRole("row", { name: /CMDB services/ })).toHaveTextContent("Value source");
    expect(within(table).getByRole("row", { name: /Cost centre open/ })).toHaveTextContent("Validation");

    await user.click(screen.getByRole("button", { name: "New entry" }));
    const sheet = await screen.findByRole("dialog", { name: "New form library entry" });
    await user.type(within(sheet).getByRole("textbox", { name: "Key" }), "shortName");
    await user.type(within(sheet).getByRole("textbox", { name: "Name" }), "Short name");
    await user.click(within(sheet).getByRole("combobox", { name: "Purpose" }));
    await user.click(await screen.findByRole("option", { name: "Validation" }));
    await user.click(within(sheet).getByRole("combobox", { name: "Kind" }));
    await user.click(await screen.findByRole("option", { name: "CEL expression" }));
    await user.click(within(sheet).getByRole("button", { name: "Add parameter" }));
    await user.type(within(sheet).getByLabelText("Name", { selector: "#fl-prm-0-name" }), "name");
    await user.type(within(sheet).getByLabelText("Label", { selector: "#fl-prm-0-label" }), "Name");
    await user.click(within(sheet).getByRole("checkbox", { name: /Required/ }));
    await user.click(within(sheet).getByRole("textbox", { name: "Check" }));
    await user.paste('size(params.name) <= 8 ? "" : "At most 8 characters."');

    const tryName = within(within(sheet).getByRole("group", { name: "Test" })).getByRole("textbox", { name: "Name" });
    await user.type(tryName, "much-too-long");
    await user.click(within(sheet).getByRole("button", { name: "Run test" }));
    expect(await within(sheet).findByText("At most 8 characters.")).toBeInTheDocument();
    await user.clear(tryName);
    await user.type(tryName, "web");
    await user.click(within(sheet).getByRole("button", { name: "Run test" }));
    expect(await within(sheet).findByText("It passes.")).toBeInTheDocument();

    await user.click(within(sheet).getByRole("button", { name: "Create entry" }));
    expect(await screen.findByText("Short name saved as version 1")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await within(table).findByRole("row", { name: /Short name/ })).toHaveTextContent("CEL expression");
  });

  it("shows where an entry reads a form's inputs", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/form-library");
    await openRow(user, "CMDB services", "Change or test");
    const sheet = await screen.findByRole("dialog", { name: "Change CMDB services" });
    expect(within(sheet).getByRole("textbox", { name: "Key" })).toBeDisabled();
    expect(within(sheet).getByRole("combobox", { name: "Purpose" })).toBeDisabled();
    expect(within(sheet).getByText("No item's form names it yet.")).toBeInTheDocument();
    const url = within(sheet).getByRole("textbox", { name: "URL" });
    await user.clear(url);
    await user.click(url);
    await user.paste("https://cmdb.corp.example/api/services?env=${inputs.environment}");
    await user.click(within(sheet).getByRole("button", { name: "Save as a new version" }));
    expect(
      await within(sheet).findByText(
        "An entry reads what forms give it, its parameters, as ${params.x}: not a form's inputs.",
      ),
    ).toBeInTheDocument();
  });

  it("lists who uses an entry, says the drafts a new version made, and keeps it", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    server.use(
      http.get("*/api/v1/admin/form-library", () => HttpResponse.json({ items: [used] })),
      http.put("*/api/v1/admin/form-library/:key", () =>
        HttpResponse.json({
          ...used,
          version: 3,
          versions: [1, 2, 3],
          drafts: [
            { item_id: "itm_1", slug: "rhel9-server", name: "RHEL 9 server", number: "1.5", created: false },
            {
              item_id: "itm_2",
              slug: "pg",
              name: "PostgreSQL",
              number: "2.0",
              created: false,
              problem: "With version 3 of cmdbApps, its form wouldn't pass the designer's checks.",
            },
          ],
        }),
      ),
    );
    renderApp("/admin/form-library");
    const row = await screen.findByRole("row", { name: /CMDB applications/ });
    expect(row).toHaveTextContent("RHEL 9 server");
    await user.click(within(row).getByRole("button", { name: "Actions for CMDB applications" }));
    expect(await screen.findByRole("menuitem", { name: "Delete: 1 item uses it" })).toHaveAttribute(
      "aria-disabled",
      "true",
    );
    await user.click(screen.getByRole("menuitem", { name: "Change or test" }));
    const sheet = await screen.findByRole("dialog", { name: "Change CMDB applications" });
    const usedBy = within(sheet).getByRole("region", { name: "Used by" });
    expect(usedBy).toHaveTextContent("version 1.4, published");
    expect(usedBy).toHaveTextContent("uses version 1");
    expect(within(sheet).getByText(/Saving makes version 3/)).toBeInTheDocument();
    await user.click(within(sheet).getByRole("button", { name: "Save as a new version" }));
    expect(await screen.findByText("CMDB applications saved as version 3")).toBeInTheDocument();
    expect(
      screen.getByText(/Drafts naming it: RHEL 9 server 1.5\. PostgreSQL: With version 3 of cmdbApps/),
    ).toBeInTheDocument();
  });

  it("deletes an entry nothing uses", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/form-library");
    await openRow(user, "Cost centre open", "Delete");
    const dialog = await screen.findByRole("alertdialog", { name: "Delete Cost centre open?" });
    await user.click(within(dialog).getByRole("button", { name: "Delete entry" }));
    expect(await screen.findByText("Cost centre open deleted")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("row", { name: /Cost centre open/ })).toBeNull());
  });
});
