import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import type { Day2FormDefinition } from "@/lib/api/types";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const idOf = (name: string) => db.deployments.find((d) => d.name === name)!.id;

/** The monitoring agent's form as an administrator designed it: its defaults read the VM. */
const monitoring: Day2FormDefinition = {
  form_schema: {
    type: "object",
    required: ["monitoring_agent", "listen_port"],
    properties: {
      monitoring_agent: { type: "string", enum: ["node_exporter", "telegraf"] },
      listen_port: { type: "integer", minimum: 1024, maximum: 65535 },
      metrics_path: { type: "string" },
    },
  },
  ui_schema: {
    fields: {
      monitoring_agent: {
        label: "Agent",
        widget: "segmented",
        options: [
          { value: "node_exporter", label: "node_exporter" },
          { value: "telegraf", label: "telegraf" },
        ],
        rules: { default: "resource.os_family == 'windows' ? 'telegraf' : 'node_exporter'" },
      },
      listen_port: {
        label: "Port",
        widget: "number",
        rules: { default: "inputs.monitoring_agent == 'telegraf' ? 8125 : 9100" },
      },
      metrics_path: {
        label: "Metrics path",
        widget: "text",
        rules: { visible: "inputs.monitoring_agent == 'telegraf'" },
        value_source: { kind: "cel", params: { value: "'/metrics/' + resource.name" } },
      },
    },
    layout: {
      kind: "page",
      sections: [
        { key: "agent", title: "Agent", columns: 1, fields: ["monitoring_agent", "listen_port", "metrics_path"] },
      ],
    },
  },
  initial_values: {},
};

async function openMonitoring() {
  const user = userEvent.setup();
  renderApp(`/deployments/${idOf("web-dev-api")}`);
  await user.click(await screen.findByRole("button", { name: "Actions" }, { timeout: 5000 }));
  await user.click(
    within(await screen.findByRole("menu")).getByRole("menuitem", { name: /^Install monitoring agent/ }),
  );
  return { user, dialog: await screen.findByRole("dialog", { name: "Install monitoring agent · web-dev-api004" }) };
}

describe("a custom action's designed form", () => {
  it("asks for the survey's form while the designed one is a draft", async () => {
    await call(
      api.PUT("/admin/day2-forms/{action}", {
        params: { path: { action: "awx.install-monitoring-agent" } },
        body: { definition: monitoring },
      }),
    );
    const { dialog } = await openMonitoring();
    expect(within(dialog).getByRole("spinbutton", { name: "Port" })).toHaveValue(9100);
    expect(within(dialog).queryByRole("radio", { name: "telegraf" })).toBeNull();
  });

  it(
    "works its defaults and rules out on the VM, and the job gets the inputs they leave",
    { timeout: 15_000 },
    async () => {
      await call(
        api.PUT("/admin/day2-forms/{action}", {
          params: { path: { action: "awx.install-monitoring-agent" } },
          body: { definition: monitoring, active: true },
        }),
      );
      const { user, dialog } = await openMonitoring();
      await waitFor(() => expect(within(dialog).getByRole("radio", { name: "node_exporter" })).toBeChecked());
      expect(within(dialog).getByRole("spinbutton", { name: "Port" })).toHaveValue(9100);
      expect(within(dialog).queryByRole("textbox", { name: "Metrics path" })).toBeNull();

      await user.click(within(dialog).getByRole("radio", { name: "telegraf" }));
      expect(await within(dialog).findByRole("textbox", { name: "Metrics path" })).toHaveValue(
        "/metrics/web-dev-api004",
      );
      await waitFor(() => expect(within(dialog).getByRole("spinbutton", { name: "Port" })).toHaveValue(8125));

      await user.click(within(dialog).getByRole("button", { name: "Install monitoring agent" }));
      await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      expect(db.requests[0]!.request).toMatchObject({
        kind: "day2",
        action: { key: "awx.install-monitoring-agent" },
        inputs: { monitoring_agent: "telegraf", listen_port: 8125, metrics_path: "/metrics/web-dev-api004" },
      });
    },
  );

  it("refuses a run its schema doesn't take, at the field", { timeout: 15_000 }, async () => {
    await call(
      api.PUT("/admin/day2-forms/{action}", {
        params: { path: { action: "awx.install-monitoring-agent" } },
        body: { definition: monitoring, active: true },
      }),
    );
    const { user, dialog } = await openMonitoring();
    const port = within(dialog).getByRole("spinbutton", { name: "Port" });
    await waitFor(() => expect(port).toHaveValue(9100));
    await user.clear(port);
    await user.type(port, "80");
    await user.click(within(dialog).getByRole("button", { name: "Install monitoring agent" }));
    await waitFor(() => expect(port).toHaveAccessibleDescription(/1024/));
    expect(screen.getByRole("dialog")).toBe(dialog);
  });
});
