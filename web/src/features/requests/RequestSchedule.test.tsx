import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

type User = ReturnType<typeof userEvent.setup>;

async function openForm(slug: string, title: string) {
  renderApp(`/catalog/${slug}/request`);
  await screen.findByRole("heading", { name: `Request ${title}` }, { timeout: 5000 });
  await screen.findByText("No approval needed", {}, { timeout: 5000 });
}

/** Types the job's target, and waits for the debounced preview: an older one's error would take the focus. */
async function target(user: User, value: string) {
  await user.type(screen.getByLabelText("Target"), value);
  await new Promise((r) => setTimeout(r, 500));
}

async function choose(user: User, label: string, option: string) {
  await user.click(screen.getByRole("combobox", { name: label }));
  await user.click(await screen.findByRole("option", { name: option }));
}

afterEach(() => resetDb());

describe("a request on a schedule", () => {
  it("is offered for an item that makes no deployment only", async () => {
    const user = userEvent.setup();
    await openForm("rhel9-server", "RHEL 9 server");
    await user.click(screen.getByRole("combobox", { name: "Start" }));
    expect(await screen.findByRole("option", { name: "At a time I choose" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Again and again, on a schedule" })).not.toBeInTheDocument();
  });

  it("makes a schedule that repeats every so many hours", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    await openForm("tls-cert", "Renew TLS certificate");
    await target(user, "api.corp.example");
    await choose(user, "Start", "Again and again, on a schedule");
    expect(screen.getByRole("button", { name: "Create schedule" })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Summary" })).toHaveTextContent(
      "Each run is a request of its own, which policies, approvals and quota check as they check this one.",
    );
    const name = screen.getByLabelText("Schedule name");
    expect(name).toHaveValue("Renew TLS certificate");
    await user.clear(name);
    await user.type(name, "API certificate");
    await user.clear(screen.getByLabelText("Every"));
    await user.type(screen.getByLabelText("Every"), "6");
    await choose(user, "Unit", "hours");
    await user.click(screen.getByRole("button", { name: "Create schedule" }));
    expect(await screen.findByText("API certificate scheduled")).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "Schedules", level: 1 })).toBeInTheDocument();
    const row = await screen.findByRole("row", { name: /API certificate/ });
    expect(row).toHaveTextContent("Every 6 hours");
    expect(row).toHaveTextContent("None yet");
  });

  it(
    "says next to the cron expression what is wrong with it, and puts the focus there",
    { timeout: 15_000 },
    async () => {
      const user = userEvent.setup();
      await openForm("tls-cert", "Renew TLS certificate");
      await target(user, "api.corp.example");
      await choose(user, "Start", "Again and again, on a schedule");
      await choose(user, "Repeats", "At the times of a cron expression");
      await user.type(screen.getByLabelText("Cron expression"), "0 2 * *");
      await user.clear(screen.getByLabelText("Time zone"));
      await user.type(screen.getByLabelText("Time zone"), "Europe/Geneva");
      await user.click(screen.getByRole("button", { name: "Create schedule" }));
      const cron = screen.getByLabelText("Cron expression");
      await waitFor(() => expect(cron).toHaveAccessibleDescription(/^Five fields: minute, hour/));
      expect(screen.getByLabelText("Time zone")).toHaveAccessibleDescription(/^Not a time zone rvav knows/);
      await waitFor(() => expect(cron).toHaveFocus());

      await user.type(cron, " 1-5");
      await user.clear(screen.getByLabelText("Time zone"));
      await user.type(screen.getByLabelText("Time zone"), "Europe/Zurich");
      await user.click(screen.getByRole("button", { name: "Create schedule" }));
      const row = await screen.findByRole("row", { name: /^Renew TLS certificate/ });
      expect(row).toHaveTextContent("0 2 * * 1-5 (Europe/Zurich)");
    },
  );

  it("refuses the inputs as a request's submit would", async () => {
    const user = userEvent.setup();
    await openForm("tls-cert", "Renew TLS certificate");
    await choose(user, "Start", "Again and again, on a schedule");
    await user.click(screen.getByRole("button", { name: "Create schedule" }));
    const field = screen.getByLabelText("Target");
    await waitFor(() => expect(field).toHaveAttribute("aria-invalid", "true"));
    await waitFor(() => expect(field).toHaveFocus());
  });
});
