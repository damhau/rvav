// Property groups (ADR-0038): inputs and constants several items share. A
// save makes the next version; an item's blueprint names the version it uses.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Boxes, Plus, X } from "lucide-react";
import { useState } from "react";
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
import { relative } from "@/lib/utils";

type Group = components["schemas"]["PropertyGroup"];
type GroupInput = components["schemas"]["PropertyGroupInput"];
type InputType = GroupInput["type"];

const groupsKey = [...keys.catalogAdmin.all, "property-groups"] as const;
const typeLabel: Record<InputType, string> = {
  string: "Text",
  integer: "Whole number",
  number: "Number",
  boolean: "Yes or no",
};
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

const constantTypes: { value: ConstantType; label: string }[] = [
  { value: "string", label: "Text" },
  { value: "number", label: "Number" },
  { value: "boolean", label: "Yes or no" },
];

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
  for (const [i, why] of Object.entries(keyProblems(rows, (c) => c.value === "", "Give the constant a name.")))
    out[`${i}/key`] = why;
  rows.forEach((c, i) => {
    if (c.type === "number" && c.key.trim() && !isNumber(c.value)) out[`${i}/value`] = "Enter a number, such as 300.";
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
  const id = (f: string) => `pg-input-${index}-${f}`;
  const err = (f: string) => errors[`/inputs/${index}/${f}`];
  const text = row.type === "string";
  return (
    <div className="border-border flex flex-col gap-3 rounded-md border p-3">
      <div className="grid grid-cols-2 gap-3">
        <Field label="Name" htmlFor={id("name")} error={err("name")}>
          <Input
            id={id("name")}
            className="font-mono"
            value={row.name}
            onChange={(e) => onChange({ ...row, name: e.target.value })}
          />
        </Field>
        <Field label="Label" htmlFor={id("label")} error={err("label")}>
          <Input id={id("label")} value={row.label} onChange={(e) => onChange({ ...row, label: e.target.value })} />
        </Field>
        <Field label="Type" htmlFor={id("type")} error={err("type")}>
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
            options={Object.entries(typeLabel).map(([value, label]) => ({ value, label }))}
          />
        </Field>
        {!row.secret && (
          <Field label="Default" htmlFor={id("default")} error={err("default")}>
            {row.type === "boolean" ? (
              <Select
                id={id("default")}
                value={row.default || none}
                onChange={(v) => onChange({ ...row, default: v === none ? "" : v })}
                options={[
                  { value: none, label: "None" },
                  { value: "true", label: "Yes" },
                  { value: "false", label: "No" },
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
        <Field
          label="Options"
          htmlFor={id("options")}
          hint="Separated by commas. Requesters choose one; none lets them type."
          error={err("options")}
        >
          <Input
            id={id("options")}
            value={row.options}
            onChange={(e) => onChange({ ...row, options: e.target.value })}
          />
        </Field>
      )}
      <Field label="Help" htmlFor={id("help")} hint="Shown under the field on the request form." error={err("help")}>
        <Input id={id("help")} value={row.help} onChange={(e) => onChange({ ...row, help: e.target.value })} />
      </Field>
      <div className="flex flex-wrap items-center gap-4">
        <label htmlFor={id("required")} className="flex items-center gap-2 text-body">
          <Checkbox id={id("required")} checked={row.required} onChange={(v) => onChange({ ...row, required: v })} />
          Required
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
            Secret
          </label>
        )}
        {!row.secret && (
          <label htmlFor={id("inventory")} className="flex items-center gap-2 text-body">
            <Checkbox
              id={id("inventory")}
              checked={row.inventory}
              onChange={(v) => onChange({ ...row, inventory: v })}
            />
            A host variable in the AWX inventory
          </label>
        )}
        {err("secret") && <span className="text-secondary text-bad">{err("secret")}</span>}
        {err("expose_to_inventory") && <span className="text-secondary text-bad">{err("expose_to_inventory")}</span>}
        <Button
          type="button"
          size="sm"
          variant="quiet"
          className="ml-auto"
          aria-label={`Remove input ${row.name || index + 1}`}
          onClick={onRemove}
        >
          <X className="size-3.5" /> Remove
        </Button>
      </div>
    </div>
  );
}

function GroupSheet({ existing, onClose }: { existing?: Group; onClose: () => void }) {
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
      toast.success(`${g.name} saved as version ${g.version}`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, "The property group wasn't saved");
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
      title={existing ? `Change ${existing.name}` : "New property group"}
      description={
        existing
          ? `Saving makes version ${Math.max(...existing.versions) + 1}. Items keep the version they name until their blueprint names this one.`
          : "Inputs and constants several items share. An item's blueprint names a version of the group."
      }
      submitLabel={save.isPending ? "Saving…" : existing ? "Save as a new version" : "Add"}
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
        label="Key"
        htmlFor="pg-key"
        hint={
          <>
            Blueprints name it, and bindings read its constants as{" "}
            <code className="font-mono">propgroup.{key || "<key>"}.&lt;name&gt;</code>.
          </>
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
      <Field label="Name" htmlFor="pg-name" error={errors["/name"]}>
        <Input id="pg-name" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Description" htmlFor="pg-description" error={errors["/description"]}>
        <Input
          id="pg-description"
          value={description}
          maxLength={500}
          onChange={(e) => setDescription(e.target.value)}
        />
      </Field>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-secondary font-medium">Inputs</legend>
        <p className="text-secondary text-muted">
          Each joins the request form of the items naming the group, in a section of its own, as if the item declared
          it.
        </p>
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
            <Plus className="size-3.5" /> Add input
          </Button>
        </div>
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-secondary font-medium">Constants</legend>
        <p className="text-secondary text-muted">Values the items' bindings read; requesters don't see them.</p>
        {constants.map((c, i) => (
          <div key={i} className="grid grid-cols-[1fr_8rem_1.4fr_auto] items-start gap-2">
            <Field
              label="Name"
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
            <Field label="Type" htmlFor={`pg-const-${i}-type`}>
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
                options={constantTypes}
              />
            </Field>
            <Field label="Value" htmlFor={`pg-const-${i}-value`} error={local[`${i}/value`]}>
              {c.type === "boolean" ? (
                <Select
                  id={`pg-const-${i}-value`}
                  value={c.value}
                  onChange={(v) => setConstant(i, { ...c, value: v })}
                  options={[
                    { value: "true", label: "Yes" },
                    { value: "false", label: "No" },
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
              aria-label={`Remove constant ${c.key || i + 1}`}
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
            <Plus className="size-3.5" /> Add constant
          </Button>
        </div>
      </fieldset>
    </Sheet>
  );
}

const names = (xs: string[]) => (xs.length ? xs.join(", ") : "None");

const columns: Column<Group>[] = [
  {
    id: "name",
    header: "Name",
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
    header: "Latest version",
    sortValue: (g) => g.version,
    cell: (g) => <span className="text-secondary text-muted">{g.version}</span>,
  },
  {
    id: "inputs",
    header: "Inputs",
    cell: (g) => <span className="font-mono text-caption">{names(g.inputs.map((i) => i.name))}</span>,
  },
  {
    id: "constants",
    header: "Constants",
    cell: (g) => <span className="font-mono text-caption">{names(Object.keys(g.constants))}</span>,
  },
  {
    id: "used",
    header: "Used by",
    sortValue: (g) => g.used_by.length,
    cell: (g) => (
      <span className="text-secondary text-muted">{g.used_by.length ? g.used_by.join(", ") : "Nothing"}</span>
    ),
  },
  {
    id: "changed",
    header: "Changed",
    sortValue: (g) => g.updated_at,
    cell: (g) => <span className="text-secondary text-muted">{relative(g.updated_at)}</span>,
  },
];

export function PropertyGroupsPage() {
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
      toast.success(`${g.name} deleted`);
      setRemoving(null);
    },
    onError: (e) => failed(e, "The property group wasn't deleted"),
  });
  const list = groups.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New property group
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title="Property groups"
        description={
          <>
            Inputs and constants several catalog items share. An item names a version of a group in its blueprint, as{" "}
            <code className="font-mono">property_groups: [{"{key: network, version: 2}"}]</code>; the group's inputs
            join its request form, and its bindings read the constants as{" "}
            <code className="font-mono">propgroup.network.&lt;name&gt;</code>.
          </>
        }
      />
      <Panel title="Groups" flush actions={list?.length ? add : undefined}>
        <DataTable
          label="Property groups"
          columns={columns}
          rows={list}
          rowKey={(g) => g.key}
          minWidth={820}
          loading={groups.isPending}
          error={groups.isError && <ErrorState error={groups.error} onRetry={() => void groups.refetch()} />}
          empty={
            <EmptyState
              icon={Boxes}
              title="No property groups yet"
              body="Gather the inputs and values several items ask for or use, such as a cost centre or a DNS domain, once."
              action={add}
            />
          }
          rowMenuLabel={(g) => `Actions for ${g.name}`}
          rowMenu={(g) => (
            <>
              <MenuItem onSelect={() => setEditing(g)}>Change</MenuItem>
              <MenuSeparator />
              <MenuItem danger disabled={g.used_by.length > 0} onSelect={() => setRemoving(g)}>
                {g.used_by.length > 0 ? "Delete: in use" : "Delete"}
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
          title={`Delete ${removing.name}?`}
          description="Every version of the group is deleted."
          consequences="No published item uses it. A draft that names it is refused when next saved."
          confirmLabel="Delete property group"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      )}
    </Page>
  );
}
