// Several NSX Managers (I1.1, ADR-0062): the NSX integration's first, then
// those administrators add. Each serves vCenters, and a VM's segments and
// security groups are made on its vCenter's.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Network } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, FieldNote, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { failed, fieldErrors } from "./governance/common";
import { ServerCertificate } from "./ServerCertificate";
import { TestResult } from "./VCenters";

type S = components["schemas"];
type Manager = S["NsxManager"];

const vcentersError = (errors: Record<string, string>) =>
  Object.entries(errors).find(([field]) => field.startsWith("/vcenters"))?.[1];

/** The vCenters an NSX Manager serves: one ticked here moves from the NSX Manager that served it. */
function VCenterChoice({
  self,
  managers,
  chosen,
  onChange,
  error,
}: {
  self: string;
  managers: Manager[];
  chosen: string[];
  onChange: (next: string[]) => void;
  error?: string;
}) {
  const id = useId();
  const vcenters = useQuery(adm.vcenters());
  const servedBy = (vc: string) => managers.find((m) => m.name !== self && m.vcenters.includes(vc))?.name;
  const names = vcenters.data?.items.map((v) => v.name) ?? [];
  return (
    <fieldset className="flex flex-col gap-2" aria-describedby={`${id}-note`}>
      <legend className="mb-1 text-secondary font-medium">vCenters it serves</legend>
      {vcenters.isError && <ErrorState error={vcenters.error} onRetry={() => void vcenters.refetch()} />}
      <div className="flex flex-col gap-1.5">
        {names.map((vc) => {
          const other = servedBy(vc);
          const on = chosen.includes(vc);
          return (
            <label key={vc} htmlFor={`${id}-${vc}`} className="flex items-start gap-1.5 text-body">
              <Checkbox
                id={`${id}-${vc}`}
                checked={on}
                onChange={(next) => onChange(next ? [...chosen, vc] : chosen.filter((x) => x !== vc))}
              />
              <span>
                <span className="font-mono text-secondary">{vc}</span>
                {other && (
                  <span className="block text-secondary text-muted">
                    {on ? `Moves here from ${other}` : `Served by ${other}`}
                  </span>
                )}
              </span>
            </label>
          );
        })}
      </div>
      <FieldNote
        id={`${id}-note`}
        error={error}
        hint="The NSX segments and security groups of a vCenter's VMs are made on the NSX Manager serving it. A vCenter none serves can't have them."
      />
    </fieldset>
  );
}

function ManagerSheet({ m, managers, onClose }: { m?: Manager; managers: Manager[]; onClose: () => void }) {
  const qc = useQueryClient();
  const formId = useId();
  const [f, setFNow] = useState({
    name: m?.name ?? "",
    url: m?.url ?? "",
    username: m?.username ?? "",
    cert_file: m?.cert_file ?? "",
    key_file: m?.key_file ?? "",
    insecure: m?.insecure ?? false,
    cert_sha256: m?.cert_sha256 ?? "",
  });
  const [password, setPasswordNow] = useState("");
  const [vcenters, setVCenters] = useState<string[]>(m?.vcenters ?? []);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const body = (): S["NsxManagerWrite"] => ({ ...f, ...(password ? { password } : {}), vcenters });
  const save = useMutation({
    mutationFn: () =>
      m
        ? call(api.PUT("/admin/nsx-managers/{name}", { params: { path: { name: m.name } }, body: body() }))
        : call(api.POST("/admin/nsx-managers", { body: body() })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "nsx-managers"] });
      toast.success(`${f.name} saved`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  const test = useMutation({
    mutationFn: () =>
      call(api.POST("/admin/nsx-managers/{name}/test", { params: { path: { name: f.name || "new" } }, body: body() })),
    onMutate: () => setErrors({}),
    onSuccess: () => setErrors({}),
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "The test didn't run");
    },
  });
  // A test result describes the settings it ran with: changed, it goes.
  const setF = (next: typeof f) => {
    setFNow(next);
    test.reset();
  };
  const setPassword = (next: string) => {
    setPasswordNow(next);
    test.reset();
  };
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={m ? `Change ${m.name}` : "Add an NSX Manager"}
      description="rvav makes the NSX segments and security groups of its vCenters' VMs on it, with this account."
      footer={
        <>
          <Button type="button" variant="secondary" onClick={onClose} disabled={save.isPending}>
            Cancel
          </Button>
          <Button type="button" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? "Testing…" : "Test connection"}
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save NSX Manager"}
          </Button>
        </>
      }
    >
      <form
        id={formId}
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          setErrors({});
          save.mutate();
        }}
      >
        <Field
          label="Name"
          htmlFor="nsxm-name"
          hint={m ? "Fixed once added" : "How rvav calls it, usually its host name"}
          error={errors["/name"]}
        >
          <Input
            id="nsxm-name"
            className="font-mono text-secondary"
            disabled={Boolean(m)}
            placeholder="nsx02.corp.example"
            value={f.name}
            onChange={(e) => setF({ ...f, name: e.target.value })}
          />
        </Field>
        <Field label="Address" htmlFor="nsxm-url" error={errors["/url"]}>
          <Input
            id="nsxm-url"
            className="font-mono text-secondary"
            placeholder="https://nsx02.corp.example"
            value={f.url}
            onChange={(e) => setF({ ...f, url: e.target.value })}
          />
        </Field>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="Account" htmlFor="nsxm-user" error={errors["/username"]}>
            <Input id="nsxm-user" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} />
          </Field>
          <Field
            label="Password"
            htmlFor="nsxm-password"
            error={errors["/password"]}
            hint={m?.password_set ? "Set; type a new one to replace it" : undefined}
          >
            <Input
              id="nsxm-password"
              type="password"
              autoComplete="new-password"
              placeholder={m?.password_set ? "••••••••" : undefined}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field
            label="Certificate file"
            htmlFor="nsxm-cert"
            error={errors["/cert_file"]}
            hint="A principal identity's, on rvav's servers: instead of the account"
          >
            <Input
              id="nsxm-cert"
              className="font-mono text-secondary"
              placeholder="/etc/rvav/nsx02.crt"
              value={f.cert_file}
              onChange={(e) => setF({ ...f, cert_file: e.target.value })}
            />
          </Field>
          <Field label="Key file" htmlFor="nsxm-key" error={errors["/key_file"]} hint="The certificate's private key">
            <Input
              id="nsxm-key"
              className="font-mono text-secondary"
              placeholder="/etc/rvav/nsx02.key"
              value={f.key_file}
              onChange={(e) => setF({ ...f, key_file: e.target.value })}
            />
          </Field>
        </div>
        <Field
          label="Trusted certificate (SHA-256)"
          htmlFor="nsxm-pin"
          error={errors["/cert_sha256"]}
          hint="A self-signed one, trusted besides the CAs: Trust this certificate, after a test, sets it"
        >
          <Input
            id="nsxm-pin"
            className="font-mono text-secondary"
            value={f.cert_sha256}
            onChange={(e) => setF({ ...f, cert_sha256: e.target.value })}
          />
        </Field>
        <label htmlFor="nsxm-insecure" className="flex items-center gap-1.5 text-body">
          <Checkbox id="nsxm-insecure" checked={f.insecure} onChange={(on) => setF({ ...f, insecure: on })} />
          Don't check its certificate
        </label>
        <VCenterChoice
          self={m?.name ?? f.name}
          managers={managers}
          chosen={vcenters}
          onChange={setVCenters}
          error={vcentersError(errors)}
        />
        {test.data && <TestResult test={test.data} />}
        {test.data?.certificate && (
          <ServerCertificate
            cert={test.data.certificate}
            onTrust={(sha256) => {
              // The settings change: test again with them.
              setF({ ...f, cert_sha256: sha256, insecure: false });
              toast.info("Its SHA-256 is in the form", { description: "Test again, then save the NSX Manager." });
            }}
          />
        )}
      </form>
    </Sheet>
  );
}

function LinkSheet({ m, managers, onClose }: { m: Manager; managers: Manager[]; onClose: () => void }) {
  const qc = useQueryClient();
  const [vcenters, setVCenters] = useState(m.vcenters);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const link = useMutation({
    mutationFn: () =>
      call(api.PUT("/admin/nsx-managers/{name}/vcenters", { params: { path: { name: m.name } }, body: { vcenters } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "nsx-managers"] });
      toast.success(`${m.name}'s vCenters saved`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={link.isPending}
      title={`vCenters ${m.name} serves`}
      description="Deployments made before keep their segments and groups where they are: rvav deletes them there."
      onSubmit={() => {
        setErrors({});
        link.mutate();
      }}
      submitLabel={link.isPending ? "Saving…" : "Save vCenters"}
    >
      <VCenterChoice
        self={m.name}
        managers={managers}
        chosen={vcenters}
        onChange={setVCenters}
        error={vcentersError(errors)}
      />
    </Sheet>
  );
}

const columns: Column<Manager>[] = [
  {
    id: "name",
    header: "Name",
    sortValue: (m) => m.name,
    cell: (m) => (
      <>
        <span className="font-mono text-secondary">{m.name}</span>
        {m.primary && (
          <Badge tone="neutral" className="ml-2">
            The connection above
          </Badge>
        )}
      </>
    ),
  },
  {
    id: "address",
    header: "Address and account",
    cell: (m) => (
      <>
        <div className="font-mono text-secondary">{m.url}</div>
        <div className="text-secondary text-muted">
          {m.cert_file ? "principal identity" : m.username || "no account"}
          {m.insecure ? " · certificate not checked" : m.cert_sha256 ? " · certificate pinned" : ""}
        </div>
      </>
    ),
  },
  {
    id: "vcenters",
    header: "vCenters",
    cell: (m) =>
      m.vcenters.length ? (
        <span className="font-mono text-secondary">{m.vcenters.join(", ")}</span>
      ) : (
        <span className="text-secondary text-muted">None yet</span>
      ),
  },
];

export function NSXManagers() {
  const qc = useQueryClient();
  const list = useQuery(adm.nsxManagers());
  const managers = list.data?.items ?? [];
  const [editing, setEditing] = useState<Manager | "new" | null>(null);
  const [linking, setLinking] = useState<Manager | null>(null);
  const [removing, setRemoving] = useState<Manager | null>(null);
  const remove = useMutation({
    mutationFn: (name: string) => call(api.DELETE("/admin/nsx-managers/{name}", { params: { path: { name } } })),
    onSuccess: (_, name) => {
      void qc.invalidateQueries({ queryKey: ["admin", "nsx-managers"] });
      toast.success(`${name} removed`);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  return (
    <Panel
      title="NSX Managers"
      description="Each vCenter is served by one NSX Manager at most: the segments and security groups of its VMs are made there. A vCenter added later is served by none until you link it."
      flush
      actions={
        <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
          Add an NSX Manager
        </Button>
      }
    >
      <DataTable
        label="NSX Managers"
        columns={columns}
        rows={list.data?.items}
        rowKey={(m) => m.name}
        minWidth={640}
        loading={list.isPending}
        error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
        empty={
          <EmptyState
            icon={Network}
            title="No NSX Manager yet"
            body="Set the NSX connection above, or add an NSX Manager here."
          />
        }
        rowMenuLabel={(m) => `Actions for ${m.name}`}
        rowMenu={(m) => (
          <>
            {!m.primary && <MenuItem onSelect={() => setEditing(m)}>Change</MenuItem>}
            <MenuItem onSelect={() => setLinking(m)}>Link vCenters</MenuItem>
            {!m.primary && (
              <>
                <MenuSeparator />
                <MenuItem danger onSelect={() => setRemoving(m)}>
                  Remove
                </MenuItem>
              </>
            )}
          </>
        )}
      />
      {editing && (
        <ManagerSheet
          m={editing === "new" ? undefined : editing}
          managers={managers}
          onClose={() => setEditing(null)}
        />
      )}
      {linking && <LinkSheet m={linking} managers={managers} onClose={() => setLinking(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Stop managing ${removing.name}?`}
          description="rvav stops making segments and security groups on it, and its vCenters are served by none."
          consequences="It is refused while deployments have NSX segments or security groups on it. What it has stays in NSX."
          confirmLabel="Stop managing it"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.name, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
