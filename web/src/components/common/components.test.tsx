import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api/client";
import { ErrorState } from "./feedback";
import { Meter } from "./meter";
import { renderWithQuery } from "@/test/render";
import { DeploymentStatusBadge, EnvBadge, PowerDot, RequestStatusBadge } from "./status";
import { Timeline } from "./timeline";

describe("status badges", () => {
  it.each([
    ["pending_approval", "Awaiting approval"],
    ["running", "In progress"],
    ["failed", "Failed"],
    ["cancelling", "Cancelling"],
  ] as const)("request %s reads “%s”", (status, label) => {
    render(<RequestStatusBadge status={status} />);
    expect(screen.getByText(label)).toBeInTheDocument();
  });

  it("deployment and power state have text, not only colour", () => {
    render(
      <>
        <DeploymentStatusBadge status="provisioning" />
        <PowerDot on={false} />
      </>,
    );
    expect(screen.getByText("Provisioning")).toBeInTheDocument();
    expect(screen.getByText("Powered off")).toBeInTheDocument();
  });

  it("outline production in red and the other environments in grey", async () => {
    renderWithQuery(
      <>
        <EnvBadge env="prd" />
        <EnvBadge env="dev" />
      </>,
    );
    expect(await screen.findByText("Production")).toHaveClass("border", "border-bad/50", "text-bad", "text-badge");
    expect(screen.getByText("Development")).toHaveClass("border", "border-line-strong", "text-muted", "text-badge");
    expect(screen.getByText("Development")).not.toHaveClass("border-line");
  });
});

describe("Meter", () => {
  it("shows usage after the request and flags going over the limit", () => {
    render(<Meter label="vCPU" used={150} adding={16} total={160} />);
    expect(screen.getByText("166")).toHaveClass("text-bad");
  });

  it("draws no bar without a limit, rather than one full and amber", () => {
    // A new install's projects have no quota until one is set: the bar was
    // drawn against the usage itself, 4 / 4 GB in the colour of a quota used up.
    const { container } = render(<Meter label="Memory" unit="GB" used={0} adding={4} total={null} />);
    expect(container).toHaveTextContent("Memory4 GB · no limit");
    expect(container).not.toHaveTextContent("/");
    expect(container.querySelector(".bg-warn, .bg-bad, .bg-accent")).toBeNull();
  });
});

describe("ErrorState", () => {
  it("shows the problem and the request ID to quote", () => {
    const err = new ApiError(404, {
      type: "https://rvav.dev/problems/not-found",
      title: "Deployment not found",
      detail: "It doesn't exist, or you can't see it.",
      status: 404,
      request_id: "rq-abc123",
    });
    render(<ErrorState error={err} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Deployment not found");
    expect(screen.getByText("rq-abc123")).toBeInTheDocument();
  });
});

describe("Timeline", () => {
  it("renders AWX tasks under their job and marks the failed one", () => {
    render(
      <Timeline
        steps={[
          {
            id: "j",
            key: "awx.job",
            title: "AWX job 1 · linux-baseline",
            system: "awx",
            status: "failed",
            has_log: true,
            children: [
              { id: "t1", key: "awx.task", title: "Gathering Facts", status: "done", children: [], has_log: false },
              {
                id: "t2",
                key: "awx.task",
                title: "idm : join realm",
                status: "failed",
                error: "DNS discovery failed",
                children: [],
                has_log: false,
              },
            ],
          },
        ]}
        onOpenLog={() => {}}
      />,
    );
    expect(screen.getByText("idm : join realm")).toHaveClass("text-bad");
    expect(screen.getByText("DNS discovery failed")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Show job output" })).toBeInTheDocument();
  });

  it("offers a step's data only where it has some, and says which step it shows", async () => {
    const opened = vi.fn();
    const step = (id: string, title: string, extra: object) => ({
      id,
      key: `step:${id}`,
      title,
      status: "done" as const,
      children: [],
      has_log: false,
      ...extra,
    });
    const { rerender } = render(
      <Timeline
        steps={[step("register", "Register", { has_data: true }), step("notify", "Tell the team", {})]}
        onOpenData={opened}
      />,
    );
    const show = screen.getByRole("button", { name: "Show data of Register" });
    expect(show).toHaveAttribute("aria-pressed", "false");
    expect(screen.getAllByRole("button")).toHaveLength(1);
    await userEvent.setup().click(show);
    expect(opened).toHaveBeenCalledWith(expect.objectContaining({ id: "register" }));
    rerender(
      <Timeline
        steps={[step("register", "Register", { has_data: true })]}
        onOpenData={opened}
        selectedDataStep="register"
      />,
    );
    expect(screen.getByRole("button", { name: "Showing data of Register" })).toHaveAttribute("aria-pressed", "true");
  });
});
