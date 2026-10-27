import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import type { FieldStates, RequestForm } from "@/lib/api/types";
import { db } from "@/mocks/seed";
import { renderWithQuery } from "@/test/render";
import { mapFieldErrors } from "./errors";
import { FormRenderer } from "./FormRenderer";

function Harness({
  form,
  errors,
  fieldStates,
  initialEnv = "dev",
}: {
  form: RequestForm;
  errors?: Record<string, string>;
  fieldStates?: FieldStates;
  initialEnv?: string;
}) {
  const [values, setValues] = useState<Record<string, unknown>>({ ...form.initial_values });
  const [env, setEnv] = useState(initialEnv);
  return (
    <>
      <button type="button" onClick={() => setEnv("prd")}>
        Switch to prd
      </button>
      <FormRenderer
        form={form}
        values={values}
        onChange={(name, v) => setValues((s) => ({ ...s, [name]: v }))}
        errors={errors}
        fieldStates={fieldStates}
        ctx={{ versionId: form.version_id, projectId: "prj_web", environment: env }}
      />
      <pre data-testid="values">{JSON.stringify(values)}</pre>
    </>
  );
}

const rhelForm = () => structuredClone(db.forms.itm_rhel9!);
const values = () => JSON.parse(screen.getByTestId("values").textContent ?? "{}") as Record<string, unknown>;

describe("FormRenderer", () => {
  it("renders sections and fields from the UI schema", () => {
    renderWithQuery(<Harness form={rhelForm()} />);
    for (const title of ["Basics", "Size", "Placement and network", "Configuration after boot"]) {
      expect(screen.getByRole("group", { name: title })).toBeInTheDocument();
    }
    expect(screen.getByLabelText("Role")).toHaveValue("app");
    expect(screen.getByRole("radio", { name: /Medium/ })).toHaveAttribute("aria-checked", "true");
  });

  it("fills server defaults and refetches dependent fields when what they depend on changes", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness form={rhelForm()} />);
    await waitFor(() => expect(values()).toMatchObject({ cluster: "GVA-DEV-CL01", datastore: "vsan-gva-dev-cl01" }));
    await waitFor(() => expect(values()).toMatchObject({ vm_name: "web-dev-app001", ip_address: "10.31.10.57" }));

    // The environment changes the cluster, which changes the datastores, the network and the IP.
    await user.click(screen.getByRole("button", { name: "Switch to prd" }));
    await waitFor(() =>
      expect(values()).toMatchObject({
        cluster: "GVA-PRD-CL02",
        datastore: "vsan-gva-prd-cl02",
        network: "dvpg-web-prd-vl210",
        ip_address: "10.21.10.88",
        vm_name: "web-prd-app001",
      }),
    );

    // Typing a role re-resolves the computed VM name.
    const role = screen.getByLabelText("Role");
    await user.clear(role);
    await user.type(role, "front");
    await waitFor(() => expect(values()).toMatchObject({ vm_name: "web-prd-front004" }));
  });

  it("shows what the server's rule results say (the approval note, a computed value)", () => {
    const hidden = { visible: false, required: false, read_only: false };
    const { unmount } = renderWithQuery(<Harness form={rhelForm()} fieldStates={{ size_note: hidden }} />);
    expect(screen.queryByText(/More than 32 GB RAM in production/)).not.toBeInTheDocument();
    unmount();
    renderWithQuery(
      <Harness
        form={rhelForm()}
        fieldStates={{
          size_note: { ...hidden, visible: true },
          role: { visible: true, required: true, read_only: true, value: "db" },
        }}
      />,
    );
    expect(screen.getByText(/More than 32 GB RAM in production/)).toBeInTheDocument();
    expect(screen.getByLabelText("Role")).toHaveValue("db");
  });

  it("keeps required checklist options selected", async () => {
    const user = userEvent.setup();
    renderWithQuery(<Harness form={rhelForm()} />);
    expect(screen.getByText("Enrols the host in idm.corp.example and applies HBAC rules")).toBeInTheDocument();
    await user.click(screen.getByLabelText(/Join IdM/));
    expect(values().post_config).toEqual(["linux-baseline", "monitoring-agent"]);
    await user.click(screen.getByLabelText(/Corporate baseline/));
    expect(values().post_config).toEqual(["linux-baseline", "monitoring-agent"]);
  });

  it("puts error messages on their fields and links them for assistive tech", () => {
    const errors = mapFieldErrors([
      { field: "/inputs/role", code: "pattern", message: "Use 2–12 lower-case letters, for example “api”." },
      { field: "", code: "quota_exceeded", message: "Not enough vCPU left." },
    ]);
    expect(errors.form).toEqual(["Not enough vCPU left."]);
    renderWithQuery(<Harness form={rhelForm()} errors={errors.fields} />);
    const role = screen.getByLabelText("Role");
    expect(role).toHaveAttribute("aria-invalid", "true");
    expect(role).toHaveAccessibleDescription(/Use 2–12 lower-case letters/);
  });
});
