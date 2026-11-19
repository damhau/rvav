import type { components } from "@/lib/api/schema";
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
  const prefixes = new Set(vmNames.map((n) => n.replace(/\d+$/, "")).filter(Boolean));
  const vmNamed = names.find((n) =>
    [...prefixes].some((p) => n.toLowerCase().startsWith(p) && /^\d+$/.test(n.toLowerCase().slice(p.length))),
  );
  if (vmNamed) {
    const p = [...prefixes].find((x) => vmNamed.toLowerCase().startsWith(x));
    errors.push({
      field: "/deployment_name",
      code: "naming",
      message: `${vmNamed} is a VM name: the naming policy gives ${p} and a number. Choose another.`,
    });
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
