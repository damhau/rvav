import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import type { FieldValuesQuery, RequestForm, UiField } from "@/lib/api/types";
import { server } from "@/mocks/node";
import { renderWithQuery } from "@/test/render";
import { FormRenderer } from "./FormRenderer";

function formOf(fields: Record<string, { ui: UiField; schema: Record<string, unknown> }>, initial = {}): RequestForm {
  return {
    item_id: "itm_test",
    version_id: "ver_test",
    schema: {
      type: "object",
      properties: Object.fromEntries(Object.entries(fields).map(([k, f]) => [k, f.schema])),
    },
    ui_schema: {
      layout: { kind: "page", sections: [{ key: "main", title: "Main", columns: 1, fields: Object.keys(fields) }] },
      fields: Object.fromEntries(Object.entries(fields).map(([k, f]) => [k, f.ui])),
    },
    initial_values: initial,
  };
}

function Harness({ form }: { form: RequestForm }) {
  const [values, setValues] = useState<Record<string, unknown>>({ ...form.initial_values });
  return (
    <>
      <FormRenderer
        form={form}
        values={values}
        onChange={(name, v) => setValues((s) => ({ ...s, [name]: v }))}
        ctx={{ versionId: form.version_id, projectId: "prj_web", environment: "dev" }}
      />
      <pre data-testid="values">{JSON.stringify(values)}</pre>
    </>
  );
}

const values = () => JSON.parse(screen.getByTestId("values").textContent ?? "{}") as Record<string, unknown>;

describe("secret fields", () => {
  it("are masked until their button shows them, by keyboard too", async () => {
    const user = userEvent.setup();
    renderWithQuery(
      <Harness
        form={formOf({
          password: { ui: { widget: "secret", label: "Password" }, schema: { type: "string" } },
          token: { ui: { widget: "text", label: "Token", secret: true }, schema: { type: "string" } },
        })}
      />,
    );
    const password = screen.getByLabelText("Password");
    expect(password).toHaveAttribute("type", "password");
    expect(password).toHaveAttribute("autocomplete", "new-password");
    expect(screen.getByLabelText("Token")).toHaveAttribute("type", "password");

    await user.type(password, "Hunt3r-2");
    const show = screen.getByRole("button", { name: "Show Password" });
    expect(show).toHaveAttribute("aria-pressed", "false");
    await user.tab();
    expect(show).toHaveFocus();
    await user.keyboard("{Enter}");
    expect(show).toHaveAttribute("aria-pressed", "true");
    expect(password).toHaveAttribute("type", "text");
    await user.keyboard(" ");
    expect(password).toHaveAttribute("type", "password");
    expect(values()).toEqual({ password: "Hunt3r-2" });
  });
});

describe("dates and times", () => {
  it("take a date between the field's bounds, as YYYY-MM-DD", async () => {
    const user = userEvent.setup();
    renderWithQuery(
      <Harness
        form={formOf({
          day: {
            ui: { widget: "date", label: "Cut-over day" },
            schema: { type: "string", format: "date", formatMinimum: "2026-12-01", formatMaximum: "2026-12-31" },
          },
        })}
      />,
    );
    const day = screen.getByLabelText("Cut-over day");
    expect(day).toHaveAttribute("type", "date");
    expect(day).toHaveAttribute("min", "2026-12-01");
    expect(day).toHaveAttribute("max", "2026-12-31");
    await user.type(day, "2026-12-24");
    expect(values()).toEqual({ day: "2026-12-24" });
    await user.clear(day);
    expect(values()).toEqual({});
  });

  it("keep a time with the browser's offset, and say which zone it is in", () => {
    renderWithQuery(
      <Harness
        form={formOf(
          {
            when: {
              ui: { widget: "datetime", label: "Maintenance window" },
              schema: { type: "string", format: "date-time", formatMinimum: "2026-12-01T00:00:00Z" },
            },
          },
          { when: "2026-12-10T13:30:00Z" },
        )}
      />,
    );
    const when = screen.getByLabelText("Maintenance window");
    expect(when).toHaveAttribute("type", "datetime-local");
    // Shown in local time: the same moment as the stored value.
    expect(new Date((when as HTMLInputElement).value).getTime()).toBe(Date.parse("2026-12-10T13:30:00Z"));
    expect(new Date(when.getAttribute("min")!).getTime()).toBe(Date.parse("2026-12-01T00:00:00Z"));
    expect(when).toHaveAccessibleDescription(/\(UTC[+-]\d\d:\d\d\)/);

    fireEvent.change(when, { target: { value: "2026-12-11T08:15" } });
    const stored = values().when as string;
    expect(stored).toMatch(/^2026-12-11T08:15:00[+-]\d\d:\d\d$/);
    expect(Date.parse(stored)).toBe(new Date("2026-12-11T08:15").getTime());
  });
});

describe("numbers", () => {
  it("take decimals in the field's steps when it allows them", async () => {
    const user = userEvent.setup();
    renderWithQuery(
      <Harness
        form={formOf({
          ratio: {
            ui: { widget: "number", label: "CPU ratio" },
            schema: { type: "number", minimum: 0.5, multipleOf: 0.25 },
          },
          count: { ui: { widget: "number", label: "Count" }, schema: { type: "integer" } },
        })}
      />,
    );
    const ratio = screen.getByLabelText("CPU ratio");
    expect(ratio).toHaveAttribute("step", "0.25");
    expect(ratio).toHaveAttribute("inputmode", "decimal");
    expect(screen.getByLabelText("Count")).toHaveAttribute("inputmode", "numeric");
    await user.type(ratio, "1.75");
    expect(values()).toEqual({ ratio: 1.75 });
  });
});

describe("tags", () => {
  it("are typed as chips and removed, by keyboard", async () => {
    const user = userEvent.setup();
    renderWithQuery(
      <Harness
        form={formOf({
          tags: {
            ui: { widget: "tags", label: "vSphere tags", options: [{ value: "backup/daily", label: "Daily backup" }] },
            schema: { type: "array", uniqueItems: true, maxItems: 3, items: { type: "string" } },
          },
        })}
      />,
    );
    const input = screen.getByLabelText("vSphere tags");
    expect(input).toHaveAttribute("list", "f-tags-suggestions");
    await user.type(input, "backup/daily{Enter}tier/gold,");
    expect(values()).toEqual({ tags: ["backup/daily", "tier/gold"] });
    await user.type(input, "tier/gold{Enter}");
    expect(screen.getByRole("status")).toHaveTextContent("tier/gold is already there.");
    expect(values()).toEqual({ tags: ["backup/daily", "tier/gold"] });
    await user.clear(input);
    await user.keyboard("{Backspace}");
    expect(values()).toEqual({ tags: ["backup/daily"] });
    await user.click(screen.getByRole("button", { name: "Remove backup/daily" }));
    expect(values()).toEqual({});
    expect(input).toHaveFocus();
  });
});

describe("key and value pairs", () => {
  it("are added, edited and removed, and a key can't be given twice", async () => {
    const user = userEvent.setup();
    renderWithQuery(
      <Harness
        form={formOf(
          {
            labels: {
              ui: { widget: "key_value", label: "Labels" },
              schema: { type: "object", additionalProperties: { type: "string" } },
            },
          },
          { labels: { app: "billing" } },
        )}
      />,
    );
    expect(screen.getByRole("group", { name: "Labels" })).toBeInTheDocument();
    expect(screen.getByLabelText("Key 1")).toHaveValue("app");
    await user.click(screen.getByRole("button", { name: "Add a pair" }));
    expect(screen.getByLabelText("Key 2")).toHaveFocus();
    await user.keyboard("app");
    await user.type(screen.getByLabelText("Value 2"), "ops");
    expect(screen.getByLabelText("Key 2")).toHaveAccessibleDescription(/app is already a key above/);
    expect(values()).toEqual({ labels: { app: "billing" } });
    await user.type(screen.getByLabelText("Key 2"), "-owner");
    expect(values()).toEqual({ labels: { app: "billing", "app-owner": "ops" } });
    await user.click(screen.getByRole("button", { name: "Remove pair 1" }));
    expect(values()).toEqual({ labels: { "app-owner": "ops" } });
    expect(screen.getByLabelText("Key 1")).toHaveFocus();
  });
});

describe("tables", () => {
  const disks = () =>
    formOf({
      disks: {
        ui: {
          widget: "datagrid",
          label: "Extra disks",
          columns: [
            { key: "size_gb", label: "Size (GB)", widget: "number" },
            { key: "tier", label: "Tier", widget: "select", options: [{ value: "gold", label: "Gold" }] },
            { key: "thin", label: "Thin", widget: "toggle" },
            { key: "mount", label: "Mount point", widget: "text" },
          ],
        },
        schema: {
          type: "array",
          minItems: 1,
          maxItems: 2,
          items: {
            type: "object",
            properties: {
              size_gb: { type: "integer" },
              tier: { type: "string", enum: ["gold"] },
              thin: { type: "boolean" },
              mount: { type: "string" },
            },
          },
        },
      },
    });

  it("add rows up to the most, and remove them down to the least", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness form={disks()} />);
    expect(screen.getByText("1 to 2 rows")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Add a row" }));
    expect(screen.getByLabelText("Size (GB), row 1")).toHaveFocus();
    await user.keyboard("50");
    await user.click(screen.getByRole("checkbox", { name: "Thin, row 1" }));
    await user.type(screen.getByLabelText("Mount point, row 1"), "/data");
    expect(values()).toEqual({ disks: [{ size_gb: 50, thin: true, mount: "/data" }] });
    expect(screen.getByRole("button", { name: "Remove row 1" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Add a row" }));
    expect(screen.getByRole("button", { name: "Add a row" })).toBeDisabled();
    await user.type(screen.getByLabelText("Size (GB), row 2"), "20");
    await user.click(screen.getByRole("button", { name: "Remove row 1" }));
    expect(values()).toEqual({ disks: [{ size_gb: 20 }] });
    expect(screen.getByLabelText("Size (GB), row 1")).toHaveFocus();
    expect(screen.getByRole("columnheader", { name: "Tier" })).toBeInTheDocument();
  });
});

describe("multi-select from a value source", () => {
  it("offers the source's options, and drops the picks a dependency change takes away", async () => {
    const user = userEvent.setup();
    server.use(
      http.post("*/api/v1/forms/values", async ({ request }) => {
        const q = (await request.json()) as FieldValuesQuery;
        const zones = q.inputs.site === "zrh" ? ["zrh-a"] : ["gva-a", "gva-b"];
        return HttpResponse.json({ field: q.field, options: zones.map((z) => ({ value: z, label: z.toUpperCase() })) });
      }),
    );
    renderWithQuery(
      <Harness
        form={formOf(
          {
            site: {
              ui: {
                widget: "segmented",
                label: "Site",
                options: [
                  { value: "gva", label: "Geneva" },
                  { value: "zrh", label: "Zurich" },
                ],
              },
              schema: { type: "string" },
            },
            zones: {
              ui: {
                widget: "multiselect",
                label: "Zones",
                depends_on: ["site"],
                value_source: { kind: "rest", params: { url: "https://cmdb.corp.example/zones?site=${inputs.site}" } },
              },
              schema: { type: "array", items: { type: "string" } },
            },
          },
          { site: "gva" },
        )}
      />,
    );
    await user.click(await screen.findByRole("checkbox", { name: "GVA-A" }));
    await user.click(screen.getByRole("checkbox", { name: "GVA-B" }));
    expect(values()).toEqual({ site: "gva", zones: ["gva-a", "gva-b"] });
    await user.click(screen.getByRole("radio", { name: "Zurich" }));
    expect(await screen.findByRole("checkbox", { name: "ZRH-A" })).not.toBeChecked();
    await waitFor(() => expect(values()).toEqual({ site: "zrh" }));
  });
});

describe("people", () => {
  const people = [
    { value: "usr_lmartin", label: "Léa Martin", meta: "lea.martin@corp.example" },
    { value: "usr_mkeller", label: "Marc Keller", meta: "marc.keller@corp.example" },
  ];
  const owner = (initial = {}) =>
    formOf({ owner: { ui: { widget: "user", label: "Technical owner" }, schema: { type: "string" } } }, initial);

  it("are searched by name and picked with the keyboard", async () => {
    const user = userEvent.setup();
    const asked: (string | undefined)[] = [];
    server.use(
      http.post("*/api/v1/forms/values", async ({ request }) => {
        const q = (await request.json()) as FieldValuesQuery;
        asked.push(q.search);
        const text = (q.search ?? "").toLowerCase();
        return HttpResponse.json({
          field: q.field,
          options: people.filter((p) => p.value === q.search || p.label.toLowerCase().includes(text)),
        });
      }),
    );
    renderWithQuery(<Harness form={owner()} />);
    const box = screen.getByRole("combobox", { name: "Technical owner" });
    await user.type(box, "ma");
    expect(await screen.findByText("2 people found: use the arrow keys, then Enter.")).toBeInTheDocument();
    expect(box).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("option", { name: /Léa Martin/ })).toHaveAttribute("aria-selected", "true");
    await user.keyboard("{ArrowDown}");
    expect(box).toHaveAttribute("aria-activedescendant", "f-owner-people-1");
    await user.keyboard("{Enter}");
    expect(values()).toEqual({ owner: "usr_mkeller" });
    expect(box).toHaveValue("Marc Keller");
    expect(box).toHaveAccessibleDescription(/marc.keller@corp.example/);
    expect(box).toHaveAttribute("aria-expanded", "false");
    await user.type(box, "x");
    expect(values()).toEqual({});
    expect(asked).not.toContain("m");
  });

  it("name the person a form was given", async () => {
    server.use(
      http.post("*/api/v1/forms/values", async ({ request }) => {
        const q = (await request.json()) as FieldValuesQuery;
        return HttpResponse.json({ field: q.field, options: people.filter((p) => p.value === q.search) });
      }),
    );
    renderWithQuery(<Harness form={owner({ owner: "usr_lmartin" })} />);
    expect(await screen.findByDisplayValue("Léa Martin")).toHaveAccessibleDescription(/lea.martin@corp.example/);
  });
});

describe("rvav's own records", () => {
  it("are offered by a deployment field that names no source", async () => {
    const asked: FieldValuesQuery[] = [];
    server.use(
      http.post("*/api/v1/forms/values", async ({ request }) => {
        const q = (await request.json()) as FieldValuesQuery;
        asked.push(q);
        return HttpResponse.json({ field: q.field, options: [{ value: "dep_web_app", label: "web-dev-app001" }] });
      }),
    );
    renderWithQuery(
      <Harness
        form={formOf({ deployment: { ui: { widget: "deployment", label: "Web server" }, schema: { type: "string" } } })}
      />,
    );
    await waitFor(() => expect(asked.map((q) => q.field)).toEqual(["deployment"]));
    expect(screen.getByRole("combobox", { name: "Web server" })).toBeEnabled();
  });
});
