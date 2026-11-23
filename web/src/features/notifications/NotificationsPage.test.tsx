// Notification preferences refused by the server: the focus goes to what the
// refusal names, once it says so, for a keyboard user to hear it there.
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { components } from "@/lib/api/schema";
import { server } from "@/mocks/node";
import { resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

type S = components["schemas"];

afterEach(() => resetDb());

/** The server's 422 for the preferences sent, with the problems `at` finds in them. */
function refuse(at: (b: S["NotificationPreferencesWrite"]) => S["FieldError"][]) {
  server.use(
    http.put("*/api/v1/me/notification-preferences", async ({ request }) =>
      HttpResponse.json(
        {
          type: "https://rvav.dev/problems/validation",
          title: "Some inputs need attention",
          status: 422,
          request_id: "r1",
          errors: at((await request.clone().json()) as S["NotificationPreferencesWrite"]),
        },
        { status: 422, headers: { "Content-Type": "application/problem+json" } },
      ),
    ),
  );
}

async function save(user: ReturnType<typeof userEvent.setup>) {
  screen.getByRole("button", { name: "Save preferences" }).focus();
  await user.keyboard("{Enter}");
}

describe("notification preferences refused", () => {
  it("go to the channel of the rule the refusal names", async () => {
    refuse((b) => [
      {
        field: `/rules/${b.rules.findIndex((r) => r.kind === "request_submitted")}/channels/0`,
        code: "invalid",
        message: "email is not a channel.",
      },
    ]);
    const user = userEvent.setup();
    renderApp("/notifications?tab=preferences");
    const email = await screen.findByRole("checkbox", { name: "Request submitted by Email" });
    email.focus();
    await user.keyboard(" ");
    expect(email).toBeChecked();
    await save(user);
    await waitFor(() => expect(email).toHaveFocus());
    expect(email).toBeInvalid();
    expect(email).toHaveAccessibleDescription("email is not a channel.");
  });

  it("go to the language when the refusal names it", async () => {
    refuse(() => [{ field: "/locale", code: "invalid", message: "en or fr." }]);
    const user = userEvent.setup();
    renderApp("/notifications?tab=preferences");
    const language = await screen.findByRole("combobox", { name: "Language" });
    await save(user);
    await waitFor(() => expect(language).toHaveFocus());
    expect(language).toBeInvalid();
    expect(language).toHaveAccessibleDescription(/en or fr\./);
  });
});
