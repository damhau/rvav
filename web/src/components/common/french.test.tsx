import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { ApiError } from "@/lib/api/client";
import { TooltipProvider } from "@/components/ui/tooltip";
import type { Approval } from "@/lib/api/types";
import { i18n } from "@/lib/i18n";
import { ApprovalHistory, dueText } from "./approvals";
import { ErrorState } from "./feedback";
import { Meter } from "./meter";
import { RequestStatusBadge } from "./status";

beforeEach(async () => {
  await i18n.changeLanguage("fr");
});

const person = (id: string, name: string) => ({ id, name, email: `${id}@example.ch` });

const level = (o: Partial<Approval>): Approval =>
  ({
    id: "apr_1",
    request: { id: "req_1", number: "REQ-1", title: "web-prd-01", status: "pending_approval" },
    level: { index: 2, count: 2, name: "Sécurité réseau" },
    reasons: [],
    approvers: [],
    status: "pending",
    votes: [],
    due_at: null,
    on_timeout: null,
    escalate_to: null,
    escalated: false,
    ...o,
  }) as Approval;

describe("the shared components, in French", () => {
  it("say who decided what, with the parts a sentence places where French puts them", () => {
    render(
      <TooltipProvider>
        <ApprovalHistory
          levels={[
            level({
              status: "approved",
              votes: [
                {
                  user: person("u1", "Alain Favre"),
                  decision: "approve",
                  comment: "Les ports correspondent",
                  changes: [{ field: "cpu", label: "CPU", from: 2, to: 4 }],
                  decided_at: new Date(Date.now() - 5 * 60_000).toISOString(),
                },
              ],
            }),
            level({
              id: "apr_2",
              roles: [
                { key: "supervisor", name: "Superviseur" },
                { key: "owner", name: "Propriétaire" },
              ],
              manager: true,
            }),
          ]}
        />
      </TooltipProvider>,
    );
    expect(screen.getByRole("list", { name: "Niveaux d'approbation" })).toBeInTheDocument();
    expect(screen.getAllByText(/niveau 2 sur 2/)).toHaveLength(2);
    expect(screen.getByText("a approuvé").parentElement).toHaveTextContent(/^Alain Favre a approuvé il y a 5 minutes$/);
    expect(screen.getByText(/Les ports correspondent/).textContent).toBe("«\u00a0Les ports correspondent\u00a0»");
    expect(screen.getByText(/A changé cpu/)).toHaveTextContent("A changé cpu de 2 à 4");
    expect(
      screen.getByText(
        "Demande à qui détient Superviseur ou Propriétaire dans le projet et au responsable du demandeur",
      ),
    ).toBeInTheDocument();
    expect(screen.getAllByText("En attente")).toHaveLength(2);
  });

  it("say what a timeout does", () => {
    const step = { escalated: false };
    expect(dueText({ ...step, on_timeout: "escalate", escalate_to: [] })).toBe(
      "puis elle passe aux approbateurs de l'escalade",
    );
    expect(dueText({ ...step, on_timeout: "approve", escalate_to: null })).toBe(
      "puis elle est approuvée automatiquement",
    );
  });

  it("give a status, an error and a size their French words", () => {
    const err = new ApiError(500, {
      type: "about:blank",
      title: "Internal error",
      status: 500,
      request_id: "rq-abc123",
    });
    render(
      <>
        <RequestStatusBadge status="pending_approval" />
        <ErrorState error={err} onRetry={() => {}} />
        <Meter label="Mémoire" used={1.5} total={64} unit="GB" />
      </>,
    );
    expect(screen.getByText("En attente d'approbation")).toBeInTheDocument();
    expect(screen.getByText("rq-abc123").parentElement).toHaveTextContent("ID de la requête rq-abc123");
    expect(screen.getByRole("button", { name: "Réessayer" })).toBeInTheDocument();
    expect(screen.getByText("1,5")).toBeInTheDocument();
    expect(screen.getByText(/64/).textContent).toBe(" / 64\u00a0Go");
  });
});
