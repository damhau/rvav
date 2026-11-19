import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import type { Day2FormDefinition } from "@/lib/api/types";
import { renderApp } from "@/test/render";

const key = "awx.install-monitoring-agent";

/** The monitoring agent's form: its default reads the VM, and it leaves the port to AWX. */
const monitoring: Day2FormDefinition = {
  form_schema: {
    type: "object",
    required: ["monitoring_agent"],
    properties: {
      monitoring_agent: { type: "string", enum: ["node_exporter", "telegraf"] },
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
      metrics_path: {
        label: "Metrics path",
        widget: "text",
        rules: { visible: "inputs.monitoring_agent == 'telegraf'" },
        value_source: { kind: "cel", params: { value: "'/metrics/' + resource.name" } },
      },
    },
    layout: {
      kind: "page",
      sections: [{ key: "agent", title: "Agent", columns: 1, fields: ["monitoring_agent", "metrics_path"] }],
    },
  },
  initial_values: {},
};

const save = (definition: Day2FormDefinition, active?: boolean) =>
  call(api.PUT("/admin/day2-forms/{action}", { params: { path: { action: key } }, body: { definition, active } }));

async function openForm() {
  const user = userEvent.setup();
  renderApp(`/admin/day2-actions/${key}/form`);
  await screen.findByRole("heading", { name: "Install monitoring agent form" }, { timeout: 5000 });
  const fields = await screen.findByRole("region", { name: "Fields" }, { timeout: 5000 });
  return { user, fields, preview: screen.getByRole("region", { name: "Preview" }) };
}

describe("a custom action's form designer", () => {
  it("starts from the AWX survey, offers what a dialog takes, and saves a draft", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/day2-actions");
    const row = await screen.findByRole("row", { name: /Install monitoring agent/ }, { timeout: 5000 });
    expect(within(row).getByText("Survey")).toBeInTheDocument();
    await user.click(within(row).getByRole("button", { name: "Actions for Install monitoring agent" }));
    await user.click(await screen.findByRole("menuitem", { name: "Design the form" }));

    await screen.findByRole("heading", { name: "Install monitoring agent form" }, { timeout: 5000 });
    expect(await screen.findByText(/Not designed yet: this is the AWX survey/)).toBeInTheDocument();
    const fields = screen.getByRole("region", { name: "Fields" });
    await user.click(within(fields).getByRole("combobox", { name: "Layout" }));
    expect(screen.getByRole("option", { name: "Tabs" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "Wizard" })).toBeNull();
    await user.keyboard("{Escape}");
    await user.click(within(fields).getByRole("combobox", { name: "Add a field" }));
    expect(screen.getByRole("option", { name: "Text" })).toBeInTheDocument();
    expect(screen.queryByRole("option", { name: "File upload" })).toBeNull();
    expect(screen.queryByRole("option", { name: "Secret" })).toBeNull();
    await user.keyboard("{Escape}");

    const properties = screen.getByRole("region", { name: "Properties" });
    expect(within(properties).queryByRole("checkbox", { name: /Approvers may change it/ })).toBeNull();
    const label = within(properties).getByRole("textbox", { name: "Label" });
    await user.type(label, " to install");
    expect(
      await screen.findByText(/Saved as a draft: people get the AWX survey until you turn this form on/, undefined, {
        timeout: 5000,
      }),
    ).toBeInTheDocument();
    expect(screen.getByText("Draft")).toBeInTheDocument();

    await user.click(within(screen.getByRole("navigation", { name: "Breadcrumb" })).getByRole("link"));
    const listed = await screen.findByRole("row", { name: /Install monitoring agent/ }, { timeout: 5000 });
    expect(await within(listed).findByText("A designed form is a draft")).toBeInTheDocument();
  });

  it("previews the form on a VM, its defaults and sources read it", { timeout: 20_000 }, async () => {
    await save(monitoring);
    const { user, preview } = await openForm();
    await user.click(within(preview).getByRole("combobox", { name: "Deployment" }));
    await user.click(await screen.findByRole("option", { name: /^web-dev-api\b/ }));
    await user.click(within(preview).getByRole("combobox", { name: "Runs on" }));
    await user.click(await screen.findByRole("option", { name: "web-dev-api004" }));
    await waitFor(() => expect(within(preview).getByRole("radio", { name: "node_exporter" })).toBeChecked(), {
      timeout: 5000,
    });
    expect(within(preview).queryByRole("textbox", { name: "Metrics path" })).toBeNull();
    await user.click(within(preview).getByRole("radio", { name: "telegraf" }));
    expect(await within(preview).findByRole("textbox", { name: "Metrics path" }, { timeout: 5000 })).toHaveValue(
      "/metrics/web-dev-api004",
    );
    expect(within(preview).getByRole("status")).toHaveTextContent("A run with these inputs would pass.");

    const differ = screen.getByRole("region", { name: "Where the form and the survey differ" });
    expect(differ).toHaveTextContent(/doesn't ask for listen_port, which the survey requires: AWX may refuse the run/);
  });

  it("turns the form on and off, and not while a run couldn't use it", { timeout: 20_000 }, async () => {
    await save({
      ...monitoring,
      ui_schema: {
        ...monitoring.ui_schema,
        fields: { ...monitoring.ui_schema.fields, metrics_path: { label: "Metrics path", widget: "search" } },
      },
    });
    const { user, fields } = await openForm();
    await user.click(screen.getByRole("button", { name: "Turn on" }));
    expect(await screen.findByText("Not turned on")).toBeInTheDocument();
    expect(await within(fields).findByRole("img", { name: "Metrics path has problems" })).toBeInTheDocument();
    await user.click(within(fields).getByRole("button", { name: /^Metrics path/ }));
    expect(
      within(screen.getByRole("region", { name: "Properties" })).getByText(
        "A search field finds its choices in a value source: give it one.",
      ),
    ).toBeInTheDocument();
    expect(screen.getByText("Draft")).toBeInTheDocument();

    await user.click(
      within(screen.getByRole("region", { name: "Properties" })).getByRole("combobox", { name: "Widget" }),
    );
    await user.click(await screen.findByRole("option", { name: "Text" }));
    const turnOn = screen.getByRole("button", { name: "Turn on" });
    await waitFor(() => expect(turnOn).toBeEnabled(), { timeout: 5000 });
    await user.click(turnOn);
    expect(
      await screen.findByText("Install monitoring agent asks for this form from its next run"),
    ).toBeInTheDocument();
    expect(screen.getByText("On")).toBeInTheDocument();
    expect(screen.getByText("Saved: people get this form from their next run.")).toBeInTheDocument();
    expect(within(fields).queryByRole("img", { name: "Metrics path has problems" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Turn off" }));
    expect(await screen.findByText("Install monitoring agent asks for the AWX survey again")).toBeInTheDocument();
  });

  it("drops the form, and the action asks for its survey's again", { timeout: 20_000 }, async () => {
    await save(monitoring, true);
    const { user } = await openForm();
    await user.click(screen.getByRole("button", { name: "More actions for Install monitoring agent form" }));
    await user.click(await screen.findByRole("menuitem", { name: "Drop the form" }));
    const dialog = await screen.findByRole("alertdialog", { name: "Drop the Install monitoring agent form?" });
    await user.click(within(dialog).getByRole("button", { name: "Drop the form" }));
    expect(
      await screen.findByText(/Not designed yet: this is the AWX survey/, undefined, { timeout: 5000 }),
    ).toBeInTheDocument();
    expect(screen.getByText("Not designed")).toBeInTheDocument();
  });

  it(
    "designs a workflow action's form from its input schema, on the whole deployment",
    { timeout: 20_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/day2-actions");
      const table = await screen.findByRole("table", { name: "Workflow day-2 actions" }, { timeout: 5000 });
      const row = await within(table).findByRole("row", { name: /Decommission/ });
      expect(within(row).getByText("Input schema")).toBeInTheDocument();
      await user.click(within(row).getByRole("button", { name: "Actions for Decommission" }));
      await user.click(await screen.findByRole("menuitem", { name: "Design the form" }));
      await screen.findByRole("heading", { name: "Decommission form" }, { timeout: 5000 });
      expect(await screen.findByText(/Not designed yet: this is the input schema's form/)).toBeInTheDocument();
      const preview = screen.getByRole("region", { name: "Preview" });
      expect(within(preview).queryByRole("combobox", { name: "Runs on" })).toBeNull();
      expect(
        await within(preview).findByRole("textbox", { name: "Application" }, { timeout: 5000 }),
      ).toBeInTheDocument();
    },
  );
});
