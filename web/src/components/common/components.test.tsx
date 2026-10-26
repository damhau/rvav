import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ApiError } from "@/lib/api/client";
import { ErrorState } from "./feedback";
import { Meter } from "./meter";
import { DeploymentStatusBadge, PowerDot, RequestStatusBadge } from "./status";
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
});

describe("Meter", () => {
  it("shows usage after the request and flags going over the limit", () => {
    render(<Meter label="vCPU" used={150} adding={16} total={160} />);
    expect(screen.getByText("166")).toHaveClass("text-bad");
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
});
