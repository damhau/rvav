import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Step } from "@/lib/api/types";
import { db } from "@/mocks/seed";
import { renderWithQuery } from "@/test/render";
import { FailureActions } from "./FailureActions";
import { rollbackOf } from "./run";

const stored = (number: string) => db.requests.find((r) => r.request.number === number)!;

describe("a failed request's actions", () => {
  it("kept what it built: Retry from the failed step, or Roll back", () => {
    const { request, steps } = stored("REQ-10482");
    expect(rollbackOf(steps)).toBeUndefined();
    renderWithQuery(<FailureActions request={request} rolledBack={rollbackOf(steps)} />);
    const box = screen.getByRole("alert");
    expect(box).toHaveTextContent("What was created is kept so you can look at it.");
    expect(within(box).getByRole("button", { name: "Retry from the failed step" })).toBeInTheDocument();
    expect(within(box).getByRole("button", { name: "Roll back" })).toBeInTheDocument();
  });

  it("rolled back (rollback_on_failure): says what was deleted, nothing to roll back, Retry runs it all again", () => {
    const { request, steps } = stored("REQ-10484");
    renderWithQuery(<FailureActions request={request} rolledBack={rollbackOf(steps)} />);
    const box = screen.getByRole("alert");
    expect(box).toHaveTextContent(
      "It was rolled back: deleted VM web-dev-pgorders001, deleted VM web-dev-pgorders002, deleted VM web-dev-pgorders003.",
    );
    expect(box).toHaveTextContent("Retry runs it again from its first step");
    expect(box).not.toHaveTextContent("What was created is kept");
    expect(within(box).getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(within(box).queryByRole("button", { name: "Roll back" })).not.toBeInTheDocument();
  });

  it("a rollback that failed rolled nothing back: Roll back stays", () => {
    const { request, steps } = stored("REQ-10482");
    const failed: Step = { ...steps[0]!, id: "stp_rb", key: "rollback", title: "Roll back failed", status: "failed" };
    renderWithQuery(<FailureActions request={request} rolledBack={rollbackOf([...steps, failed])} />);
    expect(screen.getByRole("button", { name: "Roll back" })).toBeInTheDocument();
  });
});
