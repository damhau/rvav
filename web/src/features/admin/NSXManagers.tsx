// Several NSX Managers (I1.1, ADR-0062): the NSX integration's first, then
// those administrators add. Each serves vCenters, and a VM's segments and
// security groups are made on its vCenter's.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Network } from "lucide-react";
import type { TFunction } from "i18next";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
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

const nameExample = "nsx02.corp.example";
const urlExample = "https://nsx02.corp.example";

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
  const { t } = useTranslation("admin");
  const id = useId();
  const vcenters = useQuery(adm.vcenters());
  const servedBy = (vc: string) => managers.find((m) => m.name !== self && m.vcenters.includes(vc))?.name;
  const names = vcenters.data?.items.map((v) => v.name) ?? [];
  return (
    <fieldset className="flex flex-col gap-2" aria-describedby={`${id}-note`}>
      <legend className="mb-1 text-secondary font-medium">{t("nsxManagers.served")}</legend>
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
                    {on ? t("nsxManagers.movesFrom", { name: other }) : t("nsxManagers.servedBy", { name: other })}
                  </span>
                )}
              </span>
            </label>
          );
        })}
      </div>
      <FieldNote id={`${id}-note`} error={error} hint={t("nsxManagers.servedNote")} />
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
  const { t } = useTranslation(["admin", "common"]);
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
      toast.success(t("vcenters.savedToast", { name: f.name }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("integrations.notSaved"));
    },
  });
  const test = useMutation({
    mutationFn: () =>
      call(api.POST("/admin/nsx-managers/{name}/test", { params: { path: { name: f.name || "new" } }, body: body() })),
    onMutate: () => setErrors({}),
    onSuccess: () => setErrors({}),
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("integrations.testDidntRun"));
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
      title={m ? t("vcenters.change", { name: m.name }) : t("nsxManagers.add")}
      description={t("nsxManagers.sheetDescription")}
      footer={
        <>
          <Button type="button" variant="secondary" onClick={onClose} disabled={save.isPending}>
            {t("common:actions.cancel")}
          </Button>
          <Button type="button" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? t("integrations.testing") : t("integrations.testConnection")}
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={save.isPending}>
            {save.isPending ? t("integrations.saving") : t(m ? "nsxManagers.save" : "nsxManagers.addSubmit")}
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
          label={t("vcenters.name")}
          htmlFor="nsxm-name"
          hint={m ? t("vcenters.fixed") : t("nsxManagers.nameHint")}
          error={errors["/name"]}
        >
          <Input
            id="nsxm-name"
            className="font-mono text-secondary"
            disabled={Boolean(m)}
            placeholder={nameExample}
            value={f.name}
            onChange={(e) => setF({ ...f, name: e.target.value })}
          />
        </Field>
        <Field label={t("nsxManagers.address")} htmlFor="nsxm-url" error={errors["/url"]}>
          <Input
            id="nsxm-url"
            className="font-mono text-secondary"
            placeholder={urlExample}
            value={f.url}
            onChange={(e) => setF({ ...f, url: e.target.value })}
          />
        </Field>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label={t("vcenters.account")} htmlFor="nsxm-user" error={errors["/username"]}>
            <Input id="nsxm-user" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} />
          </Field>
          <Field
            label={t("integrations.setting.password")}
            htmlFor="nsxm-password"
            error={errors["/password"]}
            hint={m?.password_set ? t("integrations.secretSet") : undefined}
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
            label={t("nsxManagers.certFile")}
            htmlFor="nsxm-cert"
            error={errors["/cert_file"]}
            hint={t("nsxManagers.certHint")}
          >
            <Input
              id="nsxm-cert"
              className="font-mono text-secondary"
              placeholder="/etc/rvav/nsx02.crt"
              value={f.cert_file}
              onChange={(e) => setF({ ...f, cert_file: e.target.value })}
            />
          </Field>
          <Field
            label={t("nsxManagers.keyFile")}
            htmlFor="nsxm-key"
            error={errors["/key_file"]}
            hint={t("nsxManagers.keyHint")}
          >
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
          label={t("integrations.pin.label")}
          htmlFor="nsxm-pin"
          error={errors["/cert_sha256"]}
          hint={t("integrations.pin.hint")}
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
          {t("vcenters.noCheck")}
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
              toast.info(t("integrations.pin.inForm"), { description: t("nsxManagers.testThenSave") });
            }}
          />
        )}
      </form>
    </Sheet>
  );
}

function LinkSheet({ m, managers, onClose }: { m: Manager; managers: Manager[]; onClose: () => void }) {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const [vcenters, setVCenters] = useState(m.vcenters);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const link = useMutation({
    mutationFn: () =>
      call(api.PUT("/admin/nsx-managers/{name}/vcenters", { params: { path: { name: m.name } }, body: { vcenters } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "nsx-managers"] });
      toast.success(t("nsxManagers.vcentersSaved", { name: m.name }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("integrations.notSaved"));
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={link.isPending}
      title={t("nsxManagers.linkTitle", { name: m.name })}
      description={t("nsxManagers.linkDescription")}
      onSubmit={() => {
        setErrors({});
        link.mutate();
      }}
      submitLabel={link.isPending ? t("integrations.saving") : t("nsxManagers.saveVCenters")}
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

const columns = (t: TFunction<"admin">): Column<Manager>[] => [
  {
    id: "name",
    header: t("vcenters.name"),
    sortValue: (m) => m.name,
    cell: (m) => (
      <>
        <span className="font-mono text-secondary">{m.name}</span>
        {m.primary && (
          <Badge tone="neutral" className="ml-2">
            {t("vcenters.primary")}
          </Badge>
        )}
      </>
    ),
  },
  {
    id: "address",
    header: t("vcenters.addressAccount"),
    cell: (m) => (
      <>
        <div className="font-mono text-secondary">{m.url}</div>
        <div className="text-secondary text-muted">
          {m.cert_file ? t("nsxManagers.principal") : m.username || t("vcenters.noAccount")}
          {m.insecure ? t("vcenters.notChecked") : m.cert_sha256 ? t("vcenters.pinned") : ""}
        </div>
      </>
    ),
  },
  {
    id: "vcenters",
    header: t("nsxManagers.vcenters"),
    cell: (m) =>
      m.vcenters.length ? (
        <span className="font-mono text-secondary">{m.vcenters.join(", ")}</span>
      ) : (
        <span className="text-secondary text-muted">{t("nsxManagers.noneYet")}</span>
      ),
  },
];

export function NSXManagers() {
  const { t } = useTranslation("admin");
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
      toast.success(t("vcenters.removedToast", { name }));
    },
    onError: (e) => failed(e, t("vcenters.notRemoved")),
  });
  return (
    <Panel
      title={t("nsxManagers.title")}
      description={t("nsxManagers.description")}
      flush
      actions={
        <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
          {t("nsxManagers.add")}
        </Button>
      }
    >
      <DataTable
        label={t("nsxManagers.title")}
        columns={columns(t)}
        rows={list.data?.items}
        rowKey={(m) => m.name}
        minWidth={640}
        loading={list.isPending}
        error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
        empty={<EmptyState icon={Network} title={t("nsxManagers.emptyTitle")} body={t("nsxManagers.emptyBody")} />}
        rowMenuLabel={(m) => t("vcenters.actionsFor", { name: m.name })}
        rowMenu={(m) => (
          <>
            {!m.primary && <MenuItem onSelect={() => setEditing(m)}>{t("vcenters.changeItem")}</MenuItem>}
            <MenuItem onSelect={() => setLinking(m)}>{t("nsxManagers.link")}</MenuItem>
            {!m.primary && (
              <>
                <MenuSeparator />
                <MenuItem danger onSelect={() => setRemoving(m)}>
                  {t("vcenters.remove")}
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
          title={t("vcenters.stopTitle", { name: removing.name })}
          description={t("nsxManagers.stopDescription")}
          consequences={t("nsxManagers.stopConsequences")}
          confirmLabel={t("vcenters.stop")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.name, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
