// rvav's own IP ranges (ADR-0063): addresses for networks EfficientIP
// doesn't manage, who holds each, and what the reconcile report finds in
// them.
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, ListOrdered } from "lucide-react";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Meter } from "@/components/common/meter";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { fullDate, relative } from "@/lib/format";
import { adm, q } from "@/lib/query";
import { errorAt, failed, fieldErrors } from "./common";

type S = components["schemas"];

const mono = "font-mono text-secondary";

const split = (s: string) =>
  s
    .split(/[\s,]+/)
    .map((x) => x.trim())
    .filter(Boolean);

function invalidate(qc: ReturnType<typeof useQueryClient>) {
  for (const queryKey of [
    ["admin", "ip-ranges"],
    ["admin", "network-mappings"],
    ["admin", "placement-preview"],
  ])
    void qc.invalidateQueries({ queryKey });
}

function Given({ at }: { at: string }) {
  return (
    <time dateTime={at} title={fullDate(at)} className="text-secondary text-muted">
      {relative(at)}
    </time>
  );
}

function Holder({ hostname, adapter }: { hostname: string; adapter?: string }) {
  const { t } = useTranslation("admin-governance");
  return (
    <span className={mono}>
      {hostname}
      {adapter && <span className="block text-caption text-muted">{t("ipRanges.adapter", { name: adapter })}</span>}
    </span>
  );
}

function DeploymentLink({ id, name }: { id?: string; name?: string }) {
  const { t } = useTranslation("admin-governance");
  if (!id || !name) return <span className="text-secondary text-muted">{t("ipRanges.gone")}</span>;
  return (
    <Link
      to="/deployments/$deploymentId"
      params={{ deploymentId: id }}
      className="font-medium text-fg hover:text-accent"
    >
      {name}
    </Link>
  );
}

function RangeSheet({ range, onClose }: { range?: S["IpRange"]; onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const qc = useQueryClient();
  const inventory = useQuery(adm.inventory());
  const [f, setF] = useState({
    name: range?.name ?? "",
    description: range?.description ?? "",
    vcenter: range?.vcenter ?? "",
    network: range?.network ?? "",
    cidr: range?.cidr ?? "",
    start_address: range?.start_address ?? "",
    end_address: range?.end_address ?? "",
    gateway: range?.gateway ?? "",
    dns_servers: (range?.dns_servers ?? []).join(", "),
    dns_search_domains: (range?.dns_search_domains ?? []).join(", "),
    dns_domain: range?.dns_domain ?? "",
    excluded: (range?.excluded ?? []).join("\n"),
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const networks = inventory.data?.networks ?? [];
  const vcenters = [...new Set(networks.map((n) => n.vcenter))];
  const vcenter = f.vcenter || vcenters[0] || "";
  const options = (list: string[]) => list.map((v) => ({ value: v, label: v }));
  // Those of a range VMs hold addresses from stay as they are.
  const held = (range?.used ?? 0) > 0;
  const save = useMutation({
    mutationFn: () => {
      const body: S["IpRangeWrite"] = {
        ...f,
        vcenter,
        dns_servers: split(f.dns_servers),
        dns_search_domains: split(f.dns_search_domains),
        excluded: split(f.excluded),
      };
      return range
        ? call(api.PUT("/admin/ip-ranges/{id}", { params: { path: { id: range.id } }, body }))
        : call(api.POST("/admin/ip-ranges", { body }));
    },
    onSuccess: () => {
      invalidate(qc);
      toast.success(t("ipRanges.saved", { name: f.name }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("infra.notSaved"));
    },
  });
  const text = (
    k: keyof typeof f,
    label: string,
    opts: { hint?: string; monospace?: boolean; disabled?: boolean } = {},
  ) => (
    <Field label={label} htmlFor={`ipr-${k}`} hint={opts.hint} error={errorAt(errors, `/${k}`)}>
      <Input
        id={`ipr-${k}`}
        className={opts.monospace ? "font-mono" : undefined}
        disabled={opts.disabled}
        value={f[k]}
        onChange={(e) => setF({ ...f, [k]: e.target.value })}
      />
    </Field>
  );
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={range ? t("infra.changeNamed", { name: range.name }) : t("ipRanges.new")}
      description={t("ipRanges.sheetLead")}
      submitLabel={save.isPending ? t("infra.saving") : t("ipRanges.save")}
      submitDisabled={!f.name || !f.network}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      {text("name", t("ipRanges.name"))}
      <Field label={t("infra.description")} htmlFor="ipr-description" hint={t("ipRanges.optional")}>
        <Textarea
          id="ipr-description"
          rows={2}
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      {held && <p className="text-secondary text-muted">{t("ipRanges.held", { count: range?.used ?? 0 })}</p>}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t("ipRanges.vcenter")} htmlFor="ipr-vcenter" error={errors["/vcenter"]}>
          <Select
            id="ipr-vcenter"
            value={vcenter}
            onChange={(v) => setF({ ...f, vcenter: v, network: "" })}
            options={options(vcenters.length > 0 ? vcenters : [vcenter].filter(Boolean))}
            loading={inventory.isPending}
            disabled={held}
          />
        </Field>
        <Field
          label={t("ipRanges.network")}
          htmlFor="ipr-network"
          error={errors["/network"]}
          hint={t("ipRanges.networkHint")}
        >
          <Select
            id="ipr-network"
            value={f.network}
            onChange={(v) => setF({ ...f, network: v })}
            options={options(networks.filter((n) => n.vcenter === vcenter).map((n) => n.name))}
            loading={inventory.isPending}
            disabled={held}
          />
        </Field>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        {text("cidr", t("ipRanges.subnet"), { monospace: true, hint: t("ipRanges.subnetHint"), disabled: held })}
        {text("gateway", t("ipRanges.gateway"), { monospace: true, disabled: held })}
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        {text("start_address", t("ipRanges.first"), { monospace: true })}
        {text("end_address", t("ipRanges.last"), { monospace: true })}
      </div>
      <Field
        label={t("ipRanges.excluded")}
        htmlFor="ipr-excluded"
        hint={t("ipRanges.excludedHint")}
        error={errorAt(errors, "/excluded")}
      >
        <Textarea
          id="ipr-excluded"
          rows={3}
          className="font-mono"
          value={f.excluded}
          onChange={(e) => setF({ ...f, excluded: e.target.value })}
        />
      </Field>
      {text("dns_servers", t("ipRanges.dnsServers"), { monospace: true, hint: t("ipRanges.commaSeparated") })}
      {text("dns_search_domains", t("ipRanges.searchDomains"), { hint: t("ipRanges.searchDomainsHint") })}
      {text("dns_domain", t("ipRanges.dnsDomain"), { hint: t("ipRanges.dnsDomainHint") })}
    </Sheet>
  );
}

function AddressesSheet({ range, onClose }: { range: S["IpRange"]; onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const allocations = useQuery(adm.ipAllocations(range.id));
  const columns: Column<S["IpAllocation"]>[] = [
    { id: "address", header: t("ipRanges.address"), cell: (a) => <span className={mono}>{a.address}</span> },
    {
      id: "vm",
      header: t("ipRanges.vm"),
      sortValue: (a) => a.hostname,
      cell: (a) => <Holder hostname={a.hostname} adapter={a.adapter} />,
    },
    {
      id: "deployment",
      header: t("ipRanges.deployment"),
      cell: (a) => <DeploymentLink id={a.deployment_id} name={a.deployment_name} />,
    },
    {
      id: "given",
      header: t("ipRanges.given"),
      sortValue: (a) => a.allocated_at,
      cell: (a) => <Given at={a.allocated_at} />,
    },
  ];
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={t("ipRanges.addressesOf", { name: range.name })}
      description={t("ipRanges.addressesLead", { network: range.network, vcenter: range.vcenter })}
    >
      <Meter label={t("ipRanges.free", { count: range.free })} used={range.used} total={range.size} />
      <DataTable
        label={t("ipRanges.addressesGiven")}
        columns={columns}
        rows={allocations.data?.items}
        rowKey={(a) => a.address}
        minWidth={460}
        loading={allocations.isPending}
        error={
          allocations.isError && <ErrorState error={allocations.error} onRetry={() => void allocations.refetch()} />
        }
        empty={<EmptyState icon={ListOrdered} title={t("ipRanges.noneGiven")} body={t("ipRanges.noneGivenBody")} />}
      />
    </Sheet>
  );
}

function Reconcile() {
  const { t } = useTranslation("admin-governance");
  const qc = useQueryClient();
  const [run, setRun] = useState(false);
  const report = useQuery({ ...q.ipamReconcile(), enabled: run });
  const [releasing, setReleasing] = useState<S["IpamOrphanAllocation"] | null>(null);
  const release = useMutation({
    mutationFn: (o: S["IpamOrphanAllocation"]) =>
      call(
        api.DELETE("/admin/ip-ranges/{id}/allocations/{address}", {
          params: { path: { id: o.range_id, address: o.address } },
        }),
      ),
    onSuccess: (_, o) => {
      invalidate(qc);
      void report.refetch();
      toast.success(t("ipRanges.givenBack", { address: o.address }));
      setReleasing(null);
    },
    onError: (e) => failed(e, t("ipRanges.notGivenBack")),
  });
  const exclude = useMutation({
    mutationFn: async (u: S["IpamUnallocated"]) => {
      const r = await call(api.GET("/admin/ip-ranges/{id}", { params: { path: { id: u.range_id } } }));
      const body: S["IpRangeWrite"] = {
        name: r.name,
        description: r.description,
        vcenter: r.vcenter,
        network: r.network,
        cidr: r.cidr,
        start_address: r.start_address,
        end_address: r.end_address,
        gateway: r.gateway,
        dns_servers: r.dns_servers,
        dns_search_domains: r.dns_search_domains,
        dns_domain: r.dns_domain,
        excluded: [...r.excluded, u.address],
      };
      return call(api.PUT("/admin/ip-ranges/{id}", { params: { path: { id: r.id } }, body }));
    },
    onSuccess: (_, u) => {
      invalidate(qc);
      void report.refetch();
      toast.success(t("ipRanges.excludedFrom", { address: u.address, range: u.range_name }));
    },
    onError: (e) => failed(e, t("ipRanges.notExcluded")),
  });
  const data = report.data;
  const clean = data && data.orphan_allocations.length + data.unallocated.length === 0;
  const range = (r: { range_name: string; network: string }) => (
    <span className="text-secondary">
      {r.range_name} <span className="block font-mono text-caption text-muted">{r.network}</span>
    </span>
  );
  return (
    <Panel
      title={t("ipRanges.reconcile.title")}
      description={t("ipRanges.reconcile.lead")}
      flush
      actions={
        <Button size="sm" disabled={report.isFetching} onClick={() => (run ? void report.refetch() : setRun(true))}>
          {report.isFetching
            ? t("ipRanges.reconcile.checking")
            : data
              ? t("ipRanges.reconcile.again")
              : t("ipRanges.reconcile.run")}
        </Button>
      }
    >
      {!run && <p className="px-4 py-3 text-secondary text-muted">{t("ipRanges.reconcile.intro")}</p>}
      {run && report.isPending && <SkeletonRows rows={3} cols={4} />}
      {report.isError && <ErrorState error={report.error} onRetry={() => void report.refetch()} />}
      {data && (
        <p className="px-4 py-3 text-secondary text-muted">
          {t("ipRanges.reconcile.checked", { at: fullDate(data.checked_at) })}
        </p>
      )}
      {clean && (
        <EmptyState
          icon={CircleCheck}
          title={t("ipRanges.reconcile.cleanTitle")}
          body={t("ipRanges.reconcile.cleanBody")}
        />
      )}
      {data && data.orphan_allocations.length > 0 && (
        <section aria-label={t("ipRanges.reconcile.orphans")} className="border-t border-line">
          <h3 className="px-4 py-2.5 text-body font-semibold">{t("ipRanges.reconcile.orphans")}</h3>
          <DataTable
            label={t("ipRanges.reconcile.orphans")}
            rows={data.orphan_allocations}
            rowKey={(o) => `${o.range_id}/${o.address}`}
            minWidth={560}
            columns={[
              { id: "address", header: t("ipRanges.address"), cell: (o) => <span className={mono}>{o.address}</span> },
              { id: "vm", header: t("ipRanges.vm"), cell: (o) => <Holder hostname={o.hostname} adapter={o.adapter} /> },
              { id: "range", header: t("ipRanges.reconcile.range"), cell: range },
              { id: "given", header: t("ipRanges.given"), cell: (o) => <Given at={o.allocated_at} /> },
            ]}
            rowMenuLabel={(o) => t("infra.actionsFor", { name: o.address })}
            rowMenu={(o) => <MenuItem onSelect={() => setReleasing(o)}>{t("ipRanges.reconcile.giveBack")}</MenuItem>}
          />
        </section>
      )}
      {data && data.unallocated.length > 0 && (
        <section aria-label={t("ipRanges.reconcile.unallocated")} className="border-t border-line">
          <h3 className="px-4 pt-2.5 text-body font-semibold">{t("ipRanges.reconcile.unallocated")}</h3>
          <p className="px-4 pb-2.5 text-secondary text-muted">{t("ipRanges.reconcile.unallocatedLead")}</p>
          <DataTable
            label={t("ipRanges.reconcile.unallocated")}
            rows={data.unallocated}
            rowKey={(u) => `${u.vcenter}/${u.vm}`}
            minWidth={560}
            columns={[
              {
                id: "vm",
                header: t("ipRanges.vm"),
                cell: (u) => (
                  <span className={mono}>
                    {u.vm}
                    {u.deployment_id && u.deployment_name && (
                      <span className="block font-sans text-caption">
                        <DeploymentLink id={u.deployment_id} name={u.deployment_name} />
                      </span>
                    )}
                  </span>
                ),
              },
              { id: "address", header: t("ipRanges.address"), cell: (u) => <span className={mono}>{u.address}</span> },
              { id: "range", header: t("ipRanges.reconcile.range"), cell: range },
              {
                id: "held",
                header: t("ipRanges.reconcile.heldBy"),
                cell: (u) =>
                  u.held_by ? (
                    <span className={mono}>{u.held_by}</span>
                  ) : (
                    <span className="text-muted">{t("ipRanges.reconcile.noVm")}</span>
                  ),
              },
            ]}
            rowMenuLabel={(u) => t("infra.actionsFor", { name: u.vm })}
            rowMenu={(u) =>
              // An address another VM holds can't be excluded: one of the two VMs moves.
              u.held_by ? null : (
                <MenuItem disabled={exclude.isPending} onSelect={() => exclude.mutate(u)}>
                  {t("ipRanges.reconcile.excludeFrom", { range: u.range_name })}
                </MenuItem>
              )
            }
          />
        </section>
      )}
      {releasing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setReleasing(null)}
          title={t("ipRanges.reconcile.giveBackTitle", { address: releasing.address })}
          description={t("ipRanges.reconcile.giveBackLead", { hostname: releasing.hostname })}
          consequences={t("ipRanges.reconcile.giveBackConsequences")}
          confirmLabel={t("ipRanges.reconcile.giveBackConfirm")}
          pending={release.isPending}
          onConfirm={() => release.mutate(releasing)}
        />
      )}
    </Panel>
  );
}

export function IPRanges({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation("admin-governance");
  const qc = useQueryClient();
  const ranges = useQuery(adm.ipRanges());
  const [editing, setEditing] = useState<S["IpRange"] | "new" | null>(null);
  const [viewing, setViewing] = useState<S["IpRange"] | null>(null);
  const [removing, setRemoving] = useState<S["IpRange"] | null>(null);
  const remove = useMutation({
    mutationFn: (r: S["IpRange"]) => call(api.DELETE("/admin/ip-ranges/{id}", { params: { path: { id: r.id } } })),
    onSuccess: (_, r) => {
      invalidate(qc);
      toast.success(t("ipRanges.removed", { name: r.name }));
      setRemoving(null);
    },
    onError: (e) => failed(e, t("infra.notRemoved")),
  });
  const items = ranges.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("ipRanges.new")}
    </Button>
  );
  const columns: Column<S["IpRange"]>[] = [
    {
      id: "range",
      header: t("ipRanges.range"),
      sortValue: (r) => r.name,
      cell: (r) => (
        <>
          <span className="font-medium">{r.name}</span>
          {r.description && <span className="block text-secondary text-muted">{r.description}</span>}
        </>
      ),
    },
    {
      id: "network",
      header: t("ipRanges.network"),
      sortValue: (r) => r.network,
      cell: (r) => (
        <span className={mono}>
          {r.network}
          <span className="block text-caption text-muted">{r.vcenter}</span>
        </span>
      ),
    },
    {
      id: "addresses",
      header: t("ipRanges.addresses"),
      cell: (r) => (
        <span className={mono}>
          {r.start_address} – {r.end_address}
          <span className="block text-caption text-muted">
            {t("ipRanges.via", { cidr: r.cidr, gateway: r.gateway })}
            {r.excluded.length > 0 && t("ipRanges.excludedCount", { count: r.excluded.length })}
          </span>
        </span>
      ),
    },
    {
      id: "usage",
      header: t("ipRanges.given"),
      sortValue: (r) => (r.size ? r.used / r.size : 0),
      className: "min-w-40",
      cell: (r) => <Meter label={t("ipRanges.free", { count: r.free })} used={r.used} total={r.size} />,
    },
    {
      id: "dns",
      header: t("ipRanges.dns"),
      cell: (r) => (
        <span className="text-secondary">
          {r.dns_domain || t("ipRanges.blueprintDomain")}
          <span className="block font-mono text-caption text-muted">{r.dns_servers.join(", ")}</span>
        </span>
      ),
    },
  ];
  return (
    <div className="flex flex-col gap-4">
      <Panel
        title={t("ipRanges.title")}
        description={t("ipRanges.lead")}
        flush
        actions={canEdit && items?.length !== 0 && add}
      >
        <DataTable
          label={t("ipRanges.title")}
          columns={columns}
          rows={items}
          rowKey={(r) => r.id}
          minWidth={820}
          loading={ranges.isPending}
          error={ranges.isError && <ErrorState error={ranges.error} onRetry={() => void ranges.refetch()} />}
          empty={
            <EmptyState
              icon={ListOrdered}
              title={t("ipRanges.emptyTitle")}
              body={t("ipRanges.emptyBody")}
              action={canEdit && add}
            />
          }
          rowMenuLabel={(r) => t("infra.actionsFor", { name: r.name })}
          rowMenu={(r) => (
            <>
              <MenuItem onSelect={() => setViewing(r)}>{t("ipRanges.addressesGiven")}</MenuItem>
              {canEdit && (
                <>
                  <MenuItem onSelect={() => setEditing(r)}>{t("infra.change")}</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setRemoving(r)}>
                    {t("infra.remove")}
                  </MenuItem>
                </>
              )}
            </>
          )}
        />
        {editing && <RangeSheet range={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
        {viewing && <AddressesSheet range={viewing} onClose={() => setViewing(null)} />}
        {removing && (
          <ConfirmDialog
            open
            onOpenChange={(o) => !o && setRemoving(null)}
            title={t("infra.removeNamed", { name: removing.name })}
            description={
              <Trans
                t={t}
                i18nKey="ipRanges.removeLead"
                values={{ network: removing.network }}
                components={{ mono: <span className="font-mono" /> }}
              />
            }
            consequences={
              removing.used > 0 ? t("ipRanges.removeHeld", { count: removing.used }) : t("ipRanges.removeFree")
            }
            confirmLabel={t("ipRanges.removeConfirm")}
            pending={remove.isPending}
            onConfirm={() => remove.mutate(removing)}
          />
        )}
      </Panel>
      {canEdit && <Reconcile />}
    </div>
  );
}
