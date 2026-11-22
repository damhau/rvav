import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const stored = (number: string) => db.requests.find((r) => r.request.number === number)!;

async function openRun() {
  const app = renderApp("/requests/REQ-10484");
  const run = await screen.findByRole("application", { name: "Run of REQ-10484" }, { timeout: 5000 });
  const data = await screen.findByRole("region", { name: "Step data" });
  return { ...app, run, data };
}

describe("a run's data (ADR-0057)", () => {
  it("shows what the selected node's step was given; one that failed gave nothing", async () => {
    const { data } = await openRun();
    expect(data).toHaveTextContent("The database's owner · pg-create-role");
    const inputs = await within(data).findByRole("region", { name: "Inputs" });
    expect(within(inputs).getByText("role_name")).toBeVisible();
    expect(within(inputs).getByText("orders_owner")).toBeVisible();
    expect(within(data).getByRole("region", { name: "Outputs" })).toHaveTextContent("It gave nothing.");
  });

  it("opens another step's data from its node, with what later steps read of it", async () => {
    const user = userEvent.setup();
    const { run, data } = await openRun();
    fireEvent.click(within(run).getByRole("button", { name: /^AWX Backups/ }));
    const backups = screen.getByRole("region", { name: "Selected: Backups" });
    const show = within(backups).getByRole("button", { name: "Showing data of Backups · pgbackrest-stanza" });
    expect(show).toHaveAttribute("aria-pressed", "true");
    const outputs = await within(data).findByRole("region", { name: "Outputs" });
    expect(outputs).toHaveTextContent("Later steps read them as steps.backups.<name>.");
    expect(outputs).toHaveTextContent(/job_id\s*18235/);
    expect(within(data).getByRole("region", { name: "Inputs" })).toHaveTextContent(/"fqdn": "web-dev-pgorders002/);

    await user.click(screen.getByRole("button", { name: "Timeline" }));
    await user.click(screen.getByRole("button", { name: "Show data of Database nodes" }));
    expect(screen.getByRole("button", { name: "Showing data of Database nodes" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(await within(data).findByRole("region", { name: "Inputs" })).toHaveTextContent(
      "It was given nothing it keeps.",
    );
    expect(within(data).getByRole("region", { name: "Outputs" })).toHaveTextContent(/moref\s*vm-7311/);
  });

  it("reads a secret as masked, and says when only others read a person's answer", async () => {
    const r = stored("REQ-10484");
    const owner = r.steps.find((s) => s.key === "step:owner")!;
    const backups = r.steps.find((s) => s.key === "step:backups")!;
    r.data = {
      ...r.data,
      [owner.id]: {
        step_id: owner.id,
        inputs: { password: "••••", role_name: "orders_owner" },
        outputs: null,
        outputs_hidden: false,
      },
      [backups.id]: { step_id: backups.id, inputs: null, outputs: null, outputs_hidden: true },
    };
    const { run, data } = await openRun();
    const inputs = await within(data).findByRole("region", { name: "Inputs" });
    expect(within(inputs).getByText("Secret, masked")).toHaveClass("sr-only");
    expect(inputs).not.toHaveTextContent("orders_owner_password");
    fireEvent.click(within(run).getByRole("button", { name: /^AWX Backups/ }));
    await waitFor(() =>
      expect(within(data).getByText(/Only the people it asks, and those who read every approval/)).toBeVisible(),
    );
  });

  it("is for those who manage the catalog or audit: a requester reads the steps, not their data", async () => {
    db.me.permissions = db.me.permissions.filter((p) => p !== "catalog.manage" && p !== "extensibility.read");
    renderApp("/requests/REQ-10484");
    await screen.findByRole("application", { name: "Run of REQ-10484" }, { timeout: 5000 });
    expect(await screen.findByRole("region", { name: "Selected: The database's owner" })).toBeVisible();
    expect(screen.queryByRole("region", { name: "Step data" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /data of/ })).not.toBeInTheDocument();
  });

  it("drops a step's data on a retry, and shows what it gives when it runs again", async () => {
    const user = userEvent.setup();
    const { run, data } = await openRun();
    fireEvent.click(within(run).getByRole("button", { name: /^Item The database's owner/ }));
    expect(await within(data).findByText("orders_owner")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Retry" }));
    // A step run again has no data until it gives some: the panel goes, and comes back.
    const panel = () => screen.getByRole("region", { name: "Step data" });
    await waitFor(() => expect(panel()).toHaveTextContent(/job_id\s*18236/), { timeout: 8000 });
    expect(panel()).not.toHaveTextContent("orders_owner");
  }, 15000);
});
