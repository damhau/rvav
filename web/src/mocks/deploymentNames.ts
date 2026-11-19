import type { components } from "@/lib/api/schema";
import type { DeploymentDetail } from "@/lib/api/types";
import { multiParts } from "./components";
import type { Db, StoredItem } from "./seed";

type S = components["schemas"];

export const MAX_DEPLOYMENT_NAME = 80;
export const MAX_DEPLOYMENT_DESCRIPTION = 2000;

/** The item makes deployments a request may name: a VM item, or a composite with a VM step (as core.MakesDeployments). */
export function namesDeployments(item: StoredItem): boolean {
  return item.type === "vm_blueprint" || item.slug === "postgres-cluster";
}

/** As core.numbered: a name for one deployment, numbered for several. */
export function numbered(name: string, n: number): string[] {
  return n <= 1 ? [name] : Array.from({ length: n }, (_, i) => `${name} ${i + 1}`);
}

const control = /\p{Cc}/u;

/** What is wrong with a name given to a deployment, as core.deploymentNameErr. */
export function deploymentNameProblem(field: string, name: string): S["FieldError"] | undefined {
  if (!name) return { field, code: "required", message: "Give the deployment a name." };
  if ([...name].length > MAX_DEPLOYMENT_NAME)
    return { field, code: "too_long", message: `${MAX_DEPLOYMENT_NAME} characters at most.` };
  if (control.test(name)) return { field, code: "invalid", message: "One line, without tabs." };
  return undefined;
}

/** The name a live deployment of the project but except holds for one of names, whatever the case, as it holds it. */
export function takenName(db: Db, projectId: string, names: string[], except?: string) {
  const wanted = new Set(names.map((n) => n.toLowerCase()));
  return db.deployments.find(
    (d) => d.project.id === projectId && d.status !== "deleted" && d.id !== except && wanted.has(d.name.toLowerCase()),
  )?.name;
}

/**
 * The name and description a request gives its deployments, checked as
 * core.evaluateDeployments does: the names they take, and what is wrong.
 */
export function requestedNames(
  db: Db,
  item: StoredItem,
  project: S["Project"],
  draft: S["RequestDraft"],
  inputs: Record<string, unknown>,
  vmNames: string[],
): { names?: string[]; errors: S["FieldError"][] } {
  const name = (draft.deployment_name ?? "").trim();
  const description = (draft.deployment_description ?? "").trim();
  const errors: S["FieldError"][] = [];
  if (!name && !description) return { errors };
  if (!namesDeployments(item)) {
    if (name)
      errors.push({ field: "/deployment_name", code: "no_deployment", message: `${item.name} makes no deployment.` });
    if (description)
      errors.push({
        field: "/deployment_description",
        code: "no_deployment",
        message: `${item.name} makes no deployment.`,
      });
    return { errors };
  }
  if ([...description].length > MAX_DEPLOYMENT_DESCRIPTION)
    errors.push({
      field: "/deployment_description",
      code: "too_long",
      message: `${MAX_DEPLOYMENT_DESCRIPTION} characters at most.`,
    });
  if (!name) return { errors };
  const wrong = deploymentNameProblem("/deployment_name", name);
  if (wrong) return { errors: [...errors, wrong] };
  const single = item.type === "vm_blueprint" && !multiParts(item.slug, inputs);
  const names = numbered(name, single ? (draft.count ?? 1) : 1);
  const vmNamed = namedLike(vmNames, names);
  if (vmNamed) {
    errors.push({ field: "/deployment_name", code: "naming", message: vmNamed });
    return { names, errors };
  }
  const taken = takenName(db, project.id, names);
  if (taken)
    errors.push({
      field: "/deployment_name",
      code: "taken",
      message: `${project.name} already has a deployment named ${taken}.`,
    });
  return { names, errors };
}

/** As core.namedLike: the first of names that the naming of vmNames gives (their prefix and a number), as a refusal. */
function namedLike(vmNames: string[], names: string[]): string | undefined {
  const prefixes = new Set<string>();
  for (const v of vmNames) {
    const p = v.toLowerCase().replace(/\d+$/, "");
    if (p && p !== v.toLowerCase()) prefixes.add(p);
  }
  for (const p of prefixes)
    for (const n of names)
      if (n.toLowerCase().startsWith(p) && /^\d+$/.test(n.toLowerCase().slice(p.length)))
        return `${n} is a VM name: the naming policy gives ${p} and a number. Choose another.`;
  return undefined;
}

/** The name and description Edit deployment gives, trimmed, as day2.DeploymentDetails. */
export function editedDetails(inputs: Record<string, unknown>) {
  const text = (v: unknown) => (typeof v === "string" ? v.trim() : "");
  return { name: text(inputs.name), description: text(inputs.description) };
}

/** What is wrong with the name and description Edit deployment gives, as the server's checks (ADR-0046). */
export function editProblem(db: Db, d: DeploymentDetail, inputs: Record<string, unknown>): S["FieldError"] | null {
  const { name, description } = editedDetails(inputs);
  if (name === d.name && description === (d.description ?? ""))
    return {
      field: "/inputs/name",
      code: "no_change",
      message: "These are the deployment's name and description now.",
    };
  const wrong = deploymentNameProblem("/inputs/name", name);
  if (wrong) return wrong;
  if (name.toLowerCase() === d.name.toLowerCase()) return null;
  const vms = d.resources.filter((r) => r.type === "vsphere.vm").map((r) => r.name);
  const vmNamed = vms.some((v) => v.toLowerCase() === name.toLowerCase()) ? undefined : namedLike(vms, [name]);
  if (vmNamed) return { field: "/inputs/name", code: "naming", message: vmNamed };
  const held = takenName(db, d.project.id, [name], d.id);
  return held
    ? { field: "/inputs/name", code: "taken", message: `${d.project.name} already has a deployment named ${held}.` }
    : null;
}
