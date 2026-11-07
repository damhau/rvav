import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import { renderApp } from "@/test/render";

const cards = (run: HTMLElement) =>
  within(run)
    .getAllByRole("button")
    .filter((b) => b.classList.contains("react-flow__node"));
const names = (run: HTMLElement) =>
  cards(run).map((b) => b.getAttribute("aria-label")!.split(". ").slice(0, 2).join(". "));
const where = (run: HTMLElement) => ({
  viewport: run.querySelector<HTMLElement>(".react-flow__viewport")?.style.transform,
  nodes: cards(run).map((b) => b.style.transform),
});

async function openRun() {
  const app = renderApp("/requests/REQ-10484");
  // The first render loads the page's code and React Flow's: slower on CI runners.
  const run = await screen.findByRole("application", { name: "Run of REQ-10484" }, { timeout: 5000 });
  // The region shows before React Flow has drawn its nodes.
  await waitFor(() => expect(cards(run).length).toBeGreaterThan(0), { timeout: 5000 });
  return { ...app, run };
}

describe("a composite request's run on its graph", () => {
  it("shows each step's status, and the failed one with its error, its job's output and Retry", async () => {
    const { run } = await openRun();
    expect(names(run)).toEqual([
      "Request. Done",
      "Item Database nodes. Undone",
      "Item The database's owner. Failed",
      "AWX Backups. Undone",
      "Done. Not started",
    ]);
    const owner = screen.getByRole("region", { name: "Selected: The database's owner" });
    expect(within(run).getByRole("button", { name: /^Item The database's owner/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(owner).toHaveTextContent("permission denied to create role");
    expect(within(owner).getByRole("alert")).toHaveTextContent(/could not be created: permission denied/);
    expect(within(owner).getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(
      await screen.findByRole("log", { name: "Output of The database's owner · pg-create-role" }),
    ).toHaveTextContent("permission denied to create role orders_owner");
  });

  it("opens another node's steps; the failure stays under the graph, once", async () => {
    const { run } = await openRun();
    // d3-zoom, under React Flow, can't take a pointer press in jsdom: a click alone.
    fireEvent.click(within(run).getByRole("button", { name: /^AWX Backups/ }));
    const backups = screen.getByRole("region", { name: "Selected: Backups" });
    expect(backups).toHaveTextContent("Undone.");
    expect(backups).toHaveTextContent("Undo Backups · pgbackrest-stanza-delete");
    expect(within(backups).queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "Retry" })).toHaveLength(1);

    fireEvent.click(within(run).getByRole("button", { name: /^Done/ }));
    expect(screen.getByRole("region", { name: "Selected: Done" })).toHaveTextContent(
      /Rolled back.*deleted VM web-dev-pgorders001/,
    );
  });

  it("has the failure read out once, where it first appears, not each time the selection moves it", async () => {
    const user = userEvent.setup();
    const { run } = await openRun();
    expect(screen.getAllByRole("alert")).toHaveLength(1);
    // Under the graph, then back in the failed node's details, then in the timeline: the same box, not an alert.
    fireEvent.click(within(run).getByRole("button", { name: /^AWX Backups/ }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("group", { name: "REQ-10484 failed" })).toHaveTextContent(/could not be created/);
    fireEvent.click(within(run).getByRole("button", { name: /^Item The database's owner/ }));
    const owner = screen.getByRole("region", { name: "Selected: The database's owner" });
    expect(within(owner).queryByRole("alert")).not.toBeInTheDocument();
    expect(within(owner).getByRole("group", { name: "REQ-10484 failed" })).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Timeline" }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("group", { name: "REQ-10484 failed" })).toBeInTheDocument();
  });

  it("has its timeline as the list view, which says it all", async () => {
    const user = userEvent.setup();
    await openRun();
    await user.click(screen.getByRole("button", { name: "Timeline" }));
    const steps = screen.getByRole("region", { name: "Steps" });
    expect(within(steps).getAllByRole("listitem").length).toBeGreaterThan(6);
    expect(steps).toHaveTextContent(/Database nodes.*The database's owner · pg-create-role.*Backups.*Undone\./);
    expect(screen.getAllByRole("button", { name: "Retry" })).toHaveLength(1);
    expect(screen.queryByRole("application", { name: "Run of REQ-10484" })).not.toBeInTheDocument();
  });

  it("follows a retry live to the end, the graph staying where it is", async () => {
    const user = userEvent.setup();
    const { run } = await openRun();
    const before = where(run);
    await user.click(screen.getByRole("button", { name: "Retry" }));
    // Steps advance through live events (20 ms per tick in tests).
    await waitFor(() => expect(names(run)).toContain("Done. Done"), { timeout: 8000 });
    expect(names(run)).toEqual([
      "Request. Done",
      "Item Database nodes. Done",
      "Item The database's owner. Done",
      "AWX Backups. Done",
      "Done. Done",
    ]);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(where(run)).toEqual(before);
  }, 15000);

  it("follows a retry made elsewhere: its rollback, deleted by the retry, no longer undoes the steps", async () => {
    const { run } = await openRun();
    expect(names(run)).toContain("Item Database nodes. Undone");
    // Another page retried it: this one hears of it through the event stream only.
    await call(api.POST("/requests/{requestId}/retry", { params: { path: { requestId: "REQ-10484" } } }));
    await waitFor(() => expect(names(run)).toContain("Done. Done"), { timeout: 8000 });
    expect(names(run)).toEqual([
      "Request. Done",
      "Item Database nodes. Done",
      "Item The database's owner. Done",
      "AWX Backups. Done",
      "Done. Done",
    ]);
  }, 15000);
});
