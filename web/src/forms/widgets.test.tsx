import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { HttpResponse, http } from "msw";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import type { FieldValuesQuery, Option, RequestForm, UiField } from "@/lib/api/types";
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

describe("dual lists", () => {
  const owners = (initial = {}) =>
    formOf(
      {
        owners: {
          ui: {
            widget: "dual_list",
            label: "Owners",
            options: [
              { value: "lmartin", label: "Léa Martin" },
              { value: "tnguyen", label: "Thanh Nguyen" },
              { value: "jroux", label: "Jean Roux" },
            ],
          },
          schema: { type: "array", uniqueItems: true, items: { type: "string" } },
        },
      },
      initial,
    );

  it("move options to the chosen list and order them, by keyboard", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness form={owners()} />);
    const available = screen.getByRole("listbox", { name: "Owners Available" });
    available.focus();
    await user.keyboard("{ArrowDown}{Enter}");
    expect(values()).toEqual({ owners: ["tnguyen"] });
    await user.keyboard("{Home}{Enter}");
    expect(values()).toEqual({ owners: ["tnguyen", "lmartin"] });
    expect(screen.getByRole("status")).toHaveTextContent("Added Léa Martin: 2 of 2.");
    const chosen = screen.getByRole("listbox", { name: "Owners Chosen, in order" });
    chosen.focus();
    await user.keyboard("{End}{Alt>}{ArrowUp}{/Alt}");
    expect(values()).toEqual({ owners: ["lmartin", "tnguyen"] });
    expect(chosen).toHaveAttribute("aria-activedescendant", `${chosen.id}-0`);
    await user.keyboard("{ArrowDown}{Enter}");
    expect(values()).toEqual({ owners: ["lmartin"] });
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toEqual([
      "Thanh Nguyen",
      "Jean Roux",
      "Léa Martin",
    ]);
  });

  it("do the same with buttons, and keep the order they were given", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness form={owners({ owners: ["jroux", "lmartin"] })} />);
    expect(
      within(screen.getByRole("listbox", { name: "Owners Chosen, in order" }))
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["Jean Roux", "Léa Martin"]);
    await user.click(screen.getByRole("button", { name: "Move down" }));
    expect(values()).toEqual({ owners: ["lmartin", "jroux"] });
    await user.click(screen.getByRole("button", { name: "Add" }));
    expect(values()).toEqual({ owners: ["lmartin", "jroux", "tnguyen"] });
    expect(screen.getByRole("button", { name: "Add" })).toBeDisabled();
    expect(screen.getByText("All chosen")).toBeInTheDocument();
  });

  it("take their options from a value source, and drop the picks it no longer offers", async () => {
    server.use(
      http.post("*/api/v1/forms/values", async ({ request }) => {
        const q = (await request.json()) as FieldValuesQuery;
        return HttpResponse.json({ field: q.field, options: [{ value: "gva-a", label: "GVA-A" }] });
      }),
    );
    renderWithQuery(
      <Harness
        form={formOf(
          {
            zones: {
              ui: {
                widget: "dual_list",
                label: "Zones",
                value_source: { kind: "rest", params: { url: "https://cmdb.corp.example/zones" } },
              },
              schema: { type: "array", items: { type: "string" } },
            },
          },
          { zones: ["gva-b", "gva-a"] },
        )}
      />,
    );
    await waitFor(() => expect(values()).toEqual({ zones: ["gva-a"] }));
    expect(screen.getByRole("option", { name: "GVA-A" })).toBeInTheDocument();
    expect(screen.getByText("All chosen")).toBeInTheDocument();
  });
});

describe("groups of fields", () => {
  it("edit one object, each sub-field under its own label", async () => {
    const user = userEvent.setup();
    renderWithQuery(
      <Harness
        form={formOf({
          backup: {
            ui: {
              widget: "object",
              label: "Backup",
              columns: [
                { key: "policy", label: "Policy", widget: "select", options: [{ value: "daily", label: "Daily" }] },
                { key: "keep_days", label: "Keep (days)", widget: "number" },
                { key: "offsite", label: "Copy off site", widget: "toggle" },
                { key: "note", label: "Note", widget: "text" },
              ],
            },
            schema: {
              type: "object",
              required: ["policy"],
              properties: {
                policy: { type: "string", enum: ["daily"] },
                keep_days: { type: "integer" },
                offsite: { type: "boolean" },
                note: { type: "string" },
              },
            },
          },
        })}
      />,
    );
    expect(within(screen.getByRole("group", { name: "Backup" })).getAllByText("Optional")).toHaveLength(2);
    await user.type(screen.getByLabelText("Keep (days)"), "14");
    await user.click(screen.getByRole("checkbox", { name: "Copy off site" }));
    await user.type(screen.getByLabelText("Note"), "x");
    expect(values()).toEqual({ backup: { keep_days: 14, offsite: true, note: "x" } });
    await user.clear(screen.getByLabelText("Note"));
    await user.click(screen.getByRole("checkbox", { name: "Copy off site" }));
    await user.clear(screen.getByLabelText("Keep (days)"));
    expect(values()).toEqual({});
  });
});

describe("images and links", () => {
  it("show a picture with its text alternative and caption, and a link that opens in a new tab", () => {
    renderWithQuery(
      <Harness
        form={formOf({
          diagram: {
            ui: {
              widget: "image",
              label: "Diagram",
              url: "https://wiki.corp.example/net.png",
              alt: "The three network zones",
              help: "Where each tier runs.",
            },
            schema: {},
          },
          sizes: {
            ui: {
              widget: "link",
              label: "VM sizes explained",
              url: "https://wiki.corp.example/sizes",
              help: "On the wiki.",
            },
            schema: {},
          },
        })}
      />,
    );
    const img = screen.getByRole("img", { name: "The three network zones" });
    expect(img).toHaveAttribute("src", "https://wiki.corp.example/net.png");
    expect(img.closest("figure")?.querySelector("figcaption")).toHaveTextContent("Where each tier runs.");
    const link = screen.getByRole("link", { name: "VM sizes explained (opens in a new tab)" });
    expect(link).toHaveAttribute("href", "https://wiki.corp.example/sizes");
    expect(link).toHaveAttribute("target", "_blank");
    expect(link).toHaveAttribute("rel", "noopener noreferrer");
    expect(screen.getByText("On the wiki.")).toBeInTheDocument();
    expect(screen.queryByText("Optional")).not.toBeInTheDocument();
  });

  it("never use an address that isn't http(s) or this site's", () => {
    renderWithQuery(
      <Harness
        form={formOf({
          bad: { ui: { widget: "link", label: "Click me", url: "javascript:alert(1)" }, schema: {} },
          other: { ui: { widget: "link", label: "Elsewhere", url: "//evil.example/" }, schema: {} },
          here: { ui: { widget: "link", label: "My requests", url: "/requests" }, schema: {} },
          pic: { ui: { widget: "image", label: "Pic", url: "data:image/svg+xml,<svg/>", alt: "A dot" }, schema: {} },
        })}
      />,
    );
    expect(screen.getAllByRole("link").map((a) => a.getAttribute("href"))).toEqual(["/requests"]);
    expect(screen.getByText("Click me")).toBeInTheDocument();
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
    expect(screen.getByText("No image yet: A dot")).toBeInTheDocument();
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

describe("search fields", () => {
  const apps = [
    { value: "app-001", label: "Payroll", meta: "Finance" },
    { value: "app-002", label: "Intranet", meta: "HR" },
    { value: "app-003", label: "Pay slips", meta: "HR" },
  ];
  const app = (initial = {}) =>
    formOf(
      {
        app: {
          ui: {
            widget: "search",
            label: "Application",
            value_source: { kind: "rest", search: true, params: { url: "https://cmdb.corp.example/apps?q=${search}" } },
          },
          schema: { type: "string" },
        },
      },
      initial,
    );
  /** The CMDB: finds by value or label; fails as many times as asked first. */
  function cmdb(fail = 0, list = apps) {
    const asked: (string | undefined)[] = [];
    let failing = fail;
    server.use(
      http.post("*/api/v1/forms/values", async ({ request }) => {
        const q = (await request.json()) as FieldValuesQuery;
        asked.push(q.search);
        if (failing-- > 0)
          return HttpResponse.json(
            { type: "about:blank", title: "Something went wrong on our side", status: 502, request_id: "r1" },
            { status: 502 },
          );
        const text = (q.search ?? "").toLowerCase();
        return HttpResponse.json({
          field: q.field,
          options: list.filter((a) => a.value === q.search || a.label.toLowerCase().includes(text)),
        });
      }),
    );
    return asked;
  }

  it("find choices with what is typed, picked with the keyboard", async () => {
    const user = userEvent.setup();
    const asked = cmdb();
    renderWithQuery(<Harness form={app()} />);
    const box = screen.getByRole("combobox", { name: "Application" });
    expect(box).toHaveAttribute("placeholder", "Type to search");
    await user.type(box, "pay");
    expect(await screen.findByText("2 matches: use the arrow keys, then Enter.")).toBeInTheDocument();
    expect(screen.getByRole("listbox", { name: "Application: matches" })).toBeVisible();
    await user.keyboard("{ArrowDown}{Enter}");
    expect(values()).toEqual({ app: "app-003" });
    expect(box).toHaveValue("Pay slips");
    expect(box).toHaveAccessibleDescription(/HR/);
    expect(asked).not.toContain("p");
    expect(asked).toContain("pay");

    await user.clear(box);
    await user.type(box, "zz");
    expect(await screen.findByText("No matches.")).toBeInTheDocument();
    expect(box).toHaveAttribute("aria-expanded", "false");
    expect(values()).toEqual({});
  });

  it("list the first 20 of more", async () => {
    const user = userEvent.setup();
    cmdb(
      0,
      Array.from({ length: 25 }, (_, i) => ({ value: `app-${i}`, label: `App ${i}`, meta: "" })),
    );
    renderWithQuery(<Harness form={app()} />);
    await user.type(screen.getByRole("combobox", { name: "Application" }), "app");
    expect(
      await screen.findByText("25 found, the first 20 listed: type more to narrow them down."),
    ).toBeInTheDocument();
    expect(screen.getAllByRole("option")).toHaveLength(20);
  });

  it("say a search that failed in place, and Try again searches again", async () => {
    const user = userEvent.setup();
    cmdb(1);
    renderWithQuery(<Harness form={app()} />);
    const box = screen.getByRole("combobox", { name: "Application" });
    await user.type(box, "intra");
    await waitFor(() => expect(box).toHaveAccessibleDescription(/Couldn't search\./));
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("option", { name: /Intranet/ })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
  });

  it("name the choice a form was given", async () => {
    cmdb();
    renderWithQuery(<Harness form={app({ app: "app-001" })} />);
    expect(await screen.findByDisplayValue("Payroll")).toHaveAccessibleDescription(/Finance/);
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

describe("a value source that fails", () => {
  /** Answers with these options per field; each field in `failing` fails once first. */
  function source(answers: Record<string, { options: Option[]; default?: string }>, failing: string[]) {
    const left = new Set(failing);
    const asked: string[] = [];
    server.use(
      http.post("*/api/v1/forms/values", async ({ request }) => {
        const q = (await request.json()) as FieldValuesQuery;
        asked.push(q.field);
        if (left.delete(q.field))
          return HttpResponse.json(
            { type: "about:blank", title: "Something went wrong on our side", status: 500, request_id: "r1" },
            { status: 500 },
          );
        return HttpResponse.json({ field: q.field, ...answers[q.field] });
      }),
    );
    return asked;
  }
  const clusters = { options: [{ value: "GVA-DEV-CL01", label: "GVA-DEV-CL01" }], default: "GVA-DEV-CL01" };
  const cluster = (initial = {}) =>
    formOf(
      {
        cluster: {
          ui: {
            widget: "vsphere_cluster",
            label: "Cluster",
            value_source: { kind: "builtin", name: "vsphere.clusters" },
          },
          schema: { type: "string" },
        },
      },
      initial,
    );

  it("leaves a select empty, says so with Try again, and Try again fills it", async () => {
    const user = userEvent.setup();
    source({ cluster: clusters }, ["cluster"]);
    renderWithQuery(<Harness form={cluster()} />);
    const select = screen.getByRole("combobox", { name: "Cluster" });
    await waitFor(() => expect(select).toHaveAccessibleDescription("Couldn't load the options."));
    expect(select).toHaveTextContent("Not loaded");
    expect(select).toBeDisabled();
    expect(values()).toEqual({});

    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(select).toHaveTextContent("GVA-DEV-CL01"));
    expect(values()).toEqual({ cluster: "GVA-DEV-CL01" });
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
    await waitFor(() => expect(select).toHaveFocus());
  });

  it("shows the value a select holds while its options fail, and settles it once they load", async () => {
    const user = userEvent.setup();
    source({ cluster: clusters }, ["cluster"]);
    renderWithQuery(<Harness form={cluster({ cluster: "GVA-OLD-CL09" })} />);
    const select = screen.getByRole("combobox", { name: "Cluster" });
    await waitFor(() => expect(select).toHaveAccessibleDescription("Couldn't load the options."));
    expect(select).toHaveTextContent("GVA-OLD-CL09");
    expect(values()).toEqual({ cluster: "GVA-OLD-CL09" });

    // No longer offered: the server's default takes its place.
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(values()).toEqual({ cluster: "GVA-DEV-CL01" }));
    expect(select).toHaveTextContent("GVA-DEV-CL01");
  });

  it("drops a value its options no longer offer when there is no default", async () => {
    source({ cluster: { options: [{ value: "GVA-DEV-CL01", label: "GVA-DEV-CL01" }] } }, []);
    renderWithQuery(<Harness form={cluster({ cluster: "GVA-OLD-CL09" })} />);
    await waitFor(() => expect(values()).toEqual({}));
    expect(screen.getByRole("combobox", { name: "Cluster" })).toHaveTextContent("Select…");
  });

  it("lists the picks a multi-select holds while its options fail", async () => {
    const user = userEvent.setup();
    source(
      {
        zones: {
          options: [
            { value: "gva-a", label: "GVA-A" },
            { value: "gva-b", label: "GVA-B" },
          ],
        },
      },
      ["zones"],
    );
    renderWithQuery(
      <Harness
        form={formOf(
          {
            zones: {
              ui: {
                widget: "multiselect",
                label: "Zones",
                value_source: { kind: "rest", params: { url: "https://cmdb.corp.example/zones" } },
              },
              schema: { type: "array", items: { type: "string" } },
            },
          },
          { zones: ["gva-a", "zrh-a"] },
        )}
      />,
    );
    expect(await screen.findByText("Couldn't load the options.")).toBeInTheDocument();
    expect(screen.getByRole("checkbox", { name: "gva-a" })).toBeChecked();
    expect(screen.getByRole("checkbox", { name: "zrh-a" })).toBeChecked();

    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("checkbox", { name: "GVA-A" })).toBeChecked();
    await waitFor(() => expect(values()).toEqual({ zones: ["gva-a"] }));
    expect(screen.queryByText("Couldn't load the options.")).not.toBeInTheDocument();
  });

  it("says a record picker couldn't load its records, and Try again loads them", async () => {
    const user = userEvent.setup();
    source({ deployment: { options: [{ value: "dep_web_app", label: "web-dev-app001" }] } }, ["deployment"]);
    renderWithQuery(
      <Harness
        form={formOf({ deployment: { ui: { widget: "deployment", label: "Web server" }, schema: { type: "string" } } })}
      />,
    );
    const select = screen.getByRole("combobox", { name: "Web server" });
    await waitFor(() => expect(select).toHaveAccessibleDescription("Couldn't load the options."));
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(select).toBeEnabled());
    expect(select).toHaveTextContent("Select…");
  });

  it("says a person given another way couldn't be looked up, and Try again names them", async () => {
    const user = userEvent.setup();
    source({ owner: { options: [{ value: "usr_lmartin", label: "Léa Martin", meta: "lea.martin@corp.example" }] } }, [
      "owner",
    ]);
    renderWithQuery(
      <Harness
        form={formOf(
          { owner: { ui: { widget: "user", label: "Technical owner" }, schema: { type: "string" } } },
          { owner: "usr_lmartin" },
        )}
      />,
    );
    const box = screen.getByRole("combobox", { name: "Technical owner" });
    await waitFor(() => expect(box).toHaveAccessibleDescription(/Couldn't look up this person\./));
    expect(box).toHaveValue("usr_lmartin");
    await user.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(box).toHaveValue("Léa Martin"));
    expect(values()).toEqual({ owner: "usr_lmartin" });
  });
});
