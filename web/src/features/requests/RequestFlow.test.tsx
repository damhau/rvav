import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import type { FieldError, FieldValuesQuery, RequestDraft } from "@/lib/api/types";
import { adminState } from "@/mocks/administration";
import { server } from "@/mocks/node";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

async function openForm() {
  const app = renderApp("/catalog/rhel9-server/request");
  // The first render loads the page's code: slower on CI runners.
  await screen.findByRole("heading", { name: "Request RHEL 9 server" }, { timeout: 5000 });
  // Wait until server defaults are in and the preview has answered.
  await screen.findByText("No approval needed", {}, { timeout: 5000 });
  await waitFor(() => expect(screen.getByLabelText("Role")).toHaveValue("app"));
  return app;
}

/** Waits until the sourced fields hold their server defaults, as the summary shows. */
async function placed() {
  const summary = screen.getByRole("region", { name: "Summary" });
  await waitFor(() => expect(summary).toHaveTextContent(/Placement\S+ · \S+.*Network\S/), { timeout: 5000 });
}

describe("request form against the mock API", () => {
  it("shows the VM name the naming policy gives, after a role is typed", async () => {
    const user = userEvent.setup();
    await openForm();
    const role = screen.getByLabelText("Role");
    await user.clear(role);
    await user.type(role, "front");
    await waitFor(() => expect(document.getElementById("f-vm_name")).toHaveTextContent(/web-dev-front\d{3}/), {
      timeout: 5000,
    });
    expect(document.getElementById("f-ip_address")).toHaveTextContent(/10\.31\.10\.\d+/);
  });

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

  it("says a resource with no limit has none, instead of drawing it used up", async () => {
    const web = db.projects[0]!;
    db.quota[web.id]!.limits.memory_gb = null;
    await openForm();
    const summary = screen.getByRole("region", { name: "Summary" });
    expect(summary).toHaveTextContent(/Memory464 GB · no limit/);
    expect(summary).toHaveTextContent(/vCPU116 \/ 160/);
  });

  it("shows the approval path for production", async () => {
    const user = userEvent.setup();
    await openForm();
    await user.click(screen.getByRole("radio", { name: "prd" }));
    expect(await screen.findByText("Needs approval from Infrastructure leads")).toBeInTheDocument();
    expect(screen.getByText(/^Production VMs need sign-off\./)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit for approval" })).toBeInTheDocument();
  });

  it("submits, follows the live timeline to Ready, and the deployment appears", async () => {
    const user = userEvent.setup();
    const { router } = await openForm();
    await user.click(screen.getByRole("button", { name: "Submit request" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/requests/REQ-10491"));
    await screen.findByRole("heading", { name: /RHEL 9 server · web-dev-app001/ }, { timeout: 5000 });
    const steps = screen.getByRole("region", { name: "Steps" });
    expect(await within(steps).findByText("Submitted")).toBeInTheDocument();

    // Steps advance through live events (20 ms per tick in tests).
    await waitFor(() => expect(screen.getAllByText("Succeeded").length).toBeGreaterThan(0), { timeout: 8000 });
    expect(within(steps).getByText(/Powered on · guest IP 10\.31\.10\.57/)).toBeInTheDocument();
    expect(screen.getByRole("log", { name: /Output of AWX job/ })).toHaveTextContent("changed: [web-dev-app001]");

    await user.click(screen.getByRole("link", { name: "Open deployment" }));
    await screen.findByRole("heading", { name: "web-dev-app" }, { timeout: 5000 });
    expect(await screen.findByText("web-dev-app001")).toBeInTheDocument();
  }, 15000);

  it("warns of a lease longer than a soft lease allows, and submits anyway", async () => {
    adminState(db).leases.find((l) => l.environment === "dev" && !l.project_id)!.enforcement = "soft";
    const user = userEvent.setup();
    const { router } = await openForm();
    expect(screen.getByRole("combobox", { name: "Lease" })).toHaveAccessibleDescription(
      /Longer than 90 days warns, and you may go on\.$/,
    );
    await user.click(screen.getByRole("combobox", { name: "Lease" }));
    await user.click(await screen.findByRole("option", { name: "180 days" }));
    const summary = screen.getByRole("region", { name: "Summary" });
    expect(await within(summary).findByRole("note", { name: "Policy warning" })).toHaveTextContent(
      "Leases in Development should be at most 90 days. (Lease in Development)You may submit anyway: the request keeps the warning.",
    );
    await user.click(screen.getByRole("button", { name: "Submit request anyway" }));

    await waitFor(() => expect(router.state.location.pathname).toBe("/requests/REQ-10491"));
    expect(await screen.findByRole("region", { name: "Policy warnings" }, { timeout: 5000 })).toHaveTextContent(
      "Leases in Development should be at most 90 days.",
    );
    expect(db.requests[0]!.request.warnings).toEqual([
      expect.objectContaining({
        policy: "Lease in Development",
        message: "Leases in Development should be at most 90 days.",
      }),
    ]);
  }, 15000);

  it("asks again, with the focus on the warning, when submitted before the preview warned", async () => {
    adminState(db).leases.find((l) => l.environment === "dev" && !l.project_id)!.enforcement = "soft";
    const sent: RequestDraft[] = [];
    server.use(
      http.post("*/api/v1/requests", async ({ request }) => {
        sent.push((await request.clone().json()) as RequestDraft);
        return undefined;
      }),
    );
    const user = userEvent.setup();
    const { router } = await openForm();
    await user.click(screen.getByRole("combobox", { name: "Lease" }));
    await user.click(await screen.findByRole("option", { name: "365 days" }));
    await user.click(screen.getByRole("button", { name: "Submit request" }));

    const summary = screen.getByRole("region", { name: "Summary" });
    const note = await within(summary).findByRole("note", { name: "Policy warning" });
    await waitFor(() => expect(note).toHaveFocus());
    expect(note).toHaveAccessibleDescription("Leases in Development should be at most 90 days. (Lease in Development)");
    expect(sent[0]).toMatchObject({ lease_days: 365, accepted_warnings: [] });
    expect(sent[0]).not.toHaveProperty("accept_warnings");

    await user.click(screen.getByRole("button", { name: "Submit request anyway" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/requests/REQ-10491"));
    expect(sent[1]).toMatchObject({ accepted_warnings: [db.requests[0]!.request.warnings![0]!.id] });
  }, 15000);
});

describe("request form when a value source fails or the server refuses", () => {
  /** Fails the next `forms/values` call for the field, and keeps the bodies of `POST /requests`. */
  function failOnce(field: string) {
    let failed = false;
    const sent: RequestDraft[] = [];
    server.use(
      http.post("*/api/v1/forms/values", async ({ request }) => {
        const q = (await request.clone().json()) as FieldValuesQuery;
        if (q.field !== field || failed) return undefined;
        failed = true;
        return HttpResponse.json(
          { type: "about:blank", title: "Something went wrong on our side", status: 500, request_id: "r1" },
          { status: 500 },
        );
      }),
      http.post("*/api/v1/requests", async ({ request }) => {
        sent.push((await request.clone().json()) as RequestDraft);
        return undefined;
      }),
    );
    return sent;
  }

  function refuse(errors: FieldError[]) {
    server.use(
      http.post("*/api/v1/requests", () =>
        HttpResponse.json(
          {
            type: "https://rvav.dev/problems/validation",
            title: "Some inputs need attention",
            status: 422,
            request_id: "r2",
            errors,
          },
          { status: 422 },
        ),
      ),
    );
  }

  it("shows the failure in place, sends no value it doesn't show, and Try again recovers", async () => {
    const user = userEvent.setup();
    const sent = failOnce("cluster");
    const { router } = await openForm();
    const cluster = screen.getByRole("combobox", { name: "Cluster" });
    await waitFor(() => expect(cluster).toHaveAccessibleDescription("Couldn't load the options."));
    expect(cluster).toHaveTextContent("Not loaded");
    const summary = screen.getByRole("region", { name: "Summary" });
    await waitFor(() => expect(summary).toHaveTextContent(/Network\S/));
    expect(summary).not.toHaveTextContent("Placement");

    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(sent).toHaveLength(1));
    expect(sent[0]!.inputs).not.toHaveProperty("cluster");
    expect(sent[0]!.inputs).not.toHaveProperty("datastore");
    expect(await screen.findByText("2 inputs need attention.")).toBeInTheDocument();
    expect(cluster).toHaveAttribute("aria-invalid", "true");
    // The cluster can't be picked until its options load: the focus goes to what loads them.
    const retry = screen.getByRole("button", { name: "Try again" });
    await waitFor(() => expect(retry).toHaveFocus());

    // Recovered: the field takes the server's default, the datastore follows, and so does the summary.
    await user.keyboard("{Enter}");
    await waitFor(() => expect(cluster).toHaveTextContent("GVA-DEV-CL01"));
    await waitFor(() => expect(cluster).toHaveFocus());
    await waitFor(() => expect(summary).toHaveTextContent(/PlacementGVA-DEV-CL01 · \S+/));
    await waitFor(() => expect(screen.queryByText(/inputs? needs? attention/)).not.toBeInTheDocument());
    expect(cluster).not.toHaveAttribute("aria-invalid");

    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
    expect(sent[1]!.inputs).toMatchObject({ cluster: "GVA-DEV-CL01", datastore: expect.any(String) as string });
  }, 15_000);

  it("puts a 422's errors on their fields, focuses the first, and counts them at the top", async () => {
    const user = userEvent.setup();
    await openForm();
    await placed();
    refuse([
      { field: "/inputs/network", code: "required", message: "This field is required." },
      { field: "/inputs/role", code: "pattern", message: "Use 2–12 lower-case letters." },
    ]);
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    expect(await screen.findByText("2 inputs need attention.")).toBeInTheDocument();
    const role = screen.getByLabelText("Role");
    await waitFor(() => expect(role).toHaveFocus());
    expect(role).toHaveAttribute("aria-invalid", "true");
    expect(role).toHaveAccessibleDescription("Use 2–12 lower-case letters.");
    const network = screen.getByRole("combobox", { name: "Network" });
    expect(network).toHaveAttribute("aria-invalid", "true");
    expect(network).toHaveAccessibleDescription("This field is required.");

    // Each fix takes one off the count.
    await user.type(role, "x");
    expect(await screen.findByText("1 input needs attention.")).toBeInTheDocument();
    expect(role).not.toHaveAttribute("aria-invalid");
  }, 15_000);

  it("says at the top what a 422 says of an input the form has no field for", async () => {
    const user = userEvent.setup();
    await openForm();
    await placed();
    refuse([{ field: "/inputs/zone", code: "required", message: "This field is required." }]);
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    const top = await screen.findByText("1 input needs attention.");
    const alert = top.closest("[role=alert]");
    expect(alert).toHaveTextContent("zone, not on this form: This field is required.");
    await waitFor(() => expect(alert).toHaveFocus());
  }, 15_000);
});
