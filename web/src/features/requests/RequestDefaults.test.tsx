import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { UiSchema } from "@/lib/api/types";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

/** The database user item, its schema's name a default from the role's and its rotation from a CEL source. */
function withDefaults(kind: UiSchema["layout"]["kind"]) {
  const form = db.forms.itm_dbuser!;
  form.schema = {
    type: "object",
    properties: {
      database: { type: "string", enum: ["ledger", "checkout"] },
      role_name: { type: "string", pattern: "^[a-z][a-z0-9_]{2,30}$" },
      schema_name: { type: "string" },
      owner: { type: "string" },
      rotate_days: { type: "integer", minimum: 1, maximum: 90 },
    },
    required: ["database", "role_name"],
  };
  form.ui_schema = {
    layout: {
      kind,
      sections: [
        { key: "user", title: "Database user", columns: 1, fields: ["database", "role_name", "schema_name"] },
        { key: "care", title: "Care", columns: 1, fields: ["owner", "rotate_days"] },
      ],
    },
    fields: {
      database: {
        widget: "select",
        label: "Database",
        options: [
          { value: "ledger", label: "Ledger" },
          { value: "checkout", label: "Checkout" },
        ],
      },
      role_name: { widget: "text", label: "Role name" },
      schema_name: { widget: "text", label: "Schema", rules: { default: 'inputs.role_name + "_schema"' } },
      owner: {
        widget: "text",
        label: "Owner",
        value_source: { kind: "rest", params: { url: "https://cmdb.corp.example/owners", value: "$.login" } },
      },
      rotate_days: {
        widget: "number",
        label: "Rotate every (days)",
        depends_on: ["$environment"],
        value_source: { kind: "cel", params: { value: 'environment == "prd" ? 30 : 90' } },
      },
    },
  };
  form.initial_values = { database: "ledger" };
}

async function openForm() {
  const app = renderApp("/catalog/db-user/request");
  await screen.findByRole("heading", { name: "Request Create database user" }, { timeout: 5000 });
  await screen.findByText("No approval needed", {}, { timeout: 5000 });
  return app;
}

function sent(path: string) {
  const number = path.split("/").at(-1);
  return db.requests.find((r) => r.request.number === number)!.request.inputs!;
}

describe("a form's defaults", () => {
  it("fills a field from another until the requester changes it, and sends what they emptied as such", async () => {
    withDefaults("page");
    const user = userEvent.setup();
    const { router } = await openForm();
    const schema = screen.getByRole("textbox", { name: "Schema" });
    const days = screen.getByRole("spinbutton", { name: "Rotate every (days)" });
    await waitFor(() => expect(days).toHaveValue(90));
    expect(screen.getByRole("textbox", { name: "Owner" })).toHaveValue("svc-01");
    expect(schema).toHaveValue("");

    // The default follows the role's name while the schema has none of its own.
    await user.type(screen.getByRole("textbox", { name: "Role name" }), "app_ro");
    await waitFor(() => expect(schema).toHaveValue("app_ro_schema"), { timeout: 3000 });
    await user.clear(schema);
    await user.type(schema, "reports");
    const role = screen.getByRole("textbox", { name: "Role name" });
    await user.clear(role);
    await user.type(role, "app_rw");
    // The next preview comes and goes: the schema keeps the requester's value.
    await screen.findByText("No approval needed");
    await new Promise((r) => setTimeout(r, 600));
    expect(schema).toHaveValue("reports");

    await user.clear(days);
    expect(days).toHaveValue(null);
    await new Promise((r) => setTimeout(r, 600));
    expect(days).toHaveValue(null);

    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
    const inputs = sent(router.state.location.pathname);
    expect(inputs).toMatchObject({ role_name: "app_rw", schema_name: "reports", owner: "svc-01" });
    expect(inputs).not.toHaveProperty("rotate_days");
  }, 15_000);

  it("lists the defaults in a wizard's review, as they will be sent", async () => {
    withDefaults("wizard");
    const user = userEvent.setup();
    const { router } = await openForm();
    await user.type(screen.getByRole("textbox", { name: "Role name" }), "app_ro");
    await waitFor(() => expect(screen.getByRole("textbox", { name: "Schema" })).toHaveValue("app_ro_schema"), {
      timeout: 3000,
    });
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(screen.getByRole("spinbutton", { name: "Rotate every (days)" })).toHaveValue(90));
    await user.click(screen.getByRole("button", { name: "Next" }));
    const review = await screen.findByRole("region", { name: /^Review/ });
    expect(within(review).getByRole("region", { name: "Database user" })).toHaveTextContent(
      "EnvironmentDevelopmentDatabaseLedgerRole nameapp_roSchemaapp_ro_schema",
    );
    expect(within(review).getByRole("region", { name: "Care" })).toHaveTextContent("Ownersvc-01Rotate every (days)90");
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
    expect(sent(router.state.location.pathname)).toMatchObject({
      schema_name: "app_ro_schema",
      owner: "svc-01",
      rotate_days: 90,
    });
  }, 15_000);
});
