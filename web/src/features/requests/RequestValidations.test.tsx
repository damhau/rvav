import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { FormValidation, UiSchema } from "@/lib/api/types";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

/** The database user item on two pages or one, with the validations given. */
function withValidations(kind: UiSchema["layout"]["kind"], validations: FormValidation[]) {
  const form = db.forms.itm_dbuser!;
  form.schema = {
    ...form.schema,
    properties: {
      database: { type: "string", enum: ["ledger", "checkout"] },
      role_name: { type: "string", pattern: "^[a-z][a-z0-9_]{2,30}$" },
      password: { type: "string", minLength: 12 },
    },
    required: ["database", "role_name", "password"],
  };
  form.ui_schema = {
    layout: {
      kind,
      sections: [
        { key: "user", title: "Database user", columns: 1, fields: ["database", "role_name"] },
        { key: "access", title: "Password", columns: 1, fields: ["password"] },
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
    },
    validations,
  };
  form.initial_values = { database: "ledger" };
}

const reserved: FormValidation = {
  key: "reserved",
  kind: "cel",
  params: { check: 'inputs.role_name != "admin_user" ? {} : {"role_name": "admin_user is kept for the DBAs."}' },
};
const cmdb: FormValidation = {
  key: "cmdb",
  title: "The CMDB",
  kind: "rest",
  params: { url: "https://cmdb.corp.example/check?role=${inputs.role_name}" },
};

async function openForm() {
  const app = renderApp("/catalog/db-user/request");
  await screen.findByRole("heading", { name: "Request Create database user" }, { timeout: 5000 });
  await screen.findByText("No approval needed", {}, { timeout: 5000 });
  return app;
}

const heading = (name: RegExp) => screen.getByRole("heading", { name });

describe("a form's validations", () => {
  it("refuse before the review step, the message on the input and the focus on its page", async () => {
    withValidations("wizard", [reserved]);
    const user = userEvent.setup();
    const { router } = await openForm();
    const role = screen.getByLabelText("Role name");
    await user.type(role, "admin_user");
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(heading(/^Password/)).toHaveFocus());
    await user.type(screen.getByLabelText("Password"), "correct-horse-battery");
    await user.click(screen.getByRole("button", { name: "Next" }));

    await waitFor(() => expect(screen.getByLabelText("Role name")).toHaveFocus());
    expect(screen.getByLabelText("Role name")).toHaveAccessibleDescription("admin_user is kept for the DBAs.");
    expect(screen.getByText("1 input needs attention.")).toBeInTheDocument();
    expect(screen.getByRole("listitem", { current: "step" })).toHaveTextContent("Database user, needs attention");

    await user.clear(screen.getByLabelText("Role name"));
    await user.type(screen.getByLabelText("Role name"), "app_reporting");
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(heading(/^Password/)).toHaveFocus());
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(heading(/^Review/)).toHaveFocus());
    expect(screen.queryByText(/needs attention/)).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
  }, 15_000);

  it("say on the review step that a service couldn't check, and keep the request from going", async () => {
    withValidations("wizard", [cmdb]);
    const user = userEvent.setup();
    const { router } = await openForm();
    await user.type(screen.getByLabelText("Role name"), "down_role");
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(heading(/^Password/)).toHaveFocus());
    await user.type(screen.getByLabelText("Password"), "correct-horse-battery");
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(heading(/^Review/)).toHaveFocus());
    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(/^The CMDB couldn't check the request \(.+\), so it can't be submitted/);

    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Submit request" })).toBeEnabled());
    expect(router.state.location.pathname).toBe("/catalog/db-user/request");
    expect(screen.getByRole("alert")).toHaveTextContent("The CMDB couldn't check the request");
  }, 15_000);

  it("refuse a one-page form's submit, the focus on the input to fix", async () => {
    withValidations("page", [reserved]);
    const user = userEvent.setup();
    const { router } = await openForm();
    await user.type(screen.getByLabelText("Role name"), "admin_user");
    await user.type(screen.getByLabelText("Password", { selector: "input" }), "correct-horse-battery");
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    const role = screen.getByLabelText("Role name");
    await waitFor(() => expect(role).toHaveFocus());
    expect(role).toHaveAccessibleDescription("admin_user is kept for the DBAs.");

    await user.clear(role);
    await user.type(role, "app_reporting");
    expect(role).not.toHaveAccessibleDescription("admin_user is kept for the DBAs.");
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
  }, 15_000);
});
