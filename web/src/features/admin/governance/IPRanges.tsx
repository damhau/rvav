// rvav's own IP ranges (ADR-0063): addresses for networks EfficientIP
// doesn't manage, who holds each, and what the reconcile report finds in
// them.
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, ListOrdered } from "lucide-react";
import { useState } from "react";
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
  return (
    <span className={mono}>
      {hostname}
      {adapter && <span className="block text-caption text-muted">adapter {adapter}</span>}
    </span>
  );
}

function DeploymentLink({ id, name }: { id?: string; name?: string }) {
  if (!id || !name) return <span className="text-secondary text-muted">Gone</span>;
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
      toast.success(`${f.name} saved`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
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
      title={range ? `Change ${range.name}` : "New IP range"}
      description="VMs on the network take their addresses from its ranges instead of EfficientIP. rvav writes no DNS records for them."
      submitLabel={save.isPending ? "Saving…" : "Save range"}
      submitDisabled={!f.name || !f.network}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      {text("name", "Name")}
      <Field label="Description" htmlFor="ipr-description" hint="Optional">
        <Textarea
          id="ipr-description"
          rows={2}
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      {held && (
        <p className="text-secondary text-muted">
          VMs hold {range?.used} addresses from this range: its vCenter, network, subnet and gateway stay as they are.
        </p>
      )}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="vCenter" htmlFor="ipr-vcenter" error={errors["/vcenter"]}>
          <Select
            id="ipr-vcenter"
            value={vcenter}
            onChange={(v) => setF({ ...f, vcenter: v, network: "" })}
            options={options(vcenters.length > 0 ? vcenters : [vcenter].filter(Boolean))}
            loading={inventory.isPending}
            disabled={held}
          />
        </Field>
        <Field label="Network" htmlFor="ipr-network" error={errors["/network"]} hint="A port group or an NSX segment's">
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
        {text("cidr", "Subnet", { monospace: true, hint: "IPv4, such as 10.51.0.0/24", disabled: held })}
        {text("gateway", "Gateway", { monospace: true, disabled: held })}
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        {text("start_address", "First address", { monospace: true })}
        {text("end_address", "Last address", { monospace: true })}
      </div>
      <Field
        label="Excluded addresses"
        htmlFor="ipr-excluded"
        hint="One address or span per line, such as 10.51.0.50-10.51.0.59: never given."
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
      {text("dns_servers", "DNS servers", { monospace: true, hint: "Comma-separated" })}
      {text("dns_search_domains", "DNS search domains", {
        hint: "Comma-separated, searched after the VM's own domain",
      })}
      {text("dns_domain", "DNS domain", {
        hint: "The domain VMs are named in; empty: the blueprint's. rvav creates no DNS records in it.",
      })}
    </Sheet>
  );
}

function AddressesSheet({ range, onClose }: { range: S["IpRange"]; onClose: () => void }) {
  const allocations = useQuery(adm.ipAllocations(range.id));
  const columns: Column<S["IpAllocation"]>[] = [
    { id: "address", header: "Address", cell: (a) => <span className={mono}>{a.address}</span> },
    {
      id: "vm",
      header: "VM",
      sortValue: (a) => a.hostname,
      cell: (a) => <Holder hostname={a.hostname} adapter={a.adapter} />,
    },
    {
      id: "deployment",
      header: "Deployment",
      cell: (a) => <DeploymentLink id={a.deployment_id} name={a.deployment_name} />,
    },
    { id: "given", header: "Given", sortValue: (a) => a.allocated_at, cell: (a) => <Given at={a.allocated_at} /> },
  ];
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={`Addresses of ${range.name}`}
      description={`${range.network} in ${range.vcenter}. rvav gives an address back when its VM or adapter goes.`}
    >
      <Meter label={`${range.free} free`} used={range.used} total={range.size} />
      <DataTable
        label="Addresses given"
        columns={columns}
        rows={allocations.data?.items}
        rowKey={(a) => a.address}
        minWidth={460}
        loading={allocations.isPending}
        error={
          allocations.isError && <ErrorState error={allocations.error} onRetry={() => void allocations.refetch()} />
        }
        empty={
          <EmptyState
            icon={ListOrdered}
            title="No address given yet"
            body="The network's next VM gets the range's first free address."
          />
        }
      />
    </Sheet>
  );
}

function Reconcile() {
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
      toast.success(`${o.address} given back`);
      setReleasing(null);
    },
    onError: (e) => failed(e, "Not given back"),
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
      toast.success(`${u.address} excluded from ${u.range_name}`);
    },
    onError: (e) => failed(e, "Not excluded"),
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
      title="Reconcile the IP ranges"
      description="Compares the ranges with what VMs hold. Nothing changes unless you give an address back."
      flush
      actions={
        <Button size="sm" disabled={report.isFetching} onClick={() => (run ? void report.refetch() : setRun(true))}>
          {report.isFetching ? "Checking…" : data ? "Check again" : "Run the check"}
        </Button>
      }
    >
      {!run && (
        <p className="px-4 py-3 text-secondary text-muted">
          It lists the addresses given to VMs rvav no longer has, such as an unregistered VM&apos;s, and the vCenter VMs
          on an address of a range that didn&apos;t give it to them.
        </p>
      )}
      {run && report.isPending && <SkeletonRows rows={3} cols={4} />}
      {report.isError && <ErrorState error={report.error} onRetry={() => void report.refetch()} />}
      {data && <p className="px-4 py-3 text-secondary text-muted">Checked {fullDate(data.checked_at)}.</p>}
      {clean && (
        <EmptyState
          icon={CircleCheck}
          title="Nothing to reconcile"
          body="Every address given belongs to a VM rvav has, and no vCenter VM is on an address its range didn't give it."
        />
      )}
      {data && data.orphan_allocations.length > 0 && (
        <section aria-label="Addresses of VMs rvav no longer has" className="border-t border-line">
          <h3 className="px-4 py-2.5 text-body font-semibold">Addresses of VMs rvav no longer has</h3>
          <DataTable
            label="Addresses of VMs rvav no longer has"
            rows={data.orphan_allocations}
            rowKey={(o) => `${o.range_id}/${o.address}`}
            minWidth={560}
            columns={[
              { id: "address", header: "Address", cell: (o) => <span className={mono}>{o.address}</span> },
              { id: "vm", header: "VM", cell: (o) => <Holder hostname={o.hostname} adapter={o.adapter} /> },
              { id: "range", header: "Range", cell: range },
              { id: "given", header: "Given", cell: (o) => <Given at={o.allocated_at} /> },
            ]}
            rowMenuLabel={(o) => `Actions for ${o.address}`}
            rowMenu={(o) => <MenuItem onSelect={() => setReleasing(o)}>Give back</MenuItem>}
          />
        </section>
      )}
      {data && data.unallocated.length > 0 && (
        <section aria-label="VMs on an address their range didn't give" className="border-t border-line">
          <h3 className="px-4 pt-2.5 text-body font-semibold">VMs on an address their range didn&apos;t give</h3>
          <p className="px-4 pb-2.5 text-secondary text-muted">
            Exclude the address from its range, or move the VM, so that the range never gives it to another VM.
          </p>
          <DataTable
            label="VMs on an address their range didn't give"
            rows={data.unallocated}
            rowKey={(u) => `${u.vcenter}/${u.vm}`}
            minWidth={560}
            columns={[
              {
                id: "vm",
                header: "VM",
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
              { id: "address", header: "Address", cell: (u) => <span className={mono}>{u.address}</span> },
              { id: "range", header: "Range", cell: range },
              {
                id: "held",
                header: "Given to",
                cell: (u) =>
                  u.held_by ? <span className={mono}>{u.held_by}</span> : <span className="text-muted">No VM</span>,
              },
            ]}
            rowMenuLabel={(u) => `Actions for ${u.vm}`}
            rowMenu={(u) =>
              // An address another VM holds can't be excluded: one of the two VMs moves.
              u.held_by ? null : (
                <MenuItem disabled={exclude.isPending} onSelect={() => exclude.mutate(u)}>
                  Exclude from {u.range_name}
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
          title={`Give back ${releasing.address}?`}
          description={`No deployment has ${releasing.hostname} any more: the range can give the address to its next VM.`}
          consequences="If the VM still runs on this address in vCenter, exclude the address from the range instead."
          confirmLabel="Give back address"
          pending={release.isPending}
          onConfirm={() => release.mutate(releasing)}
        />
      )}
    </Panel>
  );
}

export function IPRanges({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const ranges = useQuery(adm.ipRanges());
  const [editing, setEditing] = useState<S["IpRange"] | "new" | null>(null);
  const [viewing, setViewing] = useState<S["IpRange"] | null>(null);
  const [removing, setRemoving] = useState<S["IpRange"] | null>(null);
  const remove = useMutation({
    mutationFn: (r: S["IpRange"]) => call(api.DELETE("/admin/ip-ranges/{id}", { params: { path: { id: r.id } } })),
    onSuccess: (_, r) => {
      invalidate(qc);
      toast.success(`${r.name} removed`);
      setRemoving(null);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const items = ranges.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New IP range
    </Button>
  );
  const columns: Column<S["IpRange"]>[] = [
    {
      id: "range",
      header: "Range",
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
      header: "Network",
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
      header: "Addresses",
      cell: (r) => (
        <span className={mono}>
          {r.start_address} – {r.end_address}
          <span className="block text-caption text-muted">
            {r.cidr} via {r.gateway}
            {r.excluded.length > 0 && `, ${r.excluded.length} excluded`}
          </span>
        </span>
      ),
    },
    {
      id: "usage",
      header: "Given",
      sortValue: (r) => (r.size ? r.used / r.size : 0),
      className: "min-w-40",
      cell: (r) => <Meter label={`${r.free} free`} used={r.used} total={r.size} />,
    },
    {
      id: "dns",
      header: "DNS",
      cell: (r) => (
        <span className="text-secondary">
          {r.dns_domain || "The blueprint's domain"}
          <span className="block font-mono text-caption text-muted">{r.dns_servers.join(", ")}</span>
        </span>
      ),
    },
  ];
  return (
    <div className="flex flex-col gap-4">
      <Panel
        title="IP ranges"
        description="Addresses rvav gives VMs itself, on networks EfficientIP doesn't manage: a network with a range takes its new VMs' addresses from it. rvav creates no DNS records for them."
        flush
        actions={canEdit && items?.length !== 0 && add}
      >
        <DataTable
          label="IP ranges"
          columns={columns}
          rows={items}
          rowKey={(r) => r.id}
          minWidth={820}
          loading={ranges.isPending}
          error={ranges.isError && <ErrorState error={ranges.error} onRetry={() => void ranges.refetch()} />}
          empty={
            <EmptyState
              icon={ListOrdered}
              title="No IP ranges"
              body="Every network takes its VMs' addresses from EfficientIP. Add a range for a network EfficientIP doesn't manage."
              action={canEdit && add}
            />
          }
          rowMenuLabel={(r) => `Actions for ${r.name}`}
          rowMenu={(r) => (
            <>
              <MenuItem onSelect={() => setViewing(r)}>Addresses given</MenuItem>
              {canEdit && (
                <>
                  <MenuItem onSelect={() => setEditing(r)}>Change</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger onSelect={() => setRemoving(r)}>
                    Remove
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
            title={`Remove ${removing.name}?`}
            description={
              <>
                It stops giving addresses on <span className="font-mono">{removing.network}</span>.
              </>
            }
            consequences={
              removing.used > 0
                ? `VMs hold ${removing.used} addresses from it: delete them, or move their adapters, first.`
                : "When it is the network's last range, the network's new VMs take their addresses from EfficientIP again."
            }
            confirmLabel="Remove range"
            pending={remove.isPending}
            onConfirm={() => remove.mutate(removing)}
          />
        )}
      </Panel>
      {canEdit && <Reconcile />}
    </div>
  );
}
