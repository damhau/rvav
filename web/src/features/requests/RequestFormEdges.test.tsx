import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import type { UiSchema } from "@/lib/api/types";
import { server } from "@/mocks/node";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

/** The database user item with a page of more fields: a toggle that shows notes, a lock, a secret. */
function form(kind: UiSchema["layout"]["kind"], extra: Partial<UiSchema> = {}) {
  const f = db.forms.itm_dbuser!;
  f.schema = {
    type: "object",
    properties: {
      database: { type: "string", enum: ["ledger", "checkout"] },
      role_name: { type: "string" },
      extra: { type: "boolean" },
      notes: { type: "string" },
      lock: { type: "boolean" },
      schema_name: { type: "string" },
      password: { type: "string" },
    },
    required: ["database", "role_name"],
  };
  f.ui_schema = {
    layout: {
      kind,
      sections: [
        { key: "user", title: "Database user", columns: 1, fields: ["database", "role_name", "password"] },
        { key: "more", title: "More", columns: 1, fields: ["extra", "notes", "lock", "schema_name"] },
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
      password: { widget: "secret", label: "Password" },
      extra: { widget: "toggle", label: "Extra" },
      notes: { widget: "text", label: "Notes", rules: { visible: "inputs.extra == true" } },
      lock: { widget: "toggle", label: "Lock" },
      schema_name: {
        widget: "text",
        label: "Schema",
        rules: { default: '"fixed_schema"', read_only: "inputs.lock == true" },
      },
    },
    ...extra,
  };
  f.initial_values = { database: "ledger", extra: false, lock: false };
}

async function openForm() {
  const app = renderApp("/catalog/db-user/request");
  await screen.findByRole("heading", { name: "Request Create database user" }, { timeout: 5000 });
  await screen.findByText("No approval needed", {}, { timeout: 5000 });
  return app;
}

function storedInputs(path: string) {
  const number = path.split("/").at(-1);
  return db.requests.find((r) => r.request.number === number)!.request.inputs!;
}

describe("Request form edges", () => {
  it("waits for a file still sending before it submits", async () => {
    form("page");
    const f = db.forms.itm_dbuser!;
    (f.schema as { properties: Record<string, unknown> }).properties.attachment = { type: "object" };
    f.ui_schema.fields.attachment = { widget: "file", label: "Attachment", accept: [".csv"] };
    f.ui_schema.layout.sections[0]!.fields.push("attachment");
    server.use(
      http.post("*/api/v1/forms/files", async () => {
        await delay(600);
        return HttpResponse.json(
          { id: "fil_abc", name: "rules.csv", size: 10, content_type: "text/csv", sha256: "ab" },
          { status: 201 },
        );
      }),
    );
    const user = userEvent.setup();
    const { router } = await openForm();
    await user.type(screen.getByLabelText("Role name"), "app_ro");
    await user.upload(
      document.querySelector<HTMLInputElement>('input[type="file"]')!,
      new File(["a,b\n1,2\n"], "rules.csv", { type: "text/csv" }),
    );
    expect(screen.getByText("Sending rules.csv…")).toBeInTheDocument();
    expect(screen.getByText("Wait until rules.csv is sent.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit request" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Save as draft" })).toBeDisabled();
    await waitFor(() => expect(screen.getByRole("button", { name: "Submit request" })).toBeEnabled(), {
      timeout: 3000,
    });
    expect(screen.queryByText("Wait until rules.csv is sent.")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
    expect(storedInputs(router.state.location.pathname)).toHaveProperty("attachment");
  }, 20_000);
});
