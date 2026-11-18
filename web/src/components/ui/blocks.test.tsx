import { render, screen, within } from "@testing-library/react";
import { Inbox, Trash2 } from "lucide-react";
import { describe, expect, it } from "vitest";
import { Breadcrumbs } from "./breadcrumbs";
import { Button, IconButton } from "./button";
import { EmptyState } from "./empty-state";
import { Field, Input } from "./input";
import { PageHeader } from "./page-header";
import { Panel, Section } from "./panel";

describe("PageHeader", () => {
  it("is the page's one h1, with what it is for and its actions", () => {
    render(
      <PageHeader
        title="Infrastructure"
        description="What VMs are built from."
        breadcrumbs={<a href="/admin">Administration</a>}
        actions={<Button variant="primary">New template</Button>}
      />,
    );
    const header = screen.getByRole("banner");
    expect(within(header).getByRole("heading", { level: 1, name: "Infrastructure" })).toBeInTheDocument();
    expect(header).toHaveTextContent("What VMs are built from.");
    expect(within(header).getByRole("link", { name: "Administration" })).toBeInTheDocument();
    expect(within(header).getByRole("button", { name: "New template" })).toBeInTheDocument();
  });
});

describe("Breadcrumbs", () => {
  it("is a navigation landmark listing the pages above, top-level first", () => {
    render(
      <PageHeader
        title="Console of web01"
        breadcrumbs={
          <Breadcrumbs>
            <a href="/deployments">Deployments</a>
            {null}
            <a href="/deployments/dep_1">web-prod</a>
          </Breadcrumbs>
        }
      />,
    );
    const trail = screen.getByRole("navigation", { name: "Breadcrumb" });
    const items = within(trail).getAllByRole("listitem");
    expect(items.map((i) => i.textContent)).toEqual(["Deployments", "web-prod"]);
    expect(
      within(trail)
        .getAllByRole("link")
        .map((a) => a.getAttribute("href")),
    ).toEqual(["/deployments", "/deployments/dep_1"]);
  });
});

describe("Panel and Section", () => {
  it("are regions named by their title, at the level asked", () => {
    render(
      <>
        <Panel
          title="Sizes"
          description="What size pickers offer."
          actions={<Button>New size</Button>}
          footer={<Button variant="primary">Save</Button>}
        >
          <p>Medium</p>
        </Panel>
        <Section title="Limits" level={3}>
          <p>16 vCPU</p>
        </Section>
      </>,
    );
    const sizes = screen.getByRole("region", { name: "Sizes" });
    expect(within(sizes).getByRole("heading", { level: 2, name: "Sizes" })).toBeInTheDocument();
    expect(sizes).toHaveTextContent("What size pickers offer.");
    expect(within(sizes).getByRole("button", { name: "New size" })).toBeInTheDocument();
    expect(within(sizes).getByRole("button", { name: "Save" })).toBeInTheDocument();
    const limits = screen.getByRole("region", { name: "Limits" });
    expect(within(limits).getByRole("heading", { level: 3, name: "Limits" })).toBeInTheDocument();
  });

  it("is no region without a title", () => {
    render(
      <Panel>
        <p>Untitled</p>
      </Panel>,
    );
    expect(screen.queryByRole("region")).toBeNull();
  });
});

describe("EmptyState", () => {
  it("says what goes here and offers the next step, its icon hidden", () => {
    const { container } = render(
      <EmptyState
        icon={Inbox}
        title="No templates yet"
        body="Add the vCenter templates projects may build from."
        action={<Button variant="primary">New template</Button>}
      />,
    );
    expect(screen.getByText("No templates yet")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "New template" })).toBeInTheDocument();
    expect(container.querySelector("svg")?.closest("[aria-hidden]")).not.toBeNull();
  });
});

describe("Button", () => {
  it.each(["primary", "secondary", "quiet", "danger", "danger-solid"] as const)("%s is a button", (variant) => {
    render(<Button variant={variant}>Save</Button>);
    expect(screen.getByRole("button", { name: "Save" })).toHaveAttribute(
      "class",
      expect.stringContaining("rounded-md"),
    );
  });

  it("an icon button is named by its label", () => {
    render(
      <IconButton label="Remove Medium">
        <Trash2 />
      </IconButton>,
    );
    expect(screen.getByRole("button", { name: "Remove Medium" })).toHaveAttribute("title", "Remove Medium");
  });
});

describe("Field", () => {
  it("ties its help to the control", () => {
    render(
      <Field label="Key" htmlFor="key" hint="Letters, digits and dashes.">
        <Input id="key" />
      </Field>,
    );
    const key = screen.getByRole("textbox", { name: "Key" });
    expect(key).toHaveAccessibleDescription("Letters, digits and dashes.");
    expect(key).not.toBeInvalid();
  });

  it("shows the problem instead of the help, and marks the control invalid", () => {
    render(
      <Field label="Key" htmlFor="key" hint="Letters, digits and dashes." error="Already used by another template.">
        <Input id="key" />
      </Field>,
    );
    const key = screen.getByRole("textbox", { name: "Key" });
    expect(key).toHaveAccessibleDescription("Already used by another template.");
    expect(key).toBeInvalid();
    expect(screen.queryByText("Letters, digits and dashes.")).toBeNull();
  });
});
