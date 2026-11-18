import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const deployment = (name: string) => db.deployments.find((d) => d.name === name)!;

async function panel(name: string) {
  renderApp(`/deployments/${deployment(name).id}`);
  return screen.findByRole("region", { name: "Remote access" }, { timeout: 5000 });
}

describe("a deployment's remote access", () => {
  it("gives the owner the private key once, in a sheet, then says who has it", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    const box = await panel("web-prd-front");
    expect(box).toHaveTextContent("ops");
    expect(box).toHaveTextContent("With the deployment's key");
    expect(within(box).getByText(/^SHA256:/)).toBeInTheDocument();

    await user.click(within(box).getByRole("button", { name: "Download the private key" }));
    const ask = await screen.findByRole("dialog", { name: "Download the private key" });
    expect(ask).toHaveTextContent("rvav gives the private key once");
    await user.click(within(ask).getByRole("button", { name: "Show the private key" }));

    const shown = await screen.findByRole("dialog", { name: "Your private key" });
    const key = within(shown).getByRole("textbox", { name: "Private key" });
    expect((key as HTMLTextAreaElement).value).toContain("OPENSSH PRIVATE KEY");
    await waitFor(() => expect(key).toHaveFocus());
    expect(within(shown).getByRole("status")).toHaveTextContent("Save it now: rvav no longer has it.");
    expect(within(shown).getByRole("button", { name: /^Save as web-prd-front\.pem$/ })).toBeVisible();
    expect(shown).toHaveTextContent("ssh -i web-prd-front.pem ops@");

    await user.click(within(shown).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
    await waitFor(() =>
      expect(box).toHaveTextContent("Léa Martin downloaded the private key just now. rvav no longer has it."),
    );
    expect(within(box).queryByRole("button", { name: "Download the private key" })).not.toBeInTheDocument();
  });

  it("keeps the key out of TanStack Query's caches", { timeout: 15_000 }, async () => {
    const user = userEvent.setup();
    const { client } = renderApp(`/deployments/${deployment("web-prd-front").id}`);
    const box = await screen.findByRole("region", { name: "Remote access" }, { timeout: 5000 });
    await user.click(within(box).getByRole("button", { name: "Download the private key" }));
    const ask = await screen.findByRole("dialog", { name: "Download the private key" });
    await user.click(within(ask).getByRole("button", { name: "Show the private key" }));
    const shown = await screen.findByRole("dialog", { name: "Your private key" });
    await user.click(within(shown).getByRole("button", { name: "Done" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    const mutations = client.getMutationCache().getAll();
    const queries = client.getQueryCache().getAll();
    expect(JSON.stringify(mutations.map((m) => m.state))).not.toContain("PRIVATE KEY");
    expect(JSON.stringify(queries.map((q) => q.state))).not.toContain("PRIVATE KEY");
  });

  it("says who else may download it", async () => {
    const edge = deployment("web-prd-edge");
    edge.remote_access = [{ component: "vm", kind: "generated_key", username: "ops", fingerprint: "SHA256:abc" }];
    edge.generated_key = { username: "ops", fingerprint: "SHA256:abc", available: true };
    const box = await panel("web-prd-edge");
    expect(box).toHaveTextContent("Only the deployment's owner, Thomas Nguyen, can download its private key.");
    expect(within(box).queryByRole("button", { name: "Download the private key" })).toBeNull();
  });

  it("shows the requester's key, without a download", async () => {
    const box = await panel("web-prd-edge");
    expect(box).toHaveTextContent("thomas");
    expect(box).toHaveTextContent("With the SSH key given for it");
    expect(within(box).queryByRole("button", { name: "Download the private key" })).toBeNull();
  });

  it("says when the key was forgotten with the VMs", async () => {
    deployment("web-prd-front").generated_key!.available = false;
    const box = await panel("web-prd-front");
    expect(box).toHaveTextContent("rvav no longer has the private key: it was forgotten when the VMs were deleted.");
  });

  it("has no panel when the VMs were given no user", async () => {
    renderApp(`/deployments/${deployment("web-dev-api").id}`);
    expect(await screen.findByRole("heading", { level: 1, name: "web-dev-api" }, { timeout: 5000 })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Remote access" })).toBeNull();
  });
});
