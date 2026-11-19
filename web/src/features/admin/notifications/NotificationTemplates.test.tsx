import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { db, resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

type User = ReturnType<typeof userEvent.setup>;

const auditedActions = () =>
  (db as unknown as { governance?: { audit: { action: string }[] } }).governance?.audit.map((a) => a.action) ?? [];

async function replace(user: User, label: string, text: string) {
  const box = screen.getByLabelText(label);
  await user.clear(box);
  await user.click(box);
  await user.paste(text);
}

const value = (label: string) => screen.getByLabelText<HTMLInputElement>(label).value;

const frame = () => screen.findByTitle("The email as HTML");

afterEach(() => {
  localStorage.removeItem("rvav.mock.as");
  resetDb();
});

describe("Notification templates", () => {
  it("lists every kind, the built-in templates until one is edited", async () => {
    renderApp("/admin/notification-templates");
    const table = await screen.findByRole("table", { name: "Notification templates" });
    await within(table).findByRole("row", { name: /Approval needed/ });
    expect(within(table).getAllByRole("row")).toHaveLength(12);
    expect(within(table).getByRole("row", { name: /Approval needed/ })).toHaveTextContent("Built-in templates");
    expect(within(table).getByRole("link", { name: "Approval needed" })).toHaveAttribute(
      "href",
      "/admin/notification-templates/approval_needed",
    );
  });

  it(
    "previews an email with the sample, refuses an unknown variable, saves and resets",
    { timeout: 30_000 },
    async () => {
      const user = userEvent.setup();
      renderApp("/admin/notification-templates/approval_needed");
      expect(await screen.findByRole("heading", { name: "Approval needed", level: 1 })).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "Email" })).toHaveAttribute("aria-pressed", "true");
      expect(value("Subject")).toContain("{{.Request.Title}}");
      expect(screen.getByText("The built-in template.")).toBeInTheDocument();
      expect(await frame()).toHaveAttribute("sandbox", "");
      await waitFor(async () =>
        expect((await frame()).getAttribute("srcdoc")).toContain("<strong>REQ-10077</strong> waits for your decision"),
      );
      expect(screen.getByText("Approval needed: RHEL 9 server web-prd-front01 (REQ-10077)")).toBeInTheDocument();
      const variables = screen.getByRole("table", { name: "Variables" });
      expect(within(variables).getByRole("row", { name: /\.Approval\.Reasons/ })).toHaveTextContent("list of text");

      await replace(user, "Subject", "{{.Deployment.Name}} waits");
      expect(
        await screen.findByText(/Subject: Line 1: \.Deployment is not a variable of this kind\./),
      ).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "Save template" }));
      await waitFor(() =>
        expect(screen.getByLabelText("Subject")).toHaveAccessibleDescription(
          "Line 1: .Deployment is not a variable of this kind.",
        ),
      );

      await replace(user, "Subject", "Decide on {{.Request.Number}} <now>");
      await replace(user, "HTML", "<p>{{.Request.Title}} & {{.User.Name}}</p>");
      await waitFor(async () =>
        expect((await frame()).getAttribute("srcdoc")).toContain(
          "<p>RHEL 9 server web-prd-front01 & Thomas Nguyen</p>",
        ),
      );
      expect(screen.getByText("Decide on REQ-10077 <now>")).toBeInTheDocument();
      expect(screen.getByText("Not saved yet.")).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "Save template" }));
      expect(await screen.findByText("Template saved")).toBeInTheDocument();
      expect(await screen.findByText(/^Edited by .+, /)).toBeInTheDocument();
      expect(screen.getByLabelText("Subject")).toHaveValue("Decide on {{.Request.Number}} <now>");

      await user.click(screen.getByRole("button", { name: "French" }));
      await waitFor(() => expect(value("Subject")).toContain("Approbation"));
      expect(screen.getByText("The built-in template.")).toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "English" }));
      await waitFor(() => expect(screen.getByLabelText("Subject")).toHaveValue("Decide on {{.Request.Number}} <now>"));

      await user.click(screen.getByRole("button", { name: "Reset to built-in" }));
      const dialog = await screen.findByRole("alertdialog", { name: "Go back to the built-in template?" });
      await user.click(within(dialog).getByRole("button", { name: "Reset to built-in" }));
      expect(await screen.findByText("Built-in template back")).toBeInTheDocument();
      await waitFor(() => expect(value("Subject")).toContain("{{.Request.Title}}"));
      expect(auditedActions()).toEqual(
        expect.arrayContaining(["settings.notification_template.update", "settings.notification_template.reset"]),
      );
    },
  );

  it("previews Teams and Slack messages, with what variables hold escaped", { timeout: 20_000 }, async () => {
    const user = userEvent.setup();
    renderApp("/admin/notification-templates/request_failed?channel=slack");
    expect(await screen.findByRole("heading", { name: "Request failed", level: 1 })).toBeInTheDocument();
    expect(screen.queryByLabelText("HTML")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Title")).toBeInTheDocument();
    expect(await screen.findByRole("tab", { name: "Message" })).toHaveAttribute("aria-selected", "true");
    await replace(user, "Text", "*{{.Request.Number}}* failed");
    await waitFor(() => expect(within(screen.getByRole("tabpanel")).getByText("REQ-10077").tagName).toBe("STRONG"));
    expect(screen.getByRole("tabpanel")).toHaveTextContent("REQ-10077 failed");
    expect(within(screen.getByRole("tabpanel")).getByText("Open in rvav")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "JSON" }));
    expect(screen.getByRole("region", { name: "The message's blocks" })).toHaveTextContent('"type": "header"');

    await user.click(screen.getByRole("button", { name: "Teams" }));
    expect(await screen.findByRole("tab", { name: "Card" })).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "JSON" }));
    await waitFor(() =>
      expect(screen.getByRole("region", { name: "The card's JSON" })).toHaveTextContent('"type": "AdaptiveCard"'),
    );
  });

  it("is for those who manage the system", { timeout: 15_000 }, async () => {
    localStorage.setItem("rvav.mock.as", "auditor");
    resetDb();
    renderApp("/admin/notification-templates/approval_needed");
    expect(await screen.findByText("Notification templates needs a role you don't have")).toBeInTheDocument();
  });
});
