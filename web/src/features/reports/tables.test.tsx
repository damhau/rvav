// The reports' tables: what was requested most, the leases ending, and the
// chart's figures, each sortable by its columns.
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import type { components } from "@/lib/api/schema";
import { server } from "@/mocks/node";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

type S = components["schemas"];

const day = (n: number) => new Date(Date.UTC(2026, 10, n)).toISOString().slice(0, 10);
const counts = (submitted: number, succeeded: number) => ({
  submitted,
  succeeded,
  failed: submitted - succeeded,
  rejected: 0,
  cancelled: 0,
  success_rate: submitted ? succeeded / submitted : undefined,
});
const item = (id: string, name: string, submitted: number, succeeded: number, p50: number): S["ReportItem"] => ({
  item: { id, slug: id, name },
  ...counts(submitted, succeeded),
  provisioning: { count: succeeded, p50_seconds: p50 },
});

function serve(over: Partial<S["Report"]>) {
  const web = { id: "prj_web", code: "web", name: "Web Platform" };
  const front = db.deployments.find((d) => d.name === "web-prd-front")!;
  const api = db.deployments.find((d) => d.name === "web-dev-api")!;
  const report: S["Report"] = {
    from: day(1),
    to: day(3),
    interval: "day",
    requests: {
      totals: counts(9, 6),
      over_time: [
        { period: day(1), ...counts(2, 2) },
        { period: day(2), ...counts(5, 3) },
        { period: day(3), ...counts(2, 1) },
      ],
      by_item: [
        item("rhel", "RHEL 9 server", 5, 4, 600),
        item("pg", "PostgreSQL cluster", 3, 1, 1800),
        item("ubuntu", "Ubuntu server", 1, 1, 300),
      ],
    },
    provisioning: { count: 6, p50_seconds: 600 },
    approvals: { decided: 0, approved: 0, rejected: 0, lead_time: { count: 0 }, pending: 0 },
    quota: [],
    leases: [
      {
        deployment: { id: front.id, name: front.name },
        project: web,
        environment: "prd",
        owner: { id: "usr_lea", name: "Léa Fontaine" },
        expires_at: `${day(28)}T12:00:00Z`,
      },
      {
        deployment: { id: api.id, name: api.name },
        project: web,
        environment: "dev",
        owner: { id: "usr_tnguyen", name: "Thomas Nguyen" },
        expires_at: `${day(25)}T12:00:00Z`,
      },
    ],
    ...over,
  };
  server.use(http.get("*/api/v1/reports", () => HttpResponse.json(report)));
}

const firstCells = (table: HTMLElement) =>
  within(table)
    .getAllByRole("row")
    .slice(1)
    .map((r) => within(r).getAllByRole("cell")[0]!.textContent);

describe("the reports' tables", () => {
  it("sort what was requested most by its columns", async () => {
    const user = userEvent.setup();
    serve({});
    renderApp("/reports");
    const table = await screen.findByRole("table", { name: "Most requested" }, { timeout: 5000 });
    expect(firstCells(table)).toEqual(["RHEL 9 server", "PostgreSQL cluster", "Ubuntu server"]);
    await user.click(within(table).getByRole("button", { name: "Success rate" }));
    expect(firstCells(table)).toEqual(["PostgreSQL cluster", "RHEL 9 server", "Ubuntu server"]);
    await user.click(within(table).getByRole("button", { name: "Build time (median)" }));
    expect(within(table).getByRole("columnheader", { name: "Build time (median)" })).toHaveAttribute(
      "aria-sort",
      "ascending",
    );
    expect(firstCells(table)).toEqual(["Ubuntu server", "RHEL 9 server", "PostgreSQL cluster"]);
  });

  it("open a lease's deployment from its row, and sort them by when they end", async () => {
    const user = userEvent.setup();
    serve({});
    renderApp("/reports");
    const table = await screen.findByRole("table", { name: "Leases ending in the next 30 days" }, { timeout: 5000 });
    expect(firstCells(table)).toEqual(["web-prd-front", "web-dev-api"]);
    await user.click(within(table).getByRole("button", { name: "Ends" }));
    expect(firstCells(table)).toEqual(["web-dev-api", "web-prd-front"]);
    await user.click(within(table).getByRole("cell", { name: "Thomas Nguyen" }));
    expect(await screen.findByRole("heading", { level: 1, name: /web-dev-api/ })).toBeVisible();
  });

  it("say when nothing was requested and no lease ends", async () => {
    serve({ requests: { totals: counts(0, 0), over_time: [], by_item: [] }, leases: [] });
    renderApp("/reports");
    expect(
      await screen.findByText("No new deployments were requested in this period.", {}, { timeout: 5000 }),
    ).toBeVisible();
    expect(screen.getByText("No lease ends in the next 30 days.")).toBeVisible();
    expect(screen.queryByRole("table", { name: "Most requested" })).toBeNull();
    expect(screen.queryByRole("table", { name: "Leases ending in the next 30 days" })).toBeNull();
  });

  it("give the chart's figures as a table, a row per day", async () => {
    const user = userEvent.setup();
    serve({});
    renderApp("/reports");
    await user.click(await screen.findByText("Show as a table", {}, { timeout: 5000 }));
    const table = screen.getByRole("table", { name: "Requests by period" });
    await waitFor(() => expect(within(table).getAllByRole("row")).toHaveLength(4));
    await user.click(within(table).getByRole("button", { name: "Requests" }));
    await user.click(within(table).getByRole("button", { name: "Requests" }));
    expect(within(table).getAllByRole("columnheader", { name: "Requests" })[0]).toHaveAttribute(
      "aria-sort",
      "descending",
    );
    expect(
      within(table)
        .getAllByRole("row")
        .slice(1)
        .map((r) =>
          within(r)
            .getAllByRole("cell")
            .slice(1)
            .map((c) => c.textContent),
        ),
    ).toEqual([
      ["5", "3", "2", "0"],
      ["2", "2", "0", "0"],
      ["2", "1", "1", "0"],
    ]);
  });
});
