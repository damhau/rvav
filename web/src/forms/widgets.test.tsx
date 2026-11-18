import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import type { RequestForm, UiField } from "@/lib/api/types";
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
