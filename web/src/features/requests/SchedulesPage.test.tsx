import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { repeatText } from "./SchedulesPage";

type User = ReturnType<typeof userEvent.setup>;

async function act(user: User, schedule: string, action: string) {
  await user.click(await screen.findByRole("button", { name: `Actions for ${schedule}` }));
  await user.click(await screen.findByRole("menuitem", { name: action }));
}

afterEach(() => {
  localStorage.removeItem("rvav.mock.as");
  resetDb();
});

describe("repeatText", () => {
  it("says an interval in its largest whole unit, and a cron expression with its time zone", () => {
    expect(repeatText({ every_minutes: 60, timezone: "UTC" })).toBe("Every hour");
    expect(repeatText({ every_minutes: 240, timezone: "UTC" })).toBe("Every 4 hours");
    expect(repeatText({ every_minutes: 1440 * 14, timezone: "UTC" })).toBe("Every 2 weeks");
    expect(repeatText({ every_minutes: 90, timezone: "UTC" })).toBe("Every 90 minutes");
    expect(repeatText({ cron: "0 6 * * 1", timezone: "Europe/Zurich" })).toBe("0 6 * * 1 (Europe/Zurich)");
  });
});

describe("Schedules", () => {
  it("lists each schedule with how it repeats, its next run and its last", async () => {
    renderApp("/schedules");
    expect(await screen.findByRole("heading", { name: "Schedules", level: 1 })).toBeInTheDocument();
    const table = await screen.findByRole("table", { name: "Schedules" });
    const tls = await within(table).findByRole("row", { name: /Weekly TLS renewal/ });
    expect(tls).toHaveTextContent("Renew TLS certificate");
    expect(tls).toHaveTextContent("0 6 * * 1 (Europe/Zurich)");
    expect(tls).toHaveTextContent(/in \d+ (day|hour|minute)s?|tomorrow/);
    expect(within(tls).getByRole("link", { name: "REQ-10458" })).toHaveAttribute("href", "/requests/REQ-10458");
    expect(tls).toHaveTextContent("Léa Martin");
    expect(tls).toHaveTextContent("Signed in 5 minutes ago");
    const dns = within(table).getByRole("row", { name: /DNS zone audit/ });
    expect(dns).toHaveTextContent("Every 4 hours");
    expect(dns).toHaveTextContent("Paused");
    expect(dns).toHaveTextContent("Until the zone has moved to the new DDI.");
    expect(dns).toHaveTextContent("It was paused.");
    expect(dns).toHaveTextContent("Marc Keller");
    expect(dns).toHaveTextContent("Signed in 12 days ago");
  });

  it("shows a member their own schedules, and not another's", async () => {
    localStorage.setItem("rvav.mock.as", "member");
    resetDb();
    renderApp("/schedules");
    const table = await screen.findByRole("table", { name: "Schedules" });
    await within(table).findByRole("row", { name: /Weekly TLS renewal/ });
    expect(within(table).queryByRole("row", { name: /DNS zone audit/ })).not.toBeInTheDocument();
  });

  it("shows the next times and the runs, refusals with why", async () => {
    const user = userEvent.setup();
    renderApp("/schedules");
    await act(user, "Weekly TLS renewal", "Show its runs");
    const sheet = await screen.findByRole("dialog", { name: "Weekly TLS renewal" });
    expect(sheet).toHaveTextContent("Léa Martin · Signed in 5 minutes ago");
    expect(sheet).toHaveTextContent("a run is skipped once they haven't signed in for 90 days");
    const next = await within(sheet).findByRole("region", { name: "Next runs" });
    expect(within(next).getAllByRole("listitem")).toHaveLength(5);
    const runs = within(sheet).getByRole("region", { name: "Runs" });
    expect(within(runs).getByRole("link", { name: "REQ-10458" })).toBeInTheDocument();
    expect(runs).toHaveTextContent("Refused");
    expect(runs).toHaveTextContent("Target: the job's inventory has no host shop.corp.example.");
  });

  it("pauses with a note, resumes, and deletes once asked", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/schedules");
    await act(user, "Weekly TLS renewal", "Pause");
    const sheet = await screen.findByRole("dialog", { name: "Pause Weekly TLS renewal" });
    await user.type(within(sheet).getByLabelText("Why"), "The shop is frozen for the sale.");
    await user.click(within(sheet).getByRole("button", { name: "Pause" }));
    expect(await screen.findByText("Weekly TLS renewal paused")).toBeInTheDocument();
    const row = () => screen.getByRole("row", { name: /Weekly TLS renewal/ });
    await waitFor(() => expect(row()).toHaveTextContent("PausedThe shop is frozen for the sale."));

    await act(user, "Weekly TLS renewal", "Resume");
    expect(await screen.findByText("Weekly TLS renewal resumed")).toBeInTheDocument();
    await waitFor(() => expect(row()).not.toHaveTextContent("Paused"));

    await act(user, "Weekly TLS renewal", "Delete");
    const confirm = await screen.findByRole("alertdialog", { name: "Delete Weekly TLS renewal?" });
    expect(confirm).toHaveTextContent("The requests it started stay, and still name it.");
    await user.click(within(confirm).getByRole("button", { name: "Delete schedule" }));
    expect(await screen.findByText("Weekly TLS renewal deleted")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByRole("row", { name: /Weekly TLS renewal/ })).not.toBeInTheDocument());
  });

  it("offers an auditor no change to a schedule they don't own", async () => {
    const user = userEvent.setup();
    localStorage.setItem("rvav.mock.as", "auditor");
    resetDb();
    renderApp("/schedules");
    await user.click(await screen.findByRole("button", { name: "Actions for DNS zone audit" }));
    expect(await screen.findByRole("menuitem", { name: "Show its runs" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Resume" })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "Delete" })).not.toBeInTheDocument();
  });

  it("names the schedule on the requests it started", async () => {
    renderApp("/requests/REQ-10458");
    expect(
      await screen.findByText("Started by the schedule Weekly TLS renewal, as Léa Martin", {}, { timeout: 5000 }),
    ).toBeInTheDocument();
  });
});
