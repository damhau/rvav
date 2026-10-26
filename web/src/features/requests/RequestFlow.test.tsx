import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

async function openForm() {
  const app = renderApp("/catalog/rhel9-server/request");
  await screen.findByRole("heading", { name: "Request RHEL 9 server" });
  // Wait until server defaults are in and the preview has answered.
  await screen.findByText("No approval needed");
  await waitFor(() => expect(screen.getByLabelText("Role")).toHaveValue("app"));
  return app;
}

describe("request form against the mock API", () => {
  it("maps a 422 from the server onto the field", async () => {
    const user = userEvent.setup();
    await openForm();
    const role = screen.getByLabelText("Role");
    await user.clear(role);
    await user.type(role, "Bad!");
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(screen.getByLabelText("Role")).toHaveAttribute("aria-invalid", "true"));
    expect(screen.getByLabelText("Role")).toHaveAccessibleDescription(/Use 2–12 lower-case letters/);
    expect(db.requests.some((r) => r.request.title.includes("Bad"))).toBe(false);
  });

  it("shows the approval path for production", async () => {
    const user = userEvent.setup();
    await openForm();
    await user.click(screen.getByRole("radio", { name: "prd" }));
    expect(await screen.findByText("Needs approval")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit for approval" })).toBeInTheDocument();
  });

  it("submits, follows the live timeline to Ready, and the deployment appears", async () => {
    const user = userEvent.setup();
    const { router } = await openForm();
    await user.click(screen.getByRole("button", { name: "Submit request" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/requests/REQ-10491"));
    await screen.findByRole("heading", { name: /RHEL 9 server · web-dev-app001/ });
    const steps = screen.getByRole("region", { name: "Steps" });
    expect(await within(steps).findByText("Submitted")).toBeInTheDocument();

    // Steps advance through live events (20 ms per tick in tests).
    await waitFor(() => expect(screen.getAllByText("Succeeded").length).toBeGreaterThan(0), { timeout: 8000 });
    expect(within(steps).getByText(/Powered on · guest IP 10\.31\.10\.57/)).toBeInTheDocument();
    expect(screen.getByRole("log", { name: /Output of AWX job/ })).toHaveTextContent("changed: [web-dev-app001]");

    await user.click(screen.getByRole("link", { name: "Open deployment" }));
    await screen.findByRole("heading", { name: "web-dev-app" });
    expect(await screen.findByText("web-dev-app001")).toBeInTheDocument();
  }, 15000);
});
