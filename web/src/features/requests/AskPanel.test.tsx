// Answering a step that waits for a person (D2.3) from the keyboard: an answer
// refused, by the server or for want of a comment, takes the focus to the field
// to fix once it is marked, so a screen reader reads the problem with it.
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import type { ApprovalPage, ServiceRequest } from "@/lib/api/types";
import { resetDb } from "@/mocks/seed";
import { renderApp } from "@/test/render";

const listening: ((e: FocusEvent) => void)[] = [];
afterEach(() => {
  for (const l of listening.splice(0)) document.removeEventListener("focusin", l);
  resetDb();
});

const api = async <T,>(method: string, path: string, body?: unknown): Promise<T> => {
  const res = await fetch(`http://localhost/api/v1${path}`, {
    method,
    headers: {
      Authorization: "Bearer test",
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${await res.text()}`);
  return (res.status === 204 ? undefined : await res.json()) as T;
};

/** A request of a composite that asks its requester, once the run waits for the answer. */
async function asked(slug: string, ask: Record<string, unknown>) {
  const item = await api<{ id: string; draft_version: { id: string } }>("POST", "/admin/catalog/items", {
    slug,
    name: slug,
    summary: "Asks.",
    category: "compute",
    type: "composite",
  });
  const version = `/admin/catalog/items/${slug}/versions/${item.draft_version.id}`;
  const draft = await api<{ definition: Record<string, unknown>; revision: number }>("GET", version);
  const blueprint = { kind: "composite", steps: [{ key: "check", title: "Owner check", ask }] };
  await api("PUT", version, { definition: { ...draft.definition, blueprint }, base_revision: draft.revision });
  await api("POST", `${version}/publish`);
  await api("PUT", `/admin/catalog/items/${slug}/entitlements`, { all_projects: true, project_ids: [] });
  const r = await api<ServiceRequest>("POST", "/requests", {
    catalog_item_id: item.id,
    version_id: item.draft_version.id,
    project_id: "prj_web",
    environment: "dev",
    inputs: { name: "web-01" },
  });
  await waitFor(
    async () => {
      const inbox = await api<ApprovalPage>("GET", "/approvals");
      expect(inbox.items.some((a) => a.request.id === r.id)).toBe(true);
    },
    { timeout: 5000 },
  );
  return r;
}

/** Each element the focus reaches, and whether it was marked invalid and described then. */
function focusLog() {
  const log: { el: Element; marked: boolean }[] = [];
  const note = (e: FocusEvent) => {
    const el = e.target as Element;
    log.push({ el, marked: el.getAttribute("aria-invalid") === "true" && el.hasAttribute("aria-describedby") });
  };
  document.addEventListener("focusin", note);
  listening.push(note);
  return log;
}

async function press(user: ReturnType<typeof userEvent.setup>, button: HTMLElement) {
  button.focus();
  await user.keyboard("{Enter}");
}

describe("answering a step that waits for a person", () => {
  it("goes to the question the server says to answer, marked as it arrives", { timeout: 15_000 }, async () => {
    const r = await asked("backup-check", {
      requester: true,
      form: [{ key: "backup", label: "Backup policy", type: "choice", options: ["daily", "none"], required: true }],
    });
    const user = userEvent.setup();
    renderApp(`/requests/${r.number}`);
    const form = await screen.findByRole("form", { name: "Your answer: Owner check" }, { timeout: 5000 });
    const backup = within(form).getByRole("combobox", { name: /^Backup policy/ });
    const log = focusLog();
    await press(user, within(form).getByRole("button", { name: "Send answer" }));
    await waitFor(() => expect(backup).toHaveFocus());
    expect(backup).toHaveAccessibleDescription("Answer this.");
    expect(log.find((f) => f.el === backup)?.marked).toBe(true);
  });

  it("goes to the comment a reject needs, marked as it arrives", { timeout: 15_000 }, async () => {
    const r = await asked("owner-check", { requester: true });
    const user = userEvent.setup();
    renderApp(`/requests/${r.number}`);
    const form = await screen.findByRole("form", { name: "Your answer: Owner check" }, { timeout: 5000 });
    const comment = within(form).getByRole("textbox", { name: /^Comment/ });
    const log = focusLog();
    await press(user, within(form).getByRole("button", { name: "Reject" }));
    await waitFor(() => expect(comment).toHaveFocus());
    expect(comment).toHaveAccessibleDescription("Say why you reject it: the requester sees this comment.");
    expect(log.find((f) => f.el === comment)?.marked).toBe(true);
  });
});
