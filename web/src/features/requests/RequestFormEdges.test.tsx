import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { delay, http, HttpResponse } from "msw";
import { describe, expect, it } from "vitest";
import type { UiSchema } from "@/lib/api/types";
import { latency } from "@/mocks/handlers";
import { server } from "@/mocks/node";
import { db } from "@/mocks/seed";
import { renderApp } from "@/test/render";

/** The database user item with a page of more fields: a toggle that shows notes, a lock, a secret. */
function form(kind: UiSchema["layout"]["kind"], extra: Partial<UiSchema> = {}) {
  const f = db.forms.itm_dbuser!;
  f.schema = {
    type: "object",
    properties: {
      database: { type: "string", enum: ["ledger", "checkout"] },
      role_name: { type: "string" },
      extra: { type: "boolean" },
      notes: { type: "string" },
      lock: { type: "boolean" },
      schema_name: { type: "string" },
      password: { type: "string" },
    },
    required: ["database", "role_name"],
  };
  f.ui_schema = {
    layout: {
      kind,
      sections: [
        { key: "user", title: "Database user", columns: 1, fields: ["database", "role_name", "password"] },
        { key: "more", title: "More", columns: 1, fields: ["extra", "notes", "lock", "schema_name"] },
      ],
    },
    fields: {
      database: {
        widget: "select",
        label: "Database",
        options: [
          { value: "ledger", label: "Ledger" },
          { value: "checkout", label: "Checkout" },
        ],
      },
      role_name: { widget: "text", label: "Role name" },
      password: { widget: "secret", label: "Password" },
      extra: { widget: "toggle", label: "Extra" },
      notes: { widget: "text", label: "Notes", rules: { visible: "inputs.extra == true" } },
      lock: { widget: "toggle", label: "Lock" },
      schema_name: {
        widget: "text",
        label: "Schema",
        rules: { default: '"fixed_schema"', read_only: "inputs.lock == true" },
      },
    },
    ...extra,
  };
  f.initial_values = { database: "ledger", extra: false, lock: false };
}

async function openForm() {
  const app = renderApp("/catalog/db-user/request");
  await screen.findByRole("heading", { name: "Request Create database user" }, { timeout: 5000 });
  await screen.findByText("No approval needed", {}, { timeout: 5000 });
  return app;
}

function storedInputs(path: string) {
  const number = path.split("/").at(-1);
  return db.requests.find((r) => r.request.number === number)!.request.inputs!;
}

describe("Request form edges", () => {
  it("keeps a secret input out of the query and mutation caches, yet sends it", async () => {
    form("page");
    const previewed: unknown[] = [];
    const listen = ({ request }: { request: Request }) => {
      if (request.url.endsWith("/requests/preview"))
        void request
          .clone()
          .json()
          .then((b) => previewed.push(b));
    };
    server.events.on("request:start", listen);
    try {
      const user = userEvent.setup();
      const { client, router } = await openForm();
      await user.type(screen.getByLabelText("Password", { selector: "input" }), "correct-horse-battery");
      await user.type(screen.getByLabelText("Role name"), "app_ro");
      const keys = () =>
        client
          .getQueryCache()
          .getAll()
          .map((q) => JSON.stringify(q.queryKey));
      await waitFor(() => expect(keys().some((k) => k.includes("secret #"))).toBe(true));
      expect(keys().filter((k) => k.includes("correct-horse"))).toEqual([]);
      await waitFor(() => expect(JSON.stringify(previewed)).toContain("correct-horse-battery"));
      await user.click(screen.getByRole("button", { name: "Submit request" }));
      await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
      const holding = () =>
        client
          .getMutationCache()
          .getAll()
          .filter((m) => JSON.stringify(m.state.variables ?? null).includes("correct-horse"));
      await waitFor(() => expect(holding()).toEqual([]));
    } finally {
      server.events.removeListener("request:start", listen);
    }
  }, 20_000);

  it("keeps a field a later preview showed while a newer one loads, after Next", async () => {
    form("wizard");
    const user = userEvent.setup();
    await openForm();
    await user.type(screen.getByLabelText("Role name"), "app_ro");
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(screen.getByRole("heading", { name: /^More/ })).toHaveFocus());
    await user.click(screen.getByRole("checkbox", { name: "Extra" }));
    const notes = await screen.findByLabelText("Notes", {}, { timeout: 3000 });
    latency.ms = 1500;
    try {
      await user.type(notes, "abc");
      await new Promise((r) => setTimeout(r, 700));
      expect(screen.getByLabelText("Notes")).toHaveFocus();
      expect(screen.getByLabelText("Notes")).toHaveValue("abc");
    } finally {
      latency.ms = 0;
    }
  }, 20_000);

  it("shows a read-only field's default, which the request gets", async () => {
    form("page");
    const user = userEvent.setup();
    const { router } = await openForm();
    await user.type(screen.getByLabelText("Role name"), "app_ro");
    const schema = screen.getByRole("textbox", { name: "Schema" });
    await waitFor(() => expect(schema).toHaveValue("fixed_schema"));
    await user.clear(schema);
    await user.type(schema, "mine");
    await user.click(screen.getByRole("checkbox", { name: "Lock" }));
    await waitFor(() => expect(schema).toHaveAttribute("readonly"), { timeout: 3000 });
    await waitFor(() => expect(schema).toHaveValue("fixed_schema"));
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
    expect(storedInputs(router.state.location.pathname).schema_name).toBe("fixed_schema");
  }, 20_000);

  it("goes past a page whose validation message a later page fixes", async () => {
    form("wizard", {
      validations: [
        {
          key: "pair",
          kind: "cel",
          params: {
            check:
              'inputs.role_name == "tmp_role" && inputs.extra != true ? {"role_name": "A temporary role needs Extra."} : {}',
          },
        },
      ],
    });
    const user = userEvent.setup();
    await openForm();
    await user.type(screen.getByLabelText("Role name"), "tmp_role");
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(screen.getByRole("heading", { name: /^More/ })).toHaveFocus());
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(screen.getByLabelText("Role name")).toHaveFocus());
    expect(screen.getByText("A temporary role needs Extra.")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Next" }));
    await waitFor(() => expect(screen.getByRole("heading", { name: /^More/ })).toHaveFocus());
    await user.click(screen.getByRole("checkbox", { name: "Extra" }));
    await user.click(screen.getByRole("button", { name: "Next" }));
    await screen.findByRole("button", { name: "Submit request" }, { timeout: 3000 });
  }, 20_000);

  it("gives a dropdown its default rule's option, not the one its source suggests", async () => {
    form("page");
    const f = db.forms.itm_dbuser!;
    (f.schema as { properties: Record<string, unknown> }).properties.datastore = { type: "string" };
    f.ui_schema.fields.datastore = {
      widget: "vsphere_datastore",
      label: "Datastore",
      value_source: { kind: "builtin", name: "vsphere.datastores", params: { cluster: "GVA-DEV-CL01" } },
      rules: { default: '"nfs-gva-bronze-01"' },
    };
    f.ui_schema.layout.sections[0]!.fields.push("datastore");
    const user = userEvent.setup();
    const { router } = await openForm();
    await user.type(screen.getByLabelText("Role name"), "app_ro");
    await waitFor(() => expect(screen.getByLabelText("Datastore")).toHaveTextContent("nfs-gva-bronze-01"));
    await new Promise((r) => setTimeout(r, 500));
    expect(screen.getByLabelText("Datastore")).toHaveTextContent("nfs-gva-bronze-01");
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
    expect(storedInputs(router.state.location.pathname).datastore).toBe("nfs-gva-bronze-01");
  }, 20_000);

  it("waits for a file still sending before it submits", async () => {
    form("page");
    const f = db.forms.itm_dbuser!;
    (f.schema as { properties: Record<string, unknown> }).properties.attachment = { type: "object" };
    f.ui_schema.fields.attachment = { widget: "file", label: "Attachment", accept: [".csv"] };
    f.ui_schema.layout.sections[0]!.fields.push("attachment");
    server.use(
      http.post("*/api/v1/forms/files", async () => {
        await delay(600);
        return HttpResponse.json(
          { id: "fil_abc", name: "rules.csv", size: 10, content_type: "text/csv", sha256: "ab" },
          { status: 201 },
        );
      }),
    );
    const user = userEvent.setup();
    const { router } = await openForm();
    await user.type(screen.getByLabelText("Role name"), "app_ro");
    await user.upload(
      document.querySelector<HTMLInputElement>('input[type="file"]')!,
      new File(["a,b\n1,2\n"], "rules.csv", { type: "text/csv" }),
    );
    expect(screen.getByText("Sending rules.csv…")).toBeInTheDocument();
    expect(screen.getByText("Wait until rules.csv is sent.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Submit request" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Save as draft" })).toBeDisabled();
    await waitFor(() => expect(screen.getByRole("button", { name: "Submit request" })).toBeEnabled(), {
      timeout: 3000,
    });
    expect(screen.queryByText("Wait until rules.csv is sent.")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Submit request" }));
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/requests\/REQ-/));
    expect(storedInputs(router.state.location.pathname)).toHaveProperty("attachment");
  }, 20_000);
});
