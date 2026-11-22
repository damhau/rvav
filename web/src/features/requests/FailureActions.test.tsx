import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ServiceRequest, Step } from "@/lib/api/types";
import { i18n } from "@/lib/i18n";
import { db } from "@/mocks/seed";
import { renderWithQuery } from "@/test/render";
import { FailureActions, mayActOnFailure } from "./FailureActions";
import { rollbackOf } from "./run";

const stored = (number: string) => db.requests.find((r) => r.request.number === number)!;

const step = (key: string, system: Step["system"], status: Step["status"]): Step => ({
  id: `stp_${key}`,
  key,
  title: key,
  system,
  status,
  has_log: false,
  children: [],
});

/** A composite of AWX jobs only, failed at its second, as the server plans its steps: no VM, address or record. */
function vmLessComposite(): { request: ServiceRequest; steps: Step[] } {
  const { request } = stored("REQ-10484");
  const steps = [
    step("submit", "rvav", "done"),
    step("policy", "policy", "done"),
    step("step:baseline", "awx", "done"),
    step("step:harden", "awx", "failed"),
    step("ready", "rvav", "pending"),
  ];
  return {
    request: { ...request, error: { step_id: "stp_step:harden", message: "AWX: job 18250 failed" } },
    steps,
  };
}

/** A VM item's steps for each of `vms`, as the server keys them when it builds more than one. */
const vmSteps = (vms: string[], status: Step["status"] = "done") =>
  vms.flatMap((vm) => [
    step(`ipam.reserve@${vm}`, "efficientip", status),
    step(`dns.create@${vm}`, "efficientip", status),
    step(`vsphere.clone@${vm}`, "vcenter", status),
  ]);

describe("a failed request's actions", () => {
  it("kept what it built: Retry from the failed step, or Roll back", () => {
    const { request, steps } = stored("REQ-10482");
    expect(rollbackOf(steps)).toBeUndefined();
    renderWithQuery(<FailureActions request={request} steps={steps} />);
    const box = screen.getByRole("alert");
    expect(within(box).getByText(request.error!.message)).toHaveAttribute("lang", "en");
    expect(box).toHaveTextContent("What was created is kept so you can look at it.");
    expect(within(box).getByRole("button", { name: "Retry from the failed step" })).toBeInTheDocument();
    expect(within(box).getByRole("button", { name: "Roll back" })).toBeInTheDocument();
  });

  it("rolled back (rollback_on_failure): says what was deleted, nothing to roll back, Retry runs it all again", () => {
    const { request, steps } = stored("REQ-10484");
    renderWithQuery(<FailureActions request={request} steps={steps} />);
    const box = screen.getByRole("alert");
    expect(box).toHaveTextContent(
      "It was rolled back: deleted VM web-dev-pgorders001, deleted VM web-dev-pgorders002, deleted VM web-dev-pgorders003.",
    );
    expect(box).toHaveTextContent("Retry runs it again from its first step");
    expect(box).not.toHaveTextContent("What was created is kept");
    expect(within(box).getByRole("button", { name: "Retry" })).toBeInTheDocument();
    expect(within(box).queryByRole("button", { name: "Roll back" })).not.toBeInTheDocument();
  });

  it("a rollback that failed says why and what stays: Roll back stays", () => {
    const { request, steps } = stored("REQ-10482");
    const failed: Step = {
      ...steps[0]!,
      id: "stp_rb",
      key: "rollback",
      title: "Roll back failed",
      status: "failed",
      error: "Its custom resources weren't all deleted, so what they may use stays: web-lb: AWX job 812 failed.",
    };
    const all = [...steps, failed];
    renderWithQuery(<FailureActions request={request} steps={all} />);
    const box = screen.getByRole("alert");
    expect(box).toHaveTextContent(
      "Rolling it back didn't finish: Its custom resources weren't all deleted, so what they may use stays: web-lb: AWX job 812 failed. What it didn't delete is kept.",
    );
    expect(box).not.toHaveTextContent("What was created is kept so you can look at it.");
    expect(within(box).getByRole("button", { name: "Roll back" })).toBeInTheDocument();
  });

  it("offers neither to one who may not use them, an auditor: it says what failed", () => {
    const { request, steps } = stored("REQ-10482");
    renderWithQuery(<FailureActions request={request} steps={steps} mayAct={false} />);
    const box = screen.getByRole("alert");
    expect(box).toHaveTextContent(request.error!.message);
    expect(box).not.toHaveTextContent("Retry");
    expect(within(box).queryByRole("button")).not.toBeInTheDocument();
  });

  it("a VM request's says Roll back deletes its VM, its IP address and DNS records, and so does its confirmation", async () => {
    const user = userEvent.setup();
    const { request, steps } = stored("REQ-10482");
    renderWithQuery(<FailureActions request={request} steps={steps} />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Retry runs the failed step and the ones after it; Roll back deletes the VM, its IP address and DNS records.",
    );
    await user.click(screen.getByRole("button", { name: "Roll back" }));
    expect(await screen.findByRole("alertdialog")).toHaveTextContent(
      "Deletes the VM, its IP address and DNS records. You can retry it afterwards.",
    );
  });

  it("names a VM item's VMs it made, and only what its steps made for them", () => {
    const { request } = stored("REQ-10482");
    const vms = ["web-dev-app001", "web-dev-app002"];
    const { unmount } = renderWithQuery(
      <FailureActions request={request} steps={[step("submit", "rvav", "done"), ...vmSteps(vms)]} />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Roll back deletes the 2 VMs, their IP addresses and DNS records.",
    );
    unmount();
    const noRecords = vmSteps(vms).filter((s) => !s.key.startsWith("dns."));
    renderWithQuery(<FailureActions request={request} steps={noRecords} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Roll back deletes the 2 VMs and their IP addresses.");
  });

  it("a VM request that failed before its VM was cloned says Roll back deletes what it created", () => {
    const { request } = stored("REQ-10482");
    const steps = [step("ipam.reserve", "efficientip", "failed"), ...vmSteps(["web-dev-app001"], "pending")];
    renderWithQuery(<FailureActions request={request} steps={steps} />);
    const box = screen.getByRole("alert");
    expect(box).toHaveTextContent("Roll back deletes what it created.");
    expect(box).not.toHaveTextContent("deletes the VM");
  });

  it("a composite that made no VM says Roll back deletes what it created, not a VM", async () => {
    const user = userEvent.setup();
    const { request, steps } = vmLessComposite();
    expect(rollbackOf(steps)).toBeUndefined();
    renderWithQuery(<FailureActions request={request} steps={steps} />);
    const box = screen.getByRole("alert");
    expect(box).toHaveTextContent(
      "What was created is kept so you can look at it. Retry runs the failed step and the ones after it; Roll back deletes what it created.",
    );
    expect(box).not.toHaveTextContent(/VM|IP address|DNS/);
    await user.click(within(box).getByRole("button", { name: "Roll back" }));
    const dialog = await screen.findByRole("alertdialog");
    expect(dialog).toHaveTextContent("Deletes what it created. You can retry it afterwards.");
    expect(dialog).not.toHaveTextContent(/VM|IP address|DNS/);
  });

  it("says it in French: a VM request's VM, a VM-less composite's what it created", async () => {
    await i18n.changeLanguage("fr");
    const { request, steps } = stored("REQ-10482");
    const { unmount } = renderWithQuery(<FailureActions request={request} steps={steps} />);
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Revenir en arrière supprime la VM, son adresse IP et ses enregistrements DNS.",
    );
    unmount();
    const composite = vmLessComposite();
    renderWithQuery(<FailureActions request={composite.request} steps={composite.steps} />);
    const box = screen.getByRole("alert");
    expect(box).toHaveTextContent(
      "Réessayer exécute l'étape en échec et celles qui la suivent ; Revenir en arrière supprime ce qu'elle a créé.",
    );
    expect(box).not.toHaveTextContent(/VM|IP|DNS/);
  });

  it("lets the requester, the owner, a project admin act, and leaves a day-2 request's to the server", () => {
    const { request } = stored("REQ-10482");
    const someone = { ...db.me, user: { ...db.me.user, id: "usr_other" }, permissions: [], projects: [] };
    expect(mayActOnFailure(someone, request)).toBe(false);
    expect(mayActOnFailure({ ...someone, user: { ...someone.user, id: request.requester.id } }, request)).toBe(true);
    expect(mayActOnFailure(someone, { ...request, on_behalf_of: { ...request.requester, id: "usr_other" } })).toBe(
      true,
    );
    expect(mayActOnFailure({ ...someone, permissions: ["request.manage"] }, request)).toBe(true);
    expect(mayActOnFailure(someone, { ...request, deployment_id: "dep_1" })).toBe(true);
  });
});
