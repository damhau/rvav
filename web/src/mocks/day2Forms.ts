// Forms designed for custom day-2 actions in the mock backend (ADR-0050):
// one per AWX or workflow action, as the server's /admin/day2-forms, and a
// designed form worked out on the deployment or VM it runs on.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import type { ActionForm, DeploymentDetail, FieldError, RequestForm, UiField, VmResource } from "@/lib/api/types";
import { formProblems } from "./catalogAdmin";
import { workflowRegistrations } from "./extensibility";
import { validationErrors } from "./formLibrary";
import { applyStates, resolveField, settledStates, userGroups, validateInputs, withStart } from "./logic";
import type { Db } from "./seed";
import { unknownField } from "./strict";

type S = components["schemas"];
type Inputs = Record<string, unknown>;

interface StoredForm {
  definition: S["Day2FormDefinition"];
  active: boolean;
  revision: number;
  updated_at: string;
  updated_by: S["UserRef"];
}

function forms(db: Db): Map<string, StoredForm> {
  const d = db as Db & { day2Forms?: Map<string, StoredForm> };
  return (d.day2Forms ??= new Map<string, StoredForm>());
}

const asActionForm = (d: S["Day2FormDefinition"]): ActionForm => ({
  schema: d.form_schema,
  ui_schema: d.ui_schema,
  initial_values: d.initial_values,
  designed: true,
});

/** The form an action asks for while one designed for it is active. */
export function designedForm(db: Db, key: string): ActionForm | undefined {
  const f = forms(db).get(key);
  return f?.active ? asActionForm(f.definition) : undefined;
}

const osFamily = (guestOS?: string | null) => (guestOS?.toLowerCase().includes("windows") ? "windows" : "linux");
const firstIP = (vm: VmResource) => vm.vm.nics?.find((n) => n.ip_addresses?.length)?.ip_addresses?.[0] ?? "";

/** What a designed form's rules and sources read of the target, as the server's targetVars. */
function targetVars(db: Db, d: DeploymentDetail, vm?: VmResource) {
  const vms = d.resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  const families = [...new Set(vms.map((v) => osFamily(v.vm.guest_os)))];
  const resource = vm
    ? {
        id: vm.id,
        type: "vsphere.vm",
        name: vm.name,
        power_state: vm.vm.power_state,
        cpu_count: vm.vm.cpu_count,
        memory_gb: Math.floor(vm.vm.memory_mb / 1024),
        disk_gb: (vm.vm.disks ?? []).reduce((n, k) => n + k.capacity_gb, 0),
        guest_os: vm.vm.guest_os ?? "",
        os_family: osFamily(vm.vm.guest_os),
        fqdn: vm.vm.fqdn ?? "",
        ip: firstIP(vm),
        moref: vm.vm.moref,
        cluster: vm.vm.cluster ?? "",
        folder: vm.vm.folder ?? "",
        tags: vm.vm.tags ?? [],
        custom_properties: vm.vm.custom_properties ?? {},
      }
    : {
        id: d.id,
        type: "deployment",
        name: d.name,
        os_family: families.length > 1 ? "mixed" : (families[0] ?? ""),
        vms: vms.map((v) => ({ name: v.name, fqdn: v.vm.fqdn ?? "", ip: firstIP(v) })),
      };
  const item = db.items.find((i) => i.id === d.catalog_item.id);
  return {
    resource,
    deployment: {
      id: d.id,
      name: d.name,
      description: d.description ?? "",
      owner_id: d.owner.id,
      item: item?.slug ?? d.catalog_item.slug,
    },
  };
}

function formVars(db: Db, d: DeploymentDetail, vm?: VmResource) {
  return {
    environment: d.environment,
    project: { id: d.project.id, code: d.project.code, name: d.project.name },
    user: { id: db.me.user.id, roles: [], groups: userGroups[db.me.user.id] ?? [] },
    ...targetVars(db, d, vm),
  };
}

const requestForm = (key: string, form: ActionForm): RequestForm => ({
  item_id: "",
  version_id: `day2:${key}`,
  ...form,
});
const itemOf = (db: Db, d: DeploymentDetail) => db.items.find((i) => i.id === d.catalog_item.id) ?? db.items[0]!;

/**
 * A designed form on its target, as the server's formInputs: the states,
 * the inputs they leave (defaults, hidden fields dropped) and the problems;
 * a run also checks the form's validations.
 */
export function designedInputs(
  db: Db,
  d: DeploymentDetail,
  vm: VmResource | undefined,
  key: string,
  form: ActionForm,
  given: Inputs,
  run = false,
) {
  const rf = requestForm(key, form);
  const vars = formVars(db, d, vm);
  const inputs = withStart(rf, given);
  const fields = settledStates(db, rf, inputs, vars);
  const errors = applyStates(rf, fields, inputs, (x) => validateInputs(db, itemOf(db, d), x, rf));
  if (run && errors.length === 0) errors.push(...validationErrors(db, rf, { ...vars, inputs }));
  return { fields, inputs, errors };
}

/** A designed form's field resolved on its target, as the server's formValues. */
export function designedValues(
  db: Db,
  d: DeploymentDetail,
  vm: VmResource | undefined,
  key: string,
  form: ActionForm,
  field: string,
  inputs: Inputs,
  search?: string,
) {
  const project = db.projects.find((p) => p.id === d.project.id);
  if (!project) return null;
  const vars = formVars(db, d, vm);
  return resolveField(
    db,
    itemOf(db, d),
    project,
    d.environment,
    field,
    inputs,
    { form: requestForm(key, form), user: vars.user, target: targetVars(db, d, vm) },
    search,
  );
}

interface Custom {
  key: string;
  name: string;
  kind: S["Day2FormDesign"]["kind"];
  types: S["Day2FormDesign"]["types"];
  base: ActionForm;
  closed: boolean;
}

const emptyForm = (): ActionForm => ({
  schema: { type: "object", properties: {} },
  ui_schema: { fields: {}, layout: { kind: "page", sections: [] } },
  initial_values: {},
});

/** A workflow's input schema as a form, as the server's FormFromSchema. */
function schemaForm(schema: Record<string, unknown>): ActionForm {
  const props = (schema.properties ?? {}) as Record<string, Record<string, unknown>>;
  const names = Object.keys(props).sort();
  const fields: Record<string, UiField> = {};
  const initial: Inputs = {};
  for (const n of names) {
    const p = props[n]!;
    const f: UiField = {
      label:
        typeof p.title === "string" && p.title ? p.title : n.charAt(0).toUpperCase() + n.slice(1).replace(/_/g, " "),
      widget: p.type === "integer" || p.type === "number" ? "number" : p.type === "boolean" ? "toggle" : "text",
      ...(typeof p.description === "string" ? { help: p.description } : {}),
    };
    if (Array.isArray(p.enum)) {
      f.widget = "select";
      f.options = p.enum.map((v) => ({ value: String(v), label: String(v) }));
    }
    if ("default" in p) initial[n] = p.default;
    fields[n] = f;
  }
  return {
    schema,
    ui_schema: { fields, layout: { kind: "page", sections: [{ key: "main", title: "", columns: 1, fields: names }] } },
    initial_values: initial,
  };
}

export interface CustomSources {
  /** The AWX actions synced from AWX. */
  awx: () => S["AwxAction"][];
  /** An AWX action's survey form. */
  survey: (key: string) => ActionForm | undefined;
}

function custom(db: Db, src: CustomSources, key: string): Custom | undefined {
  if (key.startsWith("awx.")) {
    const a = src.awx().find((x) => x.key === key);
    if (!a) return undefined;
    return { key, name: a.name, kind: "awx", types: a.types, base: src.survey(key) ?? emptyForm(), closed: false };
  }
  if (key.startsWith("wf.")) {
    const w = workflowRegistrations(db).find((x) => `wf.${x.key}` === key && x.usages.includes("day2_action"));
    if (!w?.day2) return undefined;
    const schema = w.input_schema as Record<string, unknown>;
    return {
      key,
      name: w.day2.name,
      kind: "workflow",
      types: w.day2.types,
      base: schemaForm(schema),
      closed: schema.additionalProperties === false,
    };
  }
  return undefined;
}

/** As the server's designWarnings: where the form and the survey or schema differ. */
function warnings(c: Custom, schema: Record<string, unknown>): string[] {
  const [of, runner] = c.kind === "workflow" ? ["the input schema", "the workflow"] : ["the survey", "AWX"];
  const base = (c.base.schema.properties ?? {}) as Record<string, Record<string, unknown>>;
  const required = (c.base.schema.required ?? []) as string[];
  const props = (schema.properties ?? {}) as Record<string, Record<string, unknown>>;
  const out = required
    .filter((n) => !props[n])
    .map((n) => `This form doesn't ask for ${n}, which ${of} requires: ${runner} may refuse the run.`);
  for (const n of Object.keys(props).sort()) {
    const was = base[n];
    if (!was && c.kind === "awx")
      out.push(
        `${n} is not in the survey: AWX passes it to the job only if the template prompts for variables on launch.`,
      );
    else if (!was && c.closed)
      out.push(`${n} is not in the input schema, which takes no other inputs: the workflow may refuse the run.`);
    else if (typeof was?.type === "string" && typeof props[n]!.type === "string" && was.type !== props[n]!.type)
      out.push(`${n} is of type ${props[n]!.type} here but ${was.type} in ${of}: ${runner} may refuse the run.`);
  }
  return out;
}

function design(db: Db, c: Custom): S["Day2FormDesign"] {
  const f = forms(db).get(c.key);
  const form = f ? asActionForm(f.definition) : c.base;
  return {
    action: c.key,
    name: c.name,
    kind: c.kind,
    types: c.types,
    definition: { form_schema: form.schema, ui_schema: form.ui_schema, initial_values: form.initial_values },
    revision: f?.revision ?? 0,
    active: f?.active ?? false,
    warnings: f ? warnings(c, form.schema) : [],
    ...(f ? { updated_at: f.updated_at, updated_by: f.updated_by } : {}),
  };
}

/** As the server's checkDay2Form: a catalog form's checks, and what a dialog's form can't do. */
function day2Problems(db: Db, d: S["Day2FormDefinition"], active: boolean): FieldError[] {
  const errors = formProblems(db, { ...d, blueprint: {} }, "/definition", active);
  if (d.ui_schema.layout.kind === "wizard")
    errors.push({
      field: "/definition/ui_schema/layout/kind",
      code: "not_allowed",
      message: "An action's form shows in a dialog: lay it out as a page or as tabs.",
    });
  for (const name of Object.keys(d.ui_schema.fields).sort()) {
    const f = d.ui_schema.fields[name]!;
    const at = `/definition/ui_schema/fields/${name}`;
    if (f.widget === "file")
      errors.push({
        field: `${at}/widget`,
        code: "not_allowed",
        message: "An action's form can't take files: ask for an address to fetch it from instead.",
      });
    else if (f.widget === "secret" || f.secret)
      errors.push({
        field: `${at}/secret`,
        code: "not_allowed",
        message:
          "An action's form can't take secrets: keep the secret in AWX or in rvav's secrets and name it instead.",
      });
  }
  return errors;
}

function problem(status: number, title: string, errors?: FieldError[], detail?: string) {
  return HttpResponse.json(
    {
      type: "https://rvav.dev/problems/x",
      title,
      status,
      request_id: "req_mock",
      ...(errors ? { errors } : {}),
      ...(detail ? { detail } : {}),
    },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

/** The deployment and VM a designer previews on, refused when the action doesn't run there. */
function previewTarget(db: Db, c: Custom, deploymentId: string, resourceId?: string) {
  const d = db.deployments.find((x) => x.id === deploymentId);
  if (!d) return { error: problem(404, "Not found", undefined, "It does not exist, or you cannot see it.") };
  const vm = resourceId
    ? d.resources.find((r): r is VmResource => r.id === resourceId && r.type === "vsphere.vm")
    : undefined;
  const type = vm ? "vsphere.vm" : "deployment";
  if ((resourceId && !vm) || !c.types.includes(type))
    return {
      error: problem(422, "Some inputs need attention", [
        {
          field: "/resource_id",
          code: "unknown_resource",
          message: c.types.includes("deployment")
            ? `${c.name} runs on the whole deployment: leave out resource_id.`
            : `${c.name} runs on a VM: choose one of the deployment's.`,
        },
      ]),
    };
  return { d, vm };
}

export function day2FormHandlers(db: Db, base: string, wait: () => Promise<void>, src: CustomSources) {
  const find = (key: string) => custom(db, src, key);
  const notFound = () => problem(404, "Not found", undefined, "There is no such AWX or workflow action.");
  // Designing an action's form is extensibility.manage's, reading it too (core.Day2Form).
  const manage = () => can(db.me, "extensibility.manage");
  const forbidden = () => problem(403, "Not allowed", undefined, "You do not have permission to do this.");
  const formOf = (c: Custom) => {
    const f = forms(db).get(c.key);
    return f ? asActionForm(f.definition) : { ...c.base, designed: true };
  };
  return [
    http.get(`${base}/admin/day2-forms`, async () => {
      await wait();
      if (!manage()) return forbidden();
      const items = [...forms(db).entries()].map(([action, f]) => ({
        action,
        active: f.active,
        revision: f.revision,
        updated_at: f.updated_at,
      }));
      return HttpResponse.json({ items } satisfies S["Day2FormList"]);
    }),
    http.get(`${base}/admin/day2-forms/:action`, async ({ params }) => {
      await wait();
      if (!manage()) return forbidden();
      const c = find(String(params.action));
      return c ? HttpResponse.json(design(db, c)) : notFound();
    }),
    http.put(`${base}/admin/day2-forms/:action`, async ({ params, request }) => {
      await wait();
      const w = (await request.json()) as S["Day2FormWrite"];
      const strict = unknownField(w, ["definition", "base_revision", "active"]);
      if (strict) return strict;
      if (!manage()) return forbidden();
      const c = find(String(params.action));
      if (!c) return notFound();
      const saved = forms(db).get(c.key);
      const active = w.active ?? saved?.active ?? false;
      const errors = day2Problems(db, w.definition, active);
      if (errors.length) return problem(422, "Some inputs need attention", errors);
      if (w.base_revision !== undefined && w.base_revision !== (saved?.revision ?? 0))
        return problem(
          409,
          "Not possible right now",
          undefined,
          "This form was saved elsewhere since you opened it: reload it to see those changes.",
        );
      forms(db).set(c.key, {
        definition: w.definition,
        active,
        revision: (saved?.revision ?? 0) + 1,
        updated_at: new Date().toISOString(),
        updated_by: db.me.user,
      });
      return HttpResponse.json(design(db, c));
    }),
    // As the server: an action with no form saved has nothing to remove, and says so with 204 too.
    http.delete(`${base}/admin/day2-forms/:action`, async ({ params }) => {
      await wait();
      if (!manage()) return forbidden();
      const c = find(String(params.action));
      if (!c) return notFound();
      forms(db).delete(c.key);
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/day2-forms/:action/preview`, async ({ params, request }) => {
      await wait();
      if (!manage()) return forbidden();
      const c = find(String(params.action));
      if (!c) return notFound();
      const q = (await request.json()) as S["Day2FormDesignerQuery"];
      const t = previewTarget(db, c, q.deployment_id, q.resource_id);
      if (t.error) return t.error;
      const form = formOf(c);
      const { fields, errors } = designedInputs(db, t.d, t.vm, c.key, form, q.inputs);
      // As core.startForm: the form starts with its initial values, else its schema's defaults.
      const started = { ...form, initial_values: withStart(requestForm(c.key, form), {}) };
      return HttpResponse.json({ form: started, fields, errors } satisfies S["Day2FormPreview"]);
    }),
    http.post(`${base}/admin/day2-forms/:action/values`, async ({ params, request }) => {
      await wait();
      if (!manage()) return forbidden();
      const c = find(String(params.action));
      if (!c) return notFound();
      const q = (await request.json()) as S["Day2FormDesignerValuesQuery"];
      const t = previewTarget(db, c, q.deployment_id, q.resource_id);
      if (t.error) return t.error;
      let out: S["FieldValues"] | null;
      try {
        out = designedValues(db, t.d, t.vm, c.key, formOf(c), q.field, q.inputs ?? {}, q.search);
      } catch (e) {
        return problem(400, "Bad request", undefined, `The expression failed: ${String(e)}`);
      }
      return out
        ? HttpResponse.json(out)
        : problem(400, "Bad request", undefined, `Field “${q.field}” has no value source`);
    }),
  ];
}
