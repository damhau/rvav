import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import type { UiSchema } from "@/lib/api/types";
import { server } from "@/mocks/node";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

/** The database user item with four sections, one shown only when the password rotates. */
function layOut(kind: UiSchema["layout"]["kind"]) {
  const form = db.forms.itm_dbuser!;
  form.schema = {
    ...form.schema,
    properties: {
      database: { type: "string", enum: ["ledger", "checkout"] },
      role_name: { type: "string", pattern: "^[a-z][a-z0-9_]{2,30}$" },
      privileges: { type: "array", items: { enum: ["SELECT", "INSERT"] }, minItems: 1 },
      rotate: { type: "boolean" },
      rotate_days: { type: "integer", minimum: 1, maximum: 90 },
      password: { type: "string", minLength: 12 },
    },
    required: ["database", "role_name", "privileges", "rotate_days", "password"],
  };
  form.ui_schema = {
    layout: {
      kind,
      sections: [
        { key: "user", title: "Database user", columns: 2, fields: ["database", "role_name"] },
        { key: "rights", title: "Privileges", columns: 1, fields: ["privileges", "rotate"] },
        { key: "rotation", title: "Rotation", columns: 1, fields: ["rotate_days"] },
        { key: "access", title: "Password", columns: 1, fields: ["password"] },
      ],
    },
    fields: {
      database: {
        widget: "select",
        label: "Database",
        options: [
          { value: "ledger", label: "Ledger (data-prd-pg-ledger)" },
          { value: "checkout", label: "Checkout" },
        ],
      },
      role_name: { widget: "text", label: "Role name" },
      privileges: {
        widget: "checklist",
        label: "Privileges",
        options: ["SELECT", "INSERT"].map((v) => ({ value: v, label: v })),
      },
      rotate: { widget: "toggle", label: "Rotate the password" },
      rotate_days: { widget: "number", label: "Every (days)", rules: { visible: "inputs.rotate == true" } },
      password: { widget: "secret", label: "Password" },
    },
  };
  form.initial_values = { database: "ledger", privileges: ["SELECT"], rotate: false };
}

async function openForm() {
  const app = renderApp("/catalog/db-user/request");
  await screen.findByRole("heading", { name: "Request Create database user" }, { timeout: 5000 });
  await screen.findByText("No approval needed", {}, { timeout: 5000 });
  return app;
}

const steps = () =>
  within(screen.getByRole("list", { name: "Steps" }))
    .getAllByRole("listitem")
    .map((li) => li.textContent);
const heading = (name: RegExp) => screen.getByRole("heading", { name });
// jsdom puts a space before the heading's hidden ", step n of m"; browsers don't.
const page = (title: string, n: number, of: number) => heading(new RegExp(`^${title} ?, step ${n} of ${of}$`));

describe("a wizard form", () => {
  it("checks each page on Next, skips a page its rules hide, reviews with the secret masked, and submits", async () => {
    layOut("wizard");
    const user = userEvent.setup();
    const { router } = await openForm();
    expect(steps()).toEqual(["1Database user", "2Privileges", "3Password", "4Review"]);
    expect(screen.queryByRole("button", { name: "Submit request" })).not.toBeInTheDocument();
    expect(screen.queryByRole("group", { name: /^Privileges/ })).not.toBeInTheDocument();

    // Next checks the page: the empty role name keeps it, and takes the focus.
    await user.click(screen.getByRole("button", { name: "Next" }));
    const role = screen.getByLabelText("Role name");
    await waitFor(() => expect(role).toHaveFocus());
    expect(role).toHaveAttribute("aria-invalid", "true");
    expect(role).toHaveAccessibleDescription("Role name is required.");
    expect(page("Database user", 1, 4)).toBeInTheDocument();
    await user.type(role, "app_reporting");
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(page("Privileges", 2, 4)).toHaveFocus());
    expect(screen.getByRole("listitem", { current: "step" })).toHaveTextContent("Privileges");

    // Rotating shows the Rotation page, which Next then checks.
    await user.click(screen.getByRole("checkbox", { name: "Rotate the password" }));
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(page("Rotation", 3, 5)).toHaveFocus());
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(screen.getByLabelText("Every (days)")).toHaveFocus());

    // Back, and not rotating: Next skips the Rotation page.
    await user.click(screen.getByRole("button", { name: "Back" }));
    await waitFor(() => expect(page("Privileges", 2, 5)).toHaveFocus());
    await user.click(screen.getByRole("checkbox", { name: "Rotate the password" }));
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(page("Password", 3, 4)).toHaveFocus());
    expect(steps()).toEqual(["Database user, done", "Privileges, done", "3Password", "4Review"]);

    await user.type(screen.getByLabelText("Password"), "correct-horse-battery");
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(page("Review", 4, 4)).toHaveFocus());
    const review = screen.getByRole("region", { name: /^Review/ });
    expect(within(review).getByRole("region", { name: "Database user" })).toHaveTextContent(
      "EnvironmentDevelopmentDatabaseLedger (data-prd-pg-ledger)Role nameapp_reporting",
    );
    expect(within(review).getByRole("region", { name: "Privileges" })).toHaveTextContent(
      "PrivilegesSELECTRotate the passwordNo",
    );
    expect(within(review).queryByRole("region", { name: "Rotation" })).not.toBeInTheDocument();
    const password = within(review).getByRole("region", { name: "Password" });
    expect(password).toHaveTextContent("••••Hidden");
    expect(password).not.toHaveTextContent("correct-horse");

    // Change goes back to its page.
    await user.click(within(review).getByRole("button", { name: "Change Database user" }));
    await waitFor(() => expect(page("Database user", 1, 4)).toHaveFocus());
    for (let i = 0; i < 3; i++) await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(page("Review", 4, 4)).toHaveFocus());

    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
    const number = router.state.location.pathname.split("/").at(-1);
    const sent = db.requests.find((r) => r.request.number === number)!.request;
    expect(sent.inputs).toMatchObject({ role_name: "app_reporting", rotate: false });
    expect(sent.inputs).not.toHaveProperty("rotate_days");
  }, 15_000);

  it("takes a refused submit to the page of the first input to fix", async () => {
    layOut("wizard");
    server.use(
      http.post("/api/v1/requests", () =>
        HttpResponse.json(
          {
            type: "about:blank",
            title: "The request is not valid",
            status: 422,
            errors: [{ field: "/inputs/role_name", code: "taken", message: "That role exists on ledger." }],
          },
          { status: 422, headers: { "content-type": "application/problem+json" } },
        ),
      ),
    );
    const user = userEvent.setup();
    await openForm();
    await user.type(screen.getByLabelText("Role name"), "app_reporting");
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(heading(/^Privileges/)).toHaveFocus());
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(heading(/^Password/)).toHaveFocus());
    await user.type(screen.getByLabelText("Password"), "correct-horse-battery");
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(heading(/^Review/)).toHaveFocus());

    await user.click(screen.getByRole("button", { name: "Submit request" }));
    const role = screen.getByLabelText("Role name");
    await waitFor(() => expect(role).toHaveFocus());
    expect(role).toHaveAccessibleDescription("That role exists on ledger.");
    expect(page("Database user", 1, 4)).toBeVisible();
    expect(screen.getByRole("listitem", { current: "step" })).toHaveTextContent("Database user, needs attention");
  });
});

describe("a tabbed form", () => {
  it("shows a tab per section that shows, marks those with a problem, and opens the first to fix", async () => {
    layOut("tabs");
    const user = userEvent.setup();
    await openForm();
    const tablist = screen.getByRole("tablist", { name: "Form sections" });
    expect(
      within(tablist)
        .getAllByRole("tab")
        .map((t) => t.textContent),
    ).toEqual(["Database user", "Privileges", "Password"]);
    expect(screen.getByRole("tab", { name: "Database user" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("tabpanel", { name: "Database user" })).toContainElement(
      screen.getByRole("radiogroup", { name: "Environment" }),
    );

    // Arrow keys move along the tabs; the hidden section has none until its rule shows it.
    await user.click(screen.getByRole("tab", { name: "Database user" }));
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "Privileges" })).toHaveAttribute("aria-selected", "true");
    await user.click(screen.getByRole("checkbox", { name: "Rotate the password" }));
    expect(await screen.findByRole("tab", { name: "Rotation" })).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Rotate the password" }));
    await waitFor(() => expect(screen.queryByRole("tab", { name: "Rotation" })).not.toBeInTheDocument());

    await user.click(screen.getByRole("tab", { name: "Password" }));
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    const role = screen.getByLabelText("Role name");
    await waitFor(() => expect(role).toHaveFocus());
    expect(screen.getByRole("tab", { name: /^Database user ?, 1 input needs attention$/ })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(screen.getByRole("tab", { name: /^Password ?, 1 input needs attention$/ })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Privileges" })).toBeInTheDocument();
  });
});
