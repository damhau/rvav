import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { DeploymentDetail, RequestForm, UiField } from "@/lib/api/types";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";
import { startValues } from "./UpdatePage";

const named = (name: string) => db.deployments.find((d) => d.name === name)!;

describe("the update page against the mock API", () => {
  it("opens from the Actions menu, previews a change and applies it after asking", async () => {
    const user = userEvent.setup();
    const { router } = renderApp(`/deployments/${named("web-tst-search").id}`);
    await user.click(await screen.findByRole("button", { name: "Actions" }, { timeout: 5000 }));
    await user.click(await screen.findByRole("menuitem", { name: "Update" }));
    expect(await screen.findByRole("heading", { level: 1, name: "Update web-tst-search" })).toBeVisible();

    const summary = screen.getByRole("region", { name: "What the update does" });
    expect(await within(summary).findByText("Change an input to see what the update does.")).toBeVisible();
    expect(within(summary).getByRole("button", { name: "Review the update" })).toBeDisabled();

    await user.click(await screen.findByRole("radio", { name: /^X-Large/ }));
    const changes = await within(summary).findByRole("list", { name: "Changes" }, { timeout: 5000 });
    expect(
      within(changes)
        .getAllByRole("listitem")
        .map((li) => li.firstChild?.textContent),
    ).toEqual(["Set 8 vCPU and 48 GB on web-tst-search001", "Set 8 vCPU and 48 GB on web-tst-search002"]);
    expect(summary).toHaveTextContent("No approval needed");
    expect(summary).toHaveTextContent("Quota after this update");

    await user.click(within(summary).getByRole("button", { name: "Review the update" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Update web-tst-search?" });
    expect(dialog).toHaveTextContent("rvav makes these 2 changes now.");
    await user.click(within(dialog).getByRole("button", { name: "Update" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
    expect(await screen.findByRole("heading", { level: 1, name: "Update · web-tst-search" })).toBeVisible();
  }, 20_000);

  it("repairs a failed deployment with its own inputs: its jobs run again", async () => {
    renderApp(`/deployments/${named("web-dev-worker").id}/update`);
    const summary = await screen.findByRole("region", { name: "What the update does" }, { timeout: 5000 });
    const changes = await within(summary).findByRole("list", { name: "Changes" }, { timeout: 5000 });
    expect(
      within(changes)
        .getAllByRole("listitem")
        .map((li) => li.firstChild?.textContent),
    ).toEqual(["Run the AWX job linux-baseline", "Run the AWX job idm-join"]);
    expect(within(summary).getByRole("button", { name: "Review the update" })).toBeEnabled();
  }, 15_000);

  it("refuses a change it can't make in place, saying why", async () => {
    const user = userEvent.setup();
    renderApp(`/deployments/${named("web-dev-api").id}/update`);
    const summary = await screen.findByRole("region", { name: "What the update does" }, { timeout: 5000 });
    await user.click(await screen.findByRole("radio", { name: /^Large/ }));
    const refused = await within(summary).findByRole("list", { name: "Refused" }, { timeout: 5000 });
    expect(refused).toHaveTextContent(
      "web-dev-api004 has snapshots, and vCenter can't grow the disks of a VM with snapshots. Delete them first.",
    );
    expect(within(summary).getByRole("button", { name: "Review the update" })).toBeDisabled();
  }, 15_000);
});

describe("the values an update starts from", () => {
  const ui = (f: Partial<UiField>): UiField => ({ widget: "text", label: "x", ...f });
  const form = (version: string): RequestForm =>
    ({
      version_id: version,
      schema: { properties: { size: {}, owner: {}, token: {}, added: {} } },
      initial_values: { added: "new" },
      ui_schema: {
        layout: { kind: "page", sections: [] },
        fields: {
          size: ui({}),
          owner: ui({ rules: { default: "user.email" } }),
          token: ui({ secret: true }),
          added: ui({ rules: { default: "'x'" } }),
        },
      },
    }) as unknown as RequestForm;
  const d = {
    version: { id: "ver_1", number: "1" },
    inputs: { size: "M", token: "••••", gone: "old" },
  } as unknown as DeploymentDetail;

  it("keep a field the requester emptied empty on the same version, and leave out secrets", () => {
    expect(startValues(form("ver_1"), d)).toEqual({ size: "M", owner: null, added: null });
  });

  it("start a newer version's new fields as on a new request", () => {
    expect(startValues(form("ver_2"), d)).toEqual({ size: "M", added: "new" });
  });
});
