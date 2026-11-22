// Property groups (ADR-0038): inputs and constants several items share. A
// save makes the next version; an item's blueprint names the version it uses.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Boxes, Plus, X } from "lucide-react";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { failed, fieldErrors, keyProblems } from "@/features/admin/governance/common";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { keys } from "@/lib/query";
import { relative } from "@/lib/format";
import { i18n } from "@/lib/i18n";

type Group = components["schemas"]["PropertyGroup"];
type GroupInput = components["schemas"]["PropertyGroupInput"];
type InputType = GroupInput["type"];

const tr = i18n.getFixedT(null, "admin-catalog");

const groupsKey = [...keys.catalogAdmin.all, "property-groups"] as const;
const inputTypes = ["string", "integer", "number", "boolean"] as const satisfies readonly InputType[];
const typeLabel = (type: InputType) => tr(`groups.type.${type}`);
const inputFields = ["name", "label", "type", "default", "options", "secret", "expose_to_inventory", "help"] as const;

// Code placeholders and examples, the same in every language.
const keySlot = "<key>";
const nameSlot = "<name>";
const blueprintExample = "property_groups: [{key: network, version: 2}]";

/** Code inside a sentence: Trans leaves its text alone. */
const Expr = ({ text }: { text: string }) => <code className="font-mono">{text}</code>;
// Radix's select has no empty value: "no default" stands as this.
const none = "*";

/** An input as the sheet edits it: its default and options as typed. */
interface InputRow {
  name: string;
  label: string;
  type: InputType;
  required: boolean;
  secret: boolean;
  inventory: boolean;
  default: string;
  options: string;
  help: string;
}

type ConstantType = "string" | "number" | "boolean";

/** A constant as typed: its value as text, and the type it is saved as. */
interface ConstantRow {
  key: string;
  type: ConstantType;
  value: string;
}

const constantTypes = (): { value: ConstantType; label: string }[] =>
  (["string", "number", "boolean"] as const).map((value) => ({ value, label: typeLabel(value) }));

function asText(v: unknown): string {
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return v === undefined || v === null ? "" : JSON.stringify(v);
}

const toRow = (i: GroupInput): InputRow => ({
  name: i.name,
  label: i.label,
  type: i.type,
  required: Boolean(i.required),
  secret: Boolean(i.secret),
  inventory: Boolean(i.expose_to_inventory),
  default: asText(i.default),
  options: (i.options ?? []).join(", "),
  help: i.help ?? "",
});

function fromRow(r: InputRow): GroupInput {
  const options = r.options
    .split(",")
    .map((o) => o.trim())
    .filter(Boolean);
  const out: GroupInput = { name: r.name.trim(), label: r.label.trim(), type: r.type };
  if (r.required) out.required = true;
  if (r.type === "string" && r.secret) out.secret = true;
  if (r.type === "string" && options.length) out.options = options;
  if (r.inventory && !out.secret) out.expose_to_inventory = true;
  if (r.help.trim()) out.help = r.help.trim();
  const d = r.default.trim();
  if (d && !out.secret) {
    if (r.type === "boolean") out.default = d === "true";
    else if (r.type === "string") out.default = d;
    else out.default = Number.isNaN(Number(d)) ? d : Number(d);
  }
  return out;
}

const toConstant = ([key, v]: [string, unknown]): ConstantRow => ({
  key,
  type: typeof v === "number" ? "number" : typeof v === "boolean" ? "boolean" : "string",
  value: asText(v),
});

const isNumber = (s: string) => s.trim() !== "" && Number.isFinite(Number(s.trim()));

function constantValue(c: ConstantRow): string | number | boolean {
  if (c.type === "number") return Number(c.value.trim());
  if (c.type === "boolean") return c.value === "true";
  return c.value;
}

/** What is wrong with the constants before they are sent, by `<row>/key` and `<row>/value`. */
function constantProblems(rows: ConstantRow[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [i, why] of Object.entries(keyProblems(rows, (c) => c.value === "", tr("groups.constantName"))))
    out[`${i}/key`] = why;
  rows.forEach((c, i) => {
    if (c.type === "number" && c.key.trim() && !isNumber(c.value)) out[`${i}/value`] = tr("groups.enterNumber");
  });
  return out;
}

/** The server's problems no field of the sheet shows. */
function unplaced(errors: Record<string, string>, inputs: number, constants: ConstantRow[]): string[] {
  const keys = new Set(constants.map((c) => `/constants/${c.key.trim()}`));
  return Object.entries(errors)
    .filter(([at]) => {
      if (["/key", "/name", "/description"].includes(at) || keys.has(at)) return false;
      const m = /^\/inputs\/(\d+)\/(name|label|type|default|options|secret|expose_to_inventory|help)$/.exec(at);
      return !(m && Number(m[1]) < inputs);
    })
    .map(([, why]) => why);
}

function InputEditor({
  row,
  index,
  errors,
  onChange,
  onRemove,
}: {
  row: InputRow;
  index: number;
  errors: Record<string, string>;
  onChange: (r: InputRow) => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation("admin-catalog");
  const id = (f: string) => `pg-input-${index}-${f}`;
  const err = Object.fromEntries(inputFields.map((f) => [f, errors[`/inputs/${index}/${f}`]])) as Partial<
    Record<(typeof inputFields)[number], string>
  >;
  const text = row.type === "string";
  return (
    <div className="border-border flex flex-col gap-3 rounded-md border p-3">
      <div className="grid grid-cols-2 gap-3">
        <Field label={t("groups.name")} htmlFor={id("name")} error={err.name}>
          <Input
            id={id("name")}
            className="font-mono"
            value={row.name}
            onChange={(e) => onChange({ ...row, name: e.target.value })}
          />
        </Field>
        <Field label={t("groups.label")} htmlFor={id("label")} error={err.label}>
          <Input id={id("label")} value={row.label} onChange={(e) => onChange({ ...row, label: e.target.value })} />
        </Field>
        <Field label={t("groups.typeLabel")} htmlFor={id("type")} error={err.type}>
          <Select
            id={id("type")}
            value={row.type}
            onChange={(v) =>
              onChange(
                v === "string"
                  ? { ...row, type: v, default: "" }
                  : { ...row, type: v as InputType, default: "", secret: false, options: "" },
              )
            }
            options={inputTypes.map((value) => ({ value, label: typeLabel(value) }))}
          />
        </Field>
        {!row.secret && (
          <Field label={t("groups.default")} htmlFor={id("default")} error={err.default}>
            {row.type === "boolean" ? (
              <Select
                id={id("default")}
                value={row.default || none}
                onChange={(v) => onChange({ ...row, default: v === none ? "" : v })}
                options={[
                  { value: none, label: t("groups.none") },
                  { value: "true", label: t("groups.yes") },
                  { value: "false", label: t("groups.no") },
                ]}
              />
            ) : (
              <Input
                id={id("default")}
                inputMode={text ? undefined : "decimal"}
                value={row.default}
                onChange={(e) => onChange({ ...row, default: e.target.value })}
              />
            )}
          </Field>
        )}
      </div>
      {text && !row.secret && (
        <Field label={t("groups.options")} htmlFor={id("options")} hint={t("groups.optionsHint")} error={err.options}>
          <Input
            id={id("options")}
            value={row.options}
            onChange={(e) => onChange({ ...row, options: e.target.value })}
          />
        </Field>
      )}
      <Field label={t("groups.help")} htmlFor={id("help")} hint={t("groups.helpHint")} error={err.help}>
        <Input id={id("help")} value={row.help} onChange={(e) => onChange({ ...row, help: e.target.value })} />
      </Field>
      <div className="flex flex-wrap items-center gap-4">
        <label htmlFor={id("required")} className="flex items-center gap-2 text-body">
          <Checkbox id={id("required")} checked={row.required} onChange={(v) => onChange({ ...row, required: v })} />
          {t("groups.required")}
        </label>
        {text && (
          <label htmlFor={id("secret")} className="flex items-center gap-2 text-body">
            <Checkbox
              id={id("secret")}
              checked={row.secret}
              onChange={(v) =>
                onChange({
                  ...row,
                  secret: v,
                  inventory: v ? false : row.inventory,
                  default: v ? "" : row.default,
                  options: v ? "" : row.options,
                })
              }
            />
            {t("groups.secret")}
          </label>
        )}
        {!row.secret && (
          <label htmlFor={id("inventory")} className="flex items-center gap-2 text-body">
            <Checkbox
              id={id("inventory")}
              checked={row.inventory}
              onChange={(v) => onChange({ ...row, inventory: v })}
            />
            {t("groups.inventory")}
          </label>
        )}
        {err.secret && <span className="text-secondary text-bad">{err.secret}</span>}
        {err.expose_to_inventory && <span className="text-secondary text-bad">{err.expose_to_inventory}</span>}
        <Button
          type="button"
          size="sm"
          variant="quiet"
          className="ml-auto"
          aria-label={t("groups.removeInput", { name: row.name || index + 1 })}
          onClick={onRemove}
        >
          <X className="size-3.5" /> {t("groups.remove")}
        </Button>
      </div>
    </div>
  );
}

function GroupSheet({ existing, onClose }: { existing?: Group; onClose: () => void }) {
  const { t } = useTranslation("admin-catalog");
  const qc = useQueryClient();
  const [key, setKey] = useState(existing?.key ?? "");
  const [name, setName] = useState(existing?.name ?? "");
  const [description, setDescription] = useState(existing?.description ?? "");
  const [inputs, setInputs] = useState<InputRow[]>((existing?.inputs ?? []).map(toRow));
  const [constants, setConstants] = useState<ConstantRow[]>(Object.entries(existing?.constants ?? {}).map(toConstant));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [local, setLocal] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const body = {
        name,
        description,
        inputs: inputs.map(fromRow),
        constants: Object.fromEntries(
          constants.filter((c) => c.key.trim()).map((c) => [c.key.trim(), constantValue(c)]),
        ),
      };
      return existing
        ? call(api.PUT("/admin/property-groups/{key}", { params: { path: { key: existing.key } }, body }))
        : call(api.POST("/admin/property-groups", { body: { ...body, key } }));
    },
    onSuccess: (g) => {
      void qc.invalidateQueries({ queryKey: groupsKey });
      toast.success(t("groups.saved", { name: g.name, version: g.version }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, t("groups.notSaved"));
    },
  });
  const setInput = (i: number, r: InputRow) => setInputs((xs) => xs.map((x, j) => (j === i ? r : x)));
  const setConstant = (i: number, c: ConstantRow) => setConstants((xs) => xs.map((x, j) => (j === i ? c : x)));
  const elsewhere = unplaced(errors, inputs.length, constants);
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={existing ? t("groups.change", { name: existing.name }) : t("groups.new")}
      description={
        existing ? t("groups.changeLead", { version: Math.max(...existing.versions) + 1 }) : t("groups.newLead")
      }
      submitLabel={save.isPending ? t("groups.saving") : existing ? t("groups.saveVersion") : t("groups.add")}
      submitDisabled={!name.trim() || (!existing && !key)}
      onSubmit={() => {
        setErrors({});
        const problems = constantProblems(constants);
        setLocal(problems);
        if (Object.keys(problems).length === 0) save.mutate();
      }}
    >
      {elsewhere.length > 0 && (
        <div role="alert" className="rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-secondary text-bad">
          {elsewhere.map((why) => (
            <p key={why}>{why}</p>
          ))}
        </div>
      )}
      <Field
        label={t("groups.key")}
        htmlFor="pg-key"
        hint={
          <Trans
            t={t}
            i18nKey="groups.keyHint"
            components={{ expr: <Expr text={`propgroup.${key || keySlot}.${nameSlot}`} /> }}
          />
        }
        error={errors["/key"]}
      >
        <Input
          id="pg-key"
          className="font-mono"
          value={key}
          disabled={Boolean(existing)}
          onChange={(e) => setKey(e.target.value)}
        />
      </Field>
      <Field label={t("groups.name")} htmlFor="pg-name" error={errors["/name"]}>
        <Input id="pg-name" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label={t("groups.description")} htmlFor="pg-description" error={errors["/description"]}>
        <Input
          id="pg-description"
          value={description}
          maxLength={500}
          onChange={(e) => setDescription(e.target.value)}
        />
      </Field>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-secondary font-medium">{t("groups.inputs")}</legend>
        <p className="text-secondary text-muted">{t("groups.inputsLead")}</p>
        {inputs.map((r, i) => (
          <InputEditor
            key={i}
            row={r}
            index={i}
            errors={errors}
            onChange={(next) => setInput(i, next)}
            onRemove={() => setInputs((xs) => xs.filter((_, j) => j !== i))}
          />
        ))}
        <div>
          <Button
            type="button"
            variant="secondary"
            onClick={() =>
              setInputs((xs) => [
                ...xs,
                {
                  name: "",
                  label: "",
                  type: "string",
                  required: false,
                  secret: false,
                  inventory: false,
                  default: "",
                  options: "",
                  help: "",
                },
              ])
            }
          >
            <Plus className="size-3.5" /> {t("groups.addInput")}
          </Button>
        </div>
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-secondary font-medium">{t("groups.constants")}</legend>
        <p className="text-secondary text-muted">{t("groups.constantsLead")}</p>
        {constants.map((c, i) => (
          <div key={i} className="grid grid-cols-[1fr_8rem_1.4fr_auto] items-start gap-2">
            <Field
              label={t("groups.name")}
              htmlFor={`pg-const-${i}-key`}
              error={local[`${i}/key`] ?? errors[`/constants/${c.key.trim()}`]}
            >
              <Input
                id={`pg-const-${i}-key`}
                className="font-mono"
                value={c.key}
                onChange={(e) => setConstant(i, { ...c, key: e.target.value })}
              />
            </Field>
            <Field label={t("groups.typeLabel")} htmlFor={`pg-const-${i}-type`}>
              <Select
                id={`pg-const-${i}-type`}
                value={c.type}
                onChange={(v) =>
                  setConstant(i, {
                    ...c,
                    type: v as ConstantType,
                    value: v === "boolean" ? String(c.value === "true") : c.value,
                  })
                }
                options={constantTypes()}
              />
            </Field>
            <Field label={t("groups.value")} htmlFor={`pg-const-${i}-value`} error={local[`${i}/value`]}>
              {c.type === "boolean" ? (
                <Select
                  id={`pg-const-${i}-value`}
                  value={c.value}
                  onChange={(v) => setConstant(i, { ...c, value: v })}
                  options={[
                    { value: "true", label: t("groups.yes") },
                    { value: "false", label: t("groups.no") },
                  ]}
                />
              ) : (
                <Input
                  id={`pg-const-${i}-value`}
                  inputMode={c.type === "number" ? "decimal" : undefined}
                  value={c.value}
                  onChange={(e) => setConstant(i, { ...c, value: e.target.value })}
                />
              )}
            </Field>
            <Button
              type="button"
              size="icon"
              variant="quiet"
              className="mt-6"
              aria-label={t("groups.removeConstant", { name: c.key || i + 1 })}
              onClick={() => {
                setLocal({});
                setConstants((xs) => xs.filter((_, j) => j !== i));
              }}
            >
              <X className="size-3.5" />
            </Button>
          </div>
        ))}
        <div>
          <Button
            type="button"
            variant="secondary"
            onClick={() => setConstants((xs) => [...xs, { key: "", type: "string", value: "" }])}
          >
            <Plus className="size-3.5" /> {t("groups.addConstant")}
          </Button>
        </div>
      </fieldset>
    </Sheet>
  );
}

const names = (xs: string[]) => (xs.length ? xs.join(", ") : tr("groups.none"));

const columns = (): Column<Group>[] => [
  {
    id: "name",
    header: tr("groups.col.name"),
    sortValue: (g) => g.name,
    cell: (g) => (
      <>
        <div className="font-medium">{g.name}</div>
        <div className="font-mono text-caption text-muted">{g.key}</div>
      </>
    ),
  },
  {
    id: "version",
    header: tr("groups.col.version"),
    sortValue: (g) => g.version,
    cell: (g) => <span className="text-secondary text-muted">{g.version}</span>,
  },
  {
    id: "inputs",
    header: tr("groups.col.inputs"),
    cell: (g) => <span className="font-mono text-caption">{names(g.inputs.map((i) => i.name))}</span>,
  },
  {
    id: "constants",
    header: tr("groups.col.constants"),
    cell: (g) => <span className="font-mono text-caption">{names(Object.keys(g.constants))}</span>,
  },
  {
    id: "used",
    header: tr("groups.col.used"),
    sortValue: (g) => g.used_by.length,
    cell: (g) => (
      <span className="text-secondary text-muted">
        {g.used_by.length ? g.used_by.join(", ") : tr("groups.nothing")}
      </span>
    ),
  },
  {
    id: "changed",
    header: tr("groups.col.changed"),
    sortValue: (g) => g.updated_at,
    cell: (g) => <span className="text-secondary text-muted">{relative(g.updated_at)}</span>,
  },
];

export function PropertyGroupsPage() {
  const { t } = useTranslation("admin-catalog");
  const qc = useQueryClient();
  const groups = useQuery({
    queryKey: groupsKey,
    queryFn: ({ signal }) => call(api.GET("/admin/property-groups", { signal })),
  });
  const [editing, setEditing] = useState<Group | "new" | null>(null);
  const [removing, setRemoving] = useState<Group | null>(null);
  const remove = useMutation({
    mutationFn: (g: Group) => call(api.DELETE("/admin/property-groups/{key}", { params: { path: { key: g.key } } })),
    onSuccess: (_, g) => {
      void qc.invalidateQueries({ queryKey: groupsKey });
      toast.success(t("groups.deleted", { name: g.name }));
      setRemoving(null);
    },
    onError: (e) => failed(e, t("groups.notDeleted")),
  });
  const list = groups.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("groups.new")}
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title={t("groups.title")}
        description={
          <Trans
            t={t}
            i18nKey="groups.lead"
            components={{
              blueprint: <Expr text={blueprintExample} />,
              expr: <Expr text={`propgroup.network.${nameSlot}`} />,
            }}
          />
        }
      />
      <Panel title={t("groups.panel")} flush actions={list?.length ? add : undefined}>
        <DataTable
          label={t("groups.title")}
          columns={columns()}
          rows={list}
          rowKey={(g) => g.key}
          minWidth={820}
          loading={groups.isPending}
          error={groups.isError && <ErrorState error={groups.error} onRetry={() => void groups.refetch()} />}
          empty={<EmptyState icon={Boxes} title={t("groups.empty")} body={t("groups.emptyBody")} action={add} />}
          rowMenuLabel={(g) => t("groups.actionsFor", { name: g.name })}
          rowMenu={(g) => (
            <>
              <MenuItem onSelect={() => setEditing(g)}>{t("groups.changeAction")}</MenuItem>
              <MenuSeparator />
              <MenuItem danger disabled={g.used_by.length > 0} onSelect={() => setRemoving(g)}>
                {g.used_by.length > 0 ? t("groups.deleteInUse") : t("groups.delete")}
              </MenuItem>
            </>
          )}
        />
      </Panel>
      {editing && <GroupSheet existing={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("groups.deleteTitle", { name: removing.name })}
          description={t("groups.deleteBody")}
          consequences={t("groups.deleteConsequences")}
          confirmLabel={t("groups.deleteConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      )}
    </Page>
  );
}
