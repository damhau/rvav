import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

// Saved settings applying (ADR-0030), against the mock API: its processes
// drain and start again within a second or so of a connection's save.

async function tab(name: string) {
  const user = userEvent.setup();
  renderApp("/admin/integrations");
  await user.click(await screen.findByRole("tab", { name }));
  return { user, panel: await screen.findByRole("region", { name: `${name} connection` }) };
}

describe("saved settings applying", () => {
  it("shows each process draining and starting again with a connection's, then applied", async () => {
    const { user, panel } = await tab("AWX");
    await user.clear(within(panel).getByRole("textbox", { name: "Organisation" }));
    await user.type(within(panel).getByRole("textbox", { name: "Organisation" }), "rvav-test");
    await user.click(within(panel).getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("Each process applies them once its running work is done.")).toBeInTheDocument();

    const applying = await screen.findByRole("region", { name: "Applying the settings" });
    expect(within(applying).getByRole("status")).toHaveTextContent("Applying: each process finishes the work it runs");
    expect(screen.getByText("Applying", { selector: "span" })).toBeInTheDocument();
    const list = within(applying).getByRole("list", { name: "rvav's processes" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(4);
    expect(within(list).getByText("rvav-api-7d9f-2k4xq")).toBeInTheDocument();
    expect(await within(list).findByText("Finishing its running work", {}, { timeout: 3000 })).toBeInTheDocument();

    expect(
      await within(applying).findByText("Applied: every process runs with these settings.", {}, { timeout: 5000 }),
    ).toBeInTheDocument();
    expect(within(list).getAllByText("Applied")).toHaveLength(4);
    // The badge goes once the panel reads that nothing applies any more.
    await screen.findByRole("region", { name: "AWX connection" });
    await expect.poll(() => screen.queryByText("Applying", { selector: "span" }), { timeout: 3000 }).toBeNull();
  });

  it("applies a notification channel's on next use, with no process to start again", async () => {
    const { user, panel } = await tab("Email");
    await user.clear(within(panel).getByRole("textbox", { name: "From" }));
    await user.type(within(panel).getByRole("textbox", { name: "From" }), "rvav <noreply@corp.example>");
    await user.click(within(panel).getByRole("button", { name: "Save settings" }));
    expect(await screen.findByText("They apply from the next notification.")).toBeInTheDocument();
    await screen.findByText(/^Saved /);
    expect(screen.queryByRole("region", { name: "Applying the settings" })).not.toBeInTheDocument();
    expect(screen.queryByText("Applying", { selector: "span" })).not.toBeInTheDocument();
  });
});
