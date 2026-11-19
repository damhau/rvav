import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { renderApp } from "@/test/render";

/** A new draft of the RHEL 9 server, open in the form designer. */
async function openDesigner() {
  const user = userEvent.setup();
  renderApp("/admin/catalog-items/itm_rhel9");
  await user.click(await screen.findByRole("button", { name: "New draft" }));
  await user.click(await screen.findByRole("tab", { name: "Form designer" }));
  const fields = await screen.findByRole("region", { name: "Fields" }, { timeout: 5000 });
  const preview = screen.getByRole("region", { name: "Preview" });
  await within(preview).findByRole("group", { name: "Basics" }, { timeout: 5000 });
  return { user, fields, preview };
}

const saved = () =>
  waitFor(() => expect(screen.getByText(/^Draft [\d.]+: saved$/)).toBeInTheDocument(), { timeout: 5000 });

describe("the designer and the form library", () => {
  it("takes a dropdown's choices from a library source, its parameter given by the form", async () => {
    const { user, fields } = await openDesigner();
    await user.click(within(fields).getByRole("combobox", { name: "Add a field" }));
    await user.click(screen.getByRole("option", { name: "Dropdown" }));
    const props = screen.getByRole("region", { name: "Properties" });
    await user.click(within(props).getByRole("combobox", { name: "Value source" }));
    await user.click(screen.getByRole("option", { name: "Form library" }));
    expect(within(props).getByRole("combobox", { name: "Entry" })).toHaveTextContent("CMDB services (cmdbServices)");
    expect(within(props).getByRole("combobox", { name: "Version" })).toHaveTextContent("1 (latest)");
    expect(within(props).getByText("The services the CMDB lists in an environment.")).toBeInTheDocument();

    const env = within(props).getByRole("textbox", { name: "Environment" });
    await user.type(env, "environment{Enter}");
    await saved();
    await user.click(within(props).getByRole("button", { name: "Test the source" }));
    expect(await within(props).findByRole("status")).toHaveTextContent("2 options: Service 01, Service 02");
  }, 20_000);

  it("adds validations of the whole form, and shows where they need work", async () => {
    const { user, fields } = await openDesigner();
    const button = within(fields).getByRole("button", { name: /^Form validations/ });
    expect(button).toHaveTextContent("None");
    await user.click(button);
    const props = screen.getByRole("region", { name: "Form validations" });
    expect(within(props).getByText("No validation yet.")).toBeInTheDocument();

    await user.click(within(props).getByRole("button", { name: "Add a validation" }));
    await waitFor(() => expect(within(props).getByRole("textbox", { name: "Key" })).toHaveFocus());
    expect(within(props).getByRole("textbox", { name: "Key" })).toHaveValue("check1");
    const check = within(props).getByRole("textbox", { name: "Check" });
    await user.clear(check);
    await user.type(check, "inputs.cpu <={Enter}");
    expect(
      await within(props).findByText(/^This expression is not valid CEL/, {}, { timeout: 5000 }),
    ).toBeInTheDocument();
    expect(within(fields).getByRole("img", { name: "Form validations have problems" })).toBeInTheDocument();
    await user.clear(check);
    await user.type(check, "inputs.cpu <= 8{Enter}");
    await waitFor(() => expect(within(props).queryByText(/^This expression is not valid CEL/)).toBeNull(), {
      timeout: 5000,
    });

    await user.click(within(props).getByRole("button", { name: "Add one from the form library" }));
    const second = within(props).getByRole("group", { name: "check2" });
    expect(within(second).getByRole("combobox", { name: "Entry" })).toHaveTextContent(
      "Cost centre open (costCentreOpen)",
    );
    const centre = within(second).getByRole("textbox", { name: "Cost centre" });
    expect(centre).toHaveValue("");
    await user.type(centre, "inputs.role +{Enter}");
    expect(
      await within(second).findByText(/^This expression is not valid CEL/, {}, { timeout: 5000 }),
    ).toBeInTheDocument();
    await user.clear(centre);
    await user.type(centre, 'inputs.role + "-cc"{Enter}');
    await saved();
    expect(within(second).queryByText(/^This expression is not valid CEL/)).toBeNull();
    await user.click(within(second).getByRole("checkbox", { name: "role" }));
    expect(within(fields).getByRole("button", { name: /^Form validations/ })).toHaveTextContent("2");

    await user.click(within(second).getByRole("button", { name: "Remove check2" }));
    expect(within(props).queryByRole("group", { name: "check2" })).toBeNull();
    await waitFor(() => expect(within(props).getByRole("button", { name: "Remove check1" })).toHaveFocus());
  }, 20_000);
});
