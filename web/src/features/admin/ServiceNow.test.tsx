import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import { server } from "@/mocks/node";
import { renderApp } from "@/test/render";

// ServiceNow's page (I1.5, ADR-0065) against the mock API: the token
// ServiceNow requests with, and the CMDB report.

function open() {
  renderApp("/admin/integrations/servicenow");
  return userEvent.setup();
}

describe("ServiceNow's integration", () => {
  it("makes a token shown once, revoking the one before, and revokes it", async () => {
    const user = open();
    const panel = await screen.findByRole("region", { name: "ServiceNow's token" });
    expect(await within(panel).findByText(/ServiceNow last called rvav/)).toBeInTheDocument();
    expect(within(panel).getByText(/any address/)).toBeInTheDocument();
    // ServiceNow's calls fail from then on: asked first.
    await user.click(within(panel).getByRole("button", { name: "Create a new token" }));
    const renew = await screen.findByRole("alertdialog", { name: "Create a new token?" });
    expect(renew).toHaveTextContent(
      "ServiceNow's calls fail until you put the new token in the API Key credential of its rvav alias.",
    );
    await user.click(within(renew).getByRole("button", { name: "Create a new token" }));
    const made = await within(panel).findByRole("region", { name: "ServiceNow's token made" });
    expect(within(made).getByText(/^rvav_pat_/)).toBeInTheDocument();
    expect(within(made).getByText("The token made before is revoked.")).toBeInTheDocument();
    expect(renew).not.toBeInTheDocument();

    await user.click(within(panel).getByRole("button", { name: "Revoke" }));
    const ask = await screen.findByRole("alertdialog", { name: "Revoke ServiceNow's token?" });
    await user.click(within(ask).getByRole("button", { name: "Revoke token" }));
    expect(await within(panel).findByText("None made: ServiceNow can't call rvav.")).toBeInTheDocument();
    expect(within(panel).queryByRole("region", { name: "ServiceNow's token made" })).not.toBeInTheDocument();
    // The Revoke button went with the token: the focus goes to the one that makes the next.
    expect(within(panel).getByRole("button", { name: "Create a token" })).toHaveFocus();

    // None to revoke: made at once.
    await user.click(within(panel).getByRole("button", { name: "Create a token" }));
    expect(await within(panel).findByRole("region", { name: "ServiceNow's token made" })).toBeInTheDocument();
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  }, 20_000);

  it("reports the CIs kept, what ServiceNow refused and the CIs rvav no longer has", async () => {
    const user = open();
    const cmdb = await screen.findByRole("region", { name: "CMDB" });
    expect(await within(cmdb).findByText("cmdb_ci_vmware_instance")).toBeInTheDocument();
    expect(within(cmdb).getByText("Every project")).toBeInTheDocument();
    const refused = within(cmdb).getByRole("table", { name: "Deployments ServiceNow refused" });
    expect(within(refused).getByRole("link", { name: "web-dev-worker" })).toBeInTheDocument();
    expect(within(refused).getByText(/Insufficient rights/)).toBeInTheDocument();
    expect(within(cmdb).getByText("0 deployments; 1 refused, to be tried again")).toBeInTheDocument();
    const orphans = within(cmdb).getByRole("table", { name: "CIs rvav no longer has" });
    expect(within(orphans).getByRole("link", { name: /web-dev-old001/ })).toHaveAttribute(
      "href",
      "https://corp.service-now.com/cmdb_ci_vmware_instance.do?sys_id=b7e2c4a91b3d6010a8f1c0d5e04bcb21",
    );
    // As ServiceNow writes it, in UTC: shown as the page's other times.
    const changed = within(within(orphans).getByRole("row", { name: /web-dev-old001/ })).getByText(/ago$/);
    expect(changed).toHaveAttribute("datetime", "2026-10-28T09:14:52Z");
    await user.click(within(cmdb).getByRole("button", { name: "Reconcile now" }));
    expect(await screen.findByText("rvav reconciles within seconds")).toBeInTheDocument();
  });

  it("says neither how many CIs ServiceNow has nor which rvav no longer has when it couldn't read them", async () => {
    const cmdb = await call(api.GET("/admin/integrations/servicenow/cmdb"));
    const error = "ServiceNow GET /api/now/table/cmdb_ci_vmware_instance: lookup corp.service-now.com: no such host";
    server.use(
      http.get("*/api/v1/admin/integrations/servicenow/cmdb", () =>
        HttpResponse.json({ ...cmdb, servicenow: undefined, orphans: [], orphans_total: 0, reconcile_error: error }),
      ),
    );
    open();
    const region = await screen.findByRole("region", { name: "CMDB" });
    expect(await within(region).findByRole("alert")).toHaveTextContent(`ServiceNow's CIs couldn't be read: ${error}`);
    expect(within(region).getByText("Reconciled")).toBeInTheDocument();
    expect(within(region).queryByText(/ServiceNow has/)).not.toBeInTheDocument();
    expect(within(region).queryByRole("heading", { name: "CIs rvav no longer has" })).not.toBeInTheDocument();
    expect(within(region).queryByText(/^None: every CI/)).not.toBeInTheDocument();
  });

  it("refuses an allowed source that isn't an address and a rate out of range", async () => {
    const user = open();
    const snow = await screen.findByRole("region", { name: "ServiceNow connection" });
    await user.type(within(snow).getByRole("textbox", { name: "Allowed sources" }), "10.0.0.0/33");
    const rate = within(snow).getByRole("textbox", { name: "Requests a minute" });
    await user.clear(rate);
    await user.type(rate, "0");
    await user.click(within(snow).getByRole("button", { name: "Save settings" }));
    expect(
      await within(snow).findByText('"10.0.0.0/33" is neither an address nor a network such as 10.0.0.0/8.'),
    ).toBeInTheDocument();
    expect(within(snow).getByText("From 1 to 6000.")).toBeInTheDocument();
  }, 20_000);
});
