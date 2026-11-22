// The VM designer (ADR-0032, ADR-0066): a VM item's draft drawn on the
// canvas and edited there. The palette adds a VM, a network, a segment, a
// group, a disk or a custom resource, dragged onto the canvas or chosen and
// added with its dialog; a connection is drawn from a node's dot to another,
// or chosen in the node's details, which edit its fields too. Each edit
// writes the YAML through ./edits as one step of the editor's history, and
// the canvas follows the YAML. YAML the canvas can't tell apart
// (edits.designable) leaves it read-only, saying why.
import { useQuery } from "@tanstack/react-query";
import {
  HardDrive,
  Link2,
  Network,
  Server,
  Shapes,
  Shield,
  ShieldCheck,
  Trash2,
  Unlink,
  Waypoints,
} from "lucide-react";
import { type ElementType, type ReactNode, type RefObject, useCallback, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { type CanvasEdge, type CanvasGraph, type CanvasNode, NodeDetails, vmGraph, within } from "@/components/canvas";
import {
  type Design,
  HistoryButtons,
  legendClass,
  PanelHeading,
  panelClass,
  type Then,
  useDesigner,
  useFocusNext,
} from "@/components/canvas/designer";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";
import { type Option, Select } from "@/components/ui/select";
import { adm } from "@/lib/query";
import DefinitionGraph from "../DefinitionGraph";
import { CommitInput } from "../designer/Properties";
import * as edits from "./edits";

type Raw = Record<string, unknown>;
const str = (v: unknown) => (typeof v === "string" ? v : "");
const obj = (v: unknown): Raw => (v && typeof v === "object" && !Array.isArray(v) ? (v as Raw) : {});
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const shown = (v: unknown) => (typeof v === "string" ? v : typeof v === "number" ? String(v) : "");

/** What the palette adds. */
type Part = "component" | "network" | "segment" | "sg" | "nsx" | "disk" | "resource";
const parts: Part[] = ["component", "network", "segment", "sg", "nsx", "disk", "resource"];
const partIcon: Record<Part, ElementType> = {
  component: Server,
  network: Network,
  segment: Waypoints,
  sg: Shield,
  nsx: ShieldCheck,
  disk: HardDrive,
  resource: Shapes,
};
/** A palette entry dragged onto the canvas carries its part under this type. */
export const partType = "application/x-rvav-vm-part";
const isPart = (v: string): v is Part => (parts as string[]).includes(v);
/** The parts that are added connected to a VM: a network or a disk is one's. */
const needsVm = (p: Part) => p === "network" || p === "nsx" || p === "disk";
const none = "none";
/** CEL for the network the requester chooses: the network field's example. */
const requestedNetwork = "inputs.network";

const isMulti = (bp: Raw) => arr(bp.components).length > 0;

/** The heading a node is listed under in Connect to. */
function groupOf(id: string): Part | "other" {
  const k = edits.kindOf(id);
  if (k === "auto") return "network";
  if (k === "chosen") return "nsx";
  return isPart(k) ? k : "other";
}

interface Adding {
  part: Part;
  /** The VM it is connected to at once: the one it was dropped on, or the one selected. */
  to?: string;
}

interface Ctx {
  text: string;
  bp: Raw;
  graph: CanvasGraph;
  apply: (make: () => string, then?: Then) => boolean;
  focusNext: RefObject<string | null>;
  vms: Option[];
}

export default function VmDesigner({ name, design }: { name: string; design: Design }) {
  const { t } = useTranslation("admin-catalog");
  const { text } = design;
  const [selected, setSelected] = useState<string | null>(null);
  const [adding, setAdding] = useState<Adding | null>(null);
  const readOnly = useMemo(() => edits.designable(text), [text]);
  const bp = useMemo(() => edits.blueprintOfText(text), [text]);
  const graph = useMemo(() => vmGraph(bp, edits.prefix, { designed: true }), [bp]);
  const vms = useMemo(() => vmOptions(bp, t("vmDesign.theVmOption")), [bp, t]);
  const { box, focusNext, apply } = useDesigner(design, setSelected);

  const server = design.serverProblems;
  // The blueprint's only: the rest are on their lines in the editor.
  const problemsFor = useCallback(
    (shown: string) =>
      server && server.text === shown ? server.errors.filter((e) => within(e.field, edits.prefix)) : [],
    [server],
  );

  const add = (part: Part, to?: string | null) => {
    const vm = to && edits.kindOf(to) === "component" ? to : undefined;
    const chosen = selected && edits.kindOf(selected) === "component" ? selected : undefined;
    setAdding({ part, to: vm ?? chosen });
  };

  const ctx: Ctx = { text, bp, graph, apply, focusNext, vms };
  const renderDetails = (node: CanvasNode) =>
    readOnly ? <NodeDetails node={node} /> : <NodePanel key={node.id} node={node} ctx={ctx} />;

  return (
    <div ref={box} tabIndex={-1} className="min-w-0 outline-none">
      <DefinitionGraph
        text={text}
        name={name}
        composite={false}
        designed
        problemsFor={problemsFor}
        toolbar={
          readOnly ? (
            <p role="note" className="text-secondary text-warn">
              {t("vmDesign.readOnly.note", { reason: readOnly })}
            </p>
          ) : (
            <Palette onAdd={(p) => add(p)} />
          )
        }
        canvas={{
          selected,
          onSelect: setSelected,
          renderDetails,
          actions: <HistoryButtons design={design} />,
          design: readOnly
            ? undefined
            : {
                onConnect: (source, target) => apply(() => edits.connect(text, source, target)),
                drop: { type: partType, onDrop: (v, target) => isPart(v) && add(v, target) },
              },
        }}
      />
      {!readOnly && adding && (
        <AddDialog adding={adding} onClose={() => setAdding(null)} text={text} bp={bp} vms={vms} apply={apply} />
      )}
    </div>
  );
}

function vmOptions(bp: Raw, theVm: string): Option[] {
  if (!isMulti(bp)) return [{ value: edits.componentId(edits.singleKey), label: theVm }];
  return arr(bp.components)
    .map(obj)
    .filter((c) => str(c.key))
    .map((c) => ({
      value: edits.componentId(str(c.key)),
      label: str(c.title) || str(c.key),
      meta: str(c.title) ? str(c.key) : undefined,
    }));
}

function Palette({ onAdd }: { onAdd: (p: Part) => void }) {
  const { t } = useTranslation("admin-catalog");
  return (
    <div className="flex flex-col gap-1">
      <div
        role="group"
        aria-labelledby="vd-palette"
        aria-describedby="vd-palette-hint"
        className="flex flex-wrap gap-1"
      >
        <span id="vd-palette" className="sr-only">
          {t("vmDesign.palette.label")}
        </span>
        {parts.map((p) => {
          const Icon = partIcon[p];
          return (
            <Button
              key={p}
              size="sm"
              variant="quiet"
              draggable
              onDragStart={(e) => {
                e.dataTransfer.setData(partType, p);
                e.dataTransfer.effectAllowed = "copy";
              }}
              onClick={() => onAdd(p)}
              className="cursor-grab border border-line"
            >
              <Icon aria-hidden />
              {t(`vmDesign.part.${p}`)}
            </Button>
          );
        })}
      </div>
      <p id="vd-palette-hint" className="text-secondary text-muted">
        {t("vmDesign.palette.hint")}
      </p>
    </div>
  );
}

/** The node a part just added is drawn as. */
function addedId(part: Part, f: Form, bp: Raw): string {
  switch (part) {
    case "component":
      return edits.componentId(f.key.trim());
    case "network":
      return `network:${f.network.trim()}`;
    case "segment":
      return `segment:${f.key.trim()}`;
    case "sg":
      return `sg:${f.key.trim()}`;
    case "nsx":
      return `nsx:${f.nsx.trim()}`;
    case "resource":
      return `resource:${f.key.trim()}`;
    case "disk": {
      const key = f.to.slice("component:".length);
      return `disk:${key}:${arr(edits.resolved(bp, key)?.disks).length}`;
    }
  }
}

interface Form {
  key: string;
  first: string;
  title: string;
  count: string;
  network: string;
  nsx: string;
  size: string;
  type: string;
  to: string;
}

function startForm(part: Part, text: string, bp: Raw, vms: Option[], to?: string): Form {
  const single = !isMulti(bp);
  const key =
    part === "component"
      ? single
        ? "vm2"
        : edits.freeKey(text, "vm", "component")
      : part === "segment"
        ? edits.freeKey(text, "segment", "segment")
        : part === "sg"
          ? edits.freeKey(text, "group", "sg")
          : part === "resource"
            ? edits.freeKey(text, "resource", "resource")
            : "";
  return {
    key,
    first: "vm1",
    title: "",
    count: "1",
    network: "",
    nsx: "",
    size: "",
    type: "",
    to: to ?? (needsVm(part) ? (vms[0]?.value ?? "") : none),
  };
}

function AddDialog({
  adding,
  onClose,
  text,
  bp,
  vms,
  apply,
}: {
  adding: Adding;
  onClose: () => void;
  text: string;
  bp: Raw;
  vms: Option[];
  apply: Ctx["apply"];
}) {
  const { t } = useTranslation(["admin-catalog", "common"]);
  const { part } = adding;
  const [f, setF] = useState(() => startForm(part, text, bp, vms, adding.to));
  const [error, setError] = useState<string | null>(null);
  const added = useRef(false);
  const types = useQuery({ ...adm.resourceTypes(), enabled: part === "resource", retry: false });
  const set = (k: keyof Form) => (v: string) => setF((x) => ({ ...x, [k]: v }));
  const single = !isMulti(bp);
  const to = f.to === none ? undefined : f.to;

  const make = (): string => {
    switch (part) {
      case "component":
        return edits.addComponent(text, {
          key: f.key.trim(),
          title: f.title,
          count: edits.numberOrCel(f.count) ?? 1,
          first: single ? f.first.trim() : undefined,
        });
      case "network":
        return edits.addNetwork(text, f.network, f.to);
      case "segment":
        return edits.addSegment(text, { key: f.key.trim(), title: f.title, to });
      case "sg":
        return edits.addSecurityGroup(text, { key: f.key.trim(), title: f.title, to });
      case "nsx":
        return edits.addNsxGroup(text, f.nsx, f.to);
      case "disk":
        return edits.addDisk(text, { key: f.key, size: edits.numberOrCel(f.size) ?? "", to: f.to });
      case "resource":
        return edits.addResource(text, { key: f.key.trim(), type: f.type, title: f.title });
    }
  };
  const submit = () => {
    const ok = apply(make, { select: addedId(part, f, bp), focus: "cd-heading", onError: setError });
    if (!ok) return;
    added.current = true;
    onClose();
  };

  const typeOptions = (types.data?.items ?? []).flatMap((r) =>
    r.key ? [{ value: r.key, label: r.title || r.key, meta: r.key }] : [],
  );
  const keyed = part === "component" || part === "segment" || part === "sg" || part === "resource";
  const first =
    part === "component"
      ? single
        ? "vd-new-first"
        : "vd-new-key"
      : part === "network"
        ? "vd-new-network"
        : part === "nsx"
          ? "vd-new-nsx"
          : part === "disk"
            ? "vd-new-size"
            : "vd-new-key";

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        title={t(`vmDesign.add.${part}`)}
        description={t(`vmDesign.add.${part}Help`)}
        onOpenAutoFocus={(e) => {
          e.preventDefault();
          document.getElementById(first)?.focus();
        }}
        onCloseAutoFocus={(e) => {
          // The new node's details take the focus.
          if (added.current) e.preventDefault();
        }}
      >
        <form
          className="flex flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          {part === "component" && single && (
            <Field label={t("vmDesign.field.firstKey")} htmlFor="vd-new-first" hint={t("vmDesign.field.firstKeyHint")}>
              <Input
                id="vd-new-first"
                value={f.first}
                className="font-mono text-secondary"
                onChange={(e) => set("first")(e.target.value)}
              />
            </Field>
          )}
          {part === "network" && (
            <Field label={t("vmDesign.field.network")} htmlFor="vd-new-network" hint={t("vmDesign.field.networkHint")}>
              <Input
                id="vd-new-network"
                value={f.network}
                placeholder={requestedNetwork}
                className="font-mono text-secondary"
                onChange={(e) => set("network")(e.target.value)}
              />
            </Field>
          )}
          {part === "nsx" && (
            <Field label={t("vmDesign.field.nsxId")} htmlFor="vd-new-nsx">
              <Input
                id="vd-new-nsx"
                value={f.nsx}
                className="font-mono text-secondary"
                onChange={(e) => set("nsx")(e.target.value)}
              />
            </Field>
          )}
          {part === "disk" && (
            <Field label={t("vmDesign.field.size")} htmlFor="vd-new-size" hint={t("vmDesign.field.sizeHint")}>
              <Input
                id="vd-new-size"
                value={f.size}
                inputMode="numeric"
                className="font-mono text-secondary"
                onChange={(e) => set("size")(e.target.value)}
              />
            </Field>
          )}
          {(keyed || part === "disk") && (
            <Field
              label={t("vmDesign.field.key")}
              htmlFor="vd-new-key"
              hint={part === "disk" ? t("vmDesign.field.diskKeyHint") : t("vmDesign.field.keyHint")}
            >
              <Input
                id="vd-new-key"
                value={f.key}
                className="font-mono text-secondary"
                onChange={(e) => set("key")(e.target.value)}
              />
            </Field>
          )}
          {part === "resource" && (
            <Field label={t("vmDesign.field.type")} htmlFor="vd-new-type">
              {typeOptions.length > 0 ? (
                <Select
                  id="vd-new-type"
                  value={f.type}
                  options={typeOptions}
                  placeholder={t("vmDesign.field.chooseType")}
                  onChange={set("type")}
                />
              ) : (
                <Input
                  id="vd-new-type"
                  value={f.type}
                  className="font-mono text-secondary"
                  onChange={(e) => set("type")(e.target.value)}
                />
              )}
            </Field>
          )}
          {keyed && (
            <Field label={t("vmDesign.field.title")} htmlFor="vd-new-title">
              <Input id="vd-new-title" value={f.title} onChange={(e) => set("title")(e.target.value)} />
            </Field>
          )}
          {part === "component" && (
            <Field label={t("vmDesign.field.count")} htmlFor="vd-new-count" hint={t("vmDesign.field.countHint")}>
              <Input
                id="vd-new-count"
                value={f.count}
                className="font-mono text-secondary"
                onChange={(e) => set("count")(e.target.value)}
              />
            </Field>
          )}
          {part !== "component" && part !== "resource" && (
            <Field label={t("vmDesign.field.to")} htmlFor="vd-new-to">
              <Select
                id="vd-new-to"
                value={f.to}
                options={needsVm(part) ? vms : [{ value: none, label: t("vmDesign.field.toNone") }, ...vms]}
                onChange={set("to")}
              />
            </Field>
          )}
          {error && (
            <p role="alert" className="text-secondary text-bad">
              {error}
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="quiet" onClick={onClose}>
              {t("common:actions.cancel")}
            </Button>
            <Button type="submit" variant="primary">
              {t("vmDesign.add.submit")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function NodePanel({ node, ctx }: { node: CanvasNode; ctx: Ctx }) {
  const { t } = useTranslation(["admin-catalog", "canvas"]);
  const { text, graph, apply } = ctx;
  useFocusNext(ctx.focusNext);
  const id = node.id;
  const [refusal, setRefusal] = useState<string | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});

  /** Writes a field: a refusal is said under it, and nothing is written. */
  const commit = (field: string, make: () => string, then?: Then) => {
    const ok = apply(make, { ...then, onError: (m) => setErrors((x) => ({ ...x, [field]: m })) });
    if (ok) setErrors(({ [field]: _, ...rest }) => rest);
  };
  const remove = () => apply(() => edits.removeNode(text, id), { select: null, onError: setRefusal });

  return (
    <section aria-label={t("canvas:selected", { title: node.title })} className={panelClass}>
      <div className="flex flex-wrap items-center gap-2">
        <PanelHeading badge={node.badge}>{node.title}</PanelHeading>
        <div className="ml-auto flex flex-wrap gap-1">
          <Button id="vd-remove" size="sm" variant="danger" onClick={remove}>
            <Trash2 aria-hidden />
            {t("vmDesign.panel.remove")}
          </Button>
        </div>
      </div>
      {refusal && (
        <p role="alert" className="text-bad">
          {refusal}
        </p>
      )}
      {node.problems.length > 0 && (
        <ul aria-label={t("vmDesign.panel.problems")} className="flex flex-col gap-0.5 text-bad">
          {node.problems.map((p, i) => (
            <li key={i}>{p}</li>
          ))}
        </ul>
      )}
      <Properties node={node} ctx={ctx} errors={errors} commit={commit} />
      <Connections node={node} text={text} graph={graph} apply={apply} />
      <ConnectTo node={node} text={text} graph={graph} apply={apply} />
    </section>
  );
}

interface PropsCtx {
  text: string;
  id: string;
  data: Raw;
  errors: Record<string, string>;
  commit: (field: string, make: () => string, then?: Then) => void;
}

/** A field of the node's: written when it is left or Enter is pressed, a refusal said under it. */
function Prop({
  at,
  field,
  label,
  hint,
  placeholder,
  mono,
  value,
  write,
  then,
}: {
  at: PropsCtx;
  field: string;
  label: string;
  hint?: string;
  placeholder?: string;
  mono?: boolean;
  /** Its value when it isn't the node's own field (a network's expression). */
  value?: string;
  /** The edit, when it isn't setting the field (a rename). */
  write?: (v: string) => string;
  then?: (v: string) => Then;
}) {
  const fid = `vd-${field.replace(".", "-")}`;
  const [a = "", b] = field.split(".");
  const current = value ?? shown(b === undefined ? at.data[a] : obj(at.data[a])[b]);
  return (
    <Field label={label} htmlFor={fid} hint={hint} error={at.errors[field]}>
      <CommitInput
        id={fid}
        value={current}
        placeholder={placeholder}
        className={mono ? "font-mono text-secondary" : undefined}
        onCommit={(v) =>
          at.commit(field, () => (write ? write(v) : edits.setField(at.text, at.id, field, v.trim())), then?.(v))
        }
      />
    </Field>
  );
}

/** A node's key: what names it follows (edits.rename), and the node is selected under its new ID. */
function KeyProp({ at, stem }: { at: PropsCtx; stem: string }) {
  const { t } = useTranslation("admin-catalog");
  return (
    <Prop
      at={at}
      field="key"
      label={t("vmDesign.field.key")}
      hint={t("vmDesign.field.keyHint")}
      mono
      write={(v) => edits.rename(at.text, at.id, v)}
      then={(v) => ({ select: `${stem}:${v.trim()}`, focus: "vd-key" })}
    />
  );
}

function Properties({
  node,
  ctx,
  errors,
  commit,
}: {
  node: CanvasNode;
  ctx: Ctx;
  errors: Record<string, string>;
  commit: (field: string, make: () => string, then?: Then) => void;
}) {
  const { t } = useTranslation("admin-catalog");
  const { text, bp } = ctx;
  const id = node.id;
  const kind = edits.kindOf(id);
  const data = edits.nodeData(bp, id) ?? {};
  const at: PropsCtx = { text, id, data, errors, commit };
  const title = <Prop at={at} field="title" label={t("vmDesign.field.title")} />;

  let fields: ReactNode = null;
  switch (kind) {
    case "component":
      fields = !isMulti(bp) ? (
        <>
          <Prop at={at} field="template" label={t("vmDesign.field.template")} mono />
          <Prop at={at} field="naming" label={t("vmDesign.field.naming")} mono />
          <Field label={t("vmDesign.field.osFamily")} htmlFor="vd-guest-os_family">
            <Select
              id="vd-guest-os_family"
              value={str(obj(bp.guest).os_family) || "linux"}
              options={[
                { value: "linux", label: "Linux" },
                { value: "windows", label: "Windows" },
              ]}
              onChange={(v) => commit("guest.os_family", () => edits.setField(text, id, "guest.os_family", v))}
            />
          </Field>
        </>
      ) : (
        <>
          <KeyProp at={at} stem="component" />
          {title}
          <Prop
            at={at}
            field="count"
            label={t("vmDesign.field.count")}
            hint={t("vmDesign.field.countHint")}
            placeholder="1"
            mono
            write={(v) => edits.setField(text, id, "count", edits.numberOrCel(v))}
          />
          <Prop
            at={at}
            field="template"
            label={t("vmDesign.field.template")}
            hint={t("vmDesign.field.shared")}
            placeholder={str(bp.template)}
            mono
          />
          <Prop
            at={at}
            field="naming"
            label={t("vmDesign.field.naming")}
            hint={t("vmDesign.field.shared")}
            placeholder={str(bp.naming)}
            mono
          />
          <div className="flex items-center gap-2">
            <Checkbox
              id="vd-anti_affinity"
              checked={data.anti_affinity === true}
              onChange={(v) => commit("anti_affinity", () => edits.setField(text, id, "anti_affinity", v))}
            />
            <label htmlFor="vd-anti_affinity" className="text-body">
              {t("vmDesign.field.antiAffinity")}
            </label>
          </div>
        </>
      );
      break;
    case "network":
      fields = (
        <Prop
          at={at}
          field="network"
          label={t("vmDesign.field.network")}
          hint={t("vmDesign.field.networkHint")}
          mono
          value={id.slice(id.indexOf(":") + 1)}
          write={(v) => edits.setNetwork(text, id, v)}
          then={(v) => ({ select: `network:${v.trim()}`, focus: "vd-network" })}
        />
      );
      break;
    case "auto":
      fields = <p className="text-muted">{t("vmDesign.panel.placement")}</p>;
      break;
    case "segment":
      fields = (
        <>
          <KeyProp at={at} stem="segment" />
          {title}
        </>
      );
      break;
    case "sg":
      fields = (
        <>
          <KeyProp at={at} stem="sg" />
          {title}
          <p className="text-muted">{t("vmDesign.panel.rules")}</p>
        </>
      );
      break;
    case "nsx":
      fields = (
        <Prop
          at={at}
          field="nsx"
          label={t("vmDesign.field.nsxId")}
          mono
          value={id.slice(id.indexOf(":") + 1)}
          write={(v) => edits.setNsxGroup(text, id, v)}
          then={(v) => ({ select: `nsx:${v.trim()}`, focus: "vd-nsx" })}
        />
      );
      break;
    case "disk":
      fields = (
        <>
          {id.startsWith("disk@") && <p className="text-muted">{t("vmDesign.panel.sharedDisk")}</p>}
          <Prop
            at={at}
            field="size_gb"
            label={t("vmDesign.field.size")}
            hint={t("vmDesign.field.sizeHint")}
            mono
            write={(v) => edits.setField(text, id, "size_gb", edits.numberOrCel(v))}
          />
          <Prop at={at} field="key" label={t("vmDesign.field.key")} hint={t("vmDesign.field.diskKeyHint")} mono />
          <Prop at={at} field="storage_policy" label={t("vmDesign.field.policy")} />
          <Prop at={at} field="datastore" label={t("vmDesign.field.datastore")} />
        </>
      );
      break;
    case "resource":
      fields = (
        <>
          <KeyProp at={at} stem="resource" />
          <Prop at={at} field="type" label={t("vmDesign.field.type")} mono />
          {title}
          <p className="text-muted">{t("vmDesign.panel.inputs")}</p>
        </>
      );
      break;
  }
  if (!fields) return null;
  return (
    <fieldset className="flex flex-col gap-3">
      <legend className={legendClass}>{t("vmDesign.panel.properties")}</legend>
      {fields}
    </fieldset>
  );
}

function Connections({
  node,
  text,
  graph,
  apply,
}: {
  node: CanvasNode;
  text: string;
  graph: CanvasGraph;
  apply: Ctx["apply"];
}) {
  const { t } = useTranslation("admin-catalog");
  const [refusal, setRefusal] = useState<string | null>(null);
  const edges = graph.edges.filter((e) => e.source === node.id || e.target === node.id);
  const remove = (e: CanvasEdge, i: number) => {
    const next = edges[i + 1] ?? edges[i - 1];
    apply(() => edits.removeEdge(text, e.id), {
      focus: next ? `vd-edge-${next.id}` : "vd-connect",
      onError: setRefusal,
    });
  };
  return (
    <div className="flex flex-col gap-1">
      <h5 className={legendClass}>{t("vmDesign.panel.connections")}</h5>
      {edges.length === 0 ? (
        <p className="text-muted">{t("vmDesign.panel.noConnections")}</p>
      ) : (
        <ul aria-label={t("vmDesign.panel.connections")} className="flex flex-col gap-1">
          {edges.map((e, i) => {
            const why = edits.edgeRemovable(text, e.id);
            return (
              <li key={e.id} className="flex flex-wrap items-center gap-2">
                <span className="min-w-0">{e.description}</span>
                {why === null ? (
                  <Button
                    id={`vd-edge-${e.id}`}
                    size="sm"
                    variant="quiet"
                    className="ml-auto"
                    aria-label={t("vmDesign.panel.removeConnection", { connection: e.description })}
                    onClick={() => remove(e, i)}
                  >
                    <Unlink aria-hidden />
                    {t("vmDesign.panel.disconnect")}
                  </Button>
                ) : (
                  <span className="w-full text-caption text-muted">{why}</span>
                )}
              </li>
            );
          })}
        </ul>
      )}
      {refusal && (
        <p role="alert" className="text-bad">
          {refusal}
        </p>
      )}
    </div>
  );
}

function ConnectTo({
  node,
  text,
  graph,
  apply,
}: {
  node: CanvasNode;
  text: string;
  graph: CanvasGraph;
  apply: Ctx["apply"];
}) {
  const { t } = useTranslation("admin-catalog");
  const [to, setTo] = useState("");
  const options = graph.nodes
    .filter((n) => n.id !== node.id)
    .map((n) => ({
      value: n.id,
      label: n.title,
      meta: n.subtitle,
      group: t(`vmDesign.part.${groupOf(n.id)}`),
    }));
  const preview = useMemo(() => {
    if (!to) return null;
    try {
      return { says: edits.connection(text, node.id, to).says };
    } catch (e) {
      return { refused: e instanceof Error ? e.message : String(e) };
    }
  }, [text, node.id, to]);
  const connect = () => {
    if (apply(() => edits.connect(text, node.id, to), { focus: "vd-connect" })) setTo("");
  };
  return (
    <div className="flex flex-col gap-1.5">
      <Field label={t("vmDesign.panel.connectTo")} htmlFor="vd-connect">
        <div className="flex gap-2">
          <Select
            id="vd-connect"
            value={to}
            options={options}
            placeholder={t("vmDesign.panel.chooseNode")}
            onChange={setTo}
            aria-describedby="vd-connect-says"
          />
          <Button size="sm" disabled={!preview || "refused" in preview} onClick={connect}>
            <Link2 aria-hidden />
            {t("vmDesign.panel.connect")}
          </Button>
        </div>
      </Field>
      <p
        id="vd-connect-says"
        aria-live="polite"
        className={preview && "refused" in preview ? "text-secondary text-warn" : "text-secondary text-muted"}
      >
        {preview ? ("refused" in preview ? preview.refused : preview.says) : ""}
      </p>
    </div>
  );
}
