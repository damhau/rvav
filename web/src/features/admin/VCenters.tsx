// Several vCenters (S12.6, ADR-0024): the vCenter integration's first, then
// those administrators add, each with its account (the password sealed),
// synced and placed like the first.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleX, Server } from "lucide-react";
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
import { Field, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { fullDate, relative } from "@/lib/format";
import { failed, fieldErrors } from "./governance/common";
import { CheckLine } from "./IntegrationSettings";
import { ServerCertificate } from "./ServerCertificate";

type S = components["schemas"];
type VCenter = S["VCenter"];

const nameExample = "vc02.corp.example";
const urlExample = "https://vc02.corp.example/sdk";

export function TestResult({ test }: { test: S["IntegrationTest"] }) {
  const { t } = useTranslation("admin");
  return (
    <ul
      aria-label={t("vcenters.test")}
      aria-live="polite"
      className="flex flex-col gap-0.5 rounded-md border border-line p-3 text-secondary break-words"
    >
      {test.checks.map((c) => (
        <li key={c.name} className="flex items-start gap-1.5">
          {c.ok ? (
            <CircleCheck className="mt-px size-3.5 shrink-0 text-ok" aria-label={t("integrations.passed")} />
          ) : (
            <CircleX className="mt-px size-3.5 shrink-0 text-bad" aria-label={t("integrations.failed")} />
          )}
          <span>
            <CheckLine name={c.name} message={c.message} />
          </span>
        </li>
      ))}
    </ul>
  );
}

function VCenterSheet({ vc, onClose }: { vc?: VCenter; onClose: () => void }) {
  const qc = useQueryClient();
  const formId = useId();
  const [f, setFNow] = useState({
    name: vc?.name ?? "",
    url: vc?.url ?? "",
    username: vc?.username ?? "",
    insecure: vc?.insecure ?? false,
    cert_sha256: vc?.cert_sha256 ?? "",
  });
  const { t } = useTranslation(["admin", "common"]);
  const [password, setPasswordNow] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const body = (): S["VCenterWrite"] => ({ ...f, ...(password ? { password } : {}) });
  const save = useMutation({
    mutationFn: () =>
      vc
        ? call(api.PUT("/admin/vcenters/{name}", { params: { path: { name: vc.name } }, body: body() }))
        : call(api.POST("/admin/vcenters", { body: body() })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "vcenters"] });
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
      call(api.POST("/admin/vcenters/{name}/test", { params: { path: { name: f.name || "new" } }, body: body() })),
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
      title={vc ? t("vcenters.change", { name: vc.name }) : t("vcenters.add")}
      description={t("vcenters.sheetDescription")}
      footer={
        <>
          <Button type="button" variant="secondary" onClick={onClose} disabled={save.isPending}>
            {t("common:actions.cancel")}
          </Button>
          <Button type="button" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? t("integrations.testing") : t("integrations.testConnection")}
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={save.isPending}>
            {save.isPending ? t("integrations.saving") : t(vc ? "vcenters.save" : "vcenters.addSubmit")}
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
          htmlFor="vc-name"
          hint={vc ? t("vcenters.fixed") : t("vcenters.nameHint")}
          error={errors["/name"]}
        >
          <Input
            id="vc-name"
            className="font-mono text-secondary"
            disabled={Boolean(vc)}
            placeholder={nameExample}
            value={f.name}
            onChange={(e) => setF({ ...f, name: e.target.value })}
          />
        </Field>
        <Field label={t("vcenters.sdk")} htmlFor="vc-url" error={errors["/url"]}>
          <Input
            id="vc-url"
            className="font-mono text-secondary"
            placeholder={urlExample}
            value={f.url}
            onChange={(e) => setF({ ...f, url: e.target.value })}
          />
        </Field>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label={t("vcenters.account")} htmlFor="vc-user" error={errors["/username"]}>
            <Input id="vc-user" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value })} />
          </Field>
          <Field
            label={t("integrations.setting.password")}
            htmlFor="vc-password"
            error={errors["/password"]}
            hint={vc?.password_set ? t("integrations.secretSet") : undefined}
          >
            <Input
              id="vc-password"
              type="password"
              autoComplete="new-password"
              placeholder={vc?.password_set ? "••••••••" : undefined}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
        </div>
        <Field
          label={t("integrations.pin.label")}
          htmlFor="vc-pin"
          error={errors["/cert_sha256"]}
          hint={t("integrations.pin.hint")}
        >
          <Input
            id="vc-pin"
            className="font-mono text-secondary"
            value={f.cert_sha256}
            onChange={(e) => setF({ ...f, cert_sha256: e.target.value })}
          />
        </Field>
        <label htmlFor="vc-insecure" className="flex items-center gap-1.5 text-body">
          <Checkbox id="vc-insecure" checked={f.insecure} onChange={(on) => setF({ ...f, insecure: on })} />
          {t("vcenters.noCheck")}
        </label>
        {test.data && <TestResult test={test.data} />}
        {test.data?.certificate && (
          <ServerCertificate
            cert={test.data.certificate}
            onTrust={(sha256) => {
              // The settings change: test again with them.
              setF({ ...f, cert_sha256: sha256, insecure: false });
              toast.info(t("integrations.pin.inForm"), { description: t("vcenters.testThenSave") });
            }}
          />
        )}
      </form>
    </Sheet>
  );
}

const columns = (t: TFunction<"admin">): Column<VCenter>[] => [
  {
    id: "name",
    header: t("vcenters.name"),
    sortValue: (vc) => vc.name,
    cell: (vc) => (
      <>
        <span className="font-mono text-secondary">{vc.name}</span>
        {vc.primary && (
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
    cell: (vc) => (
      <>
        <div className="font-mono text-secondary">{vc.url}</div>
        <div className="text-secondary text-muted">
          {vc.username || t("vcenters.noAccount")}
          {vc.insecure ? t("vcenters.notChecked") : vc.cert_sha256 ? t("vcenters.pinned") : ""}
        </div>
      </>
    ),
  },
  {
    id: "inventory",
    header: t("vcenters.inventory"),
    cell: (vc) =>
      vc.inventory ? (
        <span className="text-secondary">
          {t("vcenters.counts", {
            clusters: t("vcenters.clusters", { count: vc.inventory.clusters ?? 0 }),
            vms: t("vcenters.vms", { count: vc.inventory.vms ?? 0 }),
          })}
          {vc.inventory.synced_at && (
            <span className="block text-muted" title={fullDate(vc.inventory.synced_at)}>
              {t("vcenters.synced", { when: relative(vc.inventory.synced_at) })}
            </span>
          )}
        </span>
      ) : (
        <span className="text-secondary text-muted">{t("vcenters.notSynced")}</span>
      ),
  },
];

export function VCenters() {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const list = useQuery(adm.vcenters());
  const [editing, setEditing] = useState<VCenter | "new" | null>(null);
  const [removing, setRemoving] = useState<VCenter | null>(null);
  const remove = useMutation({
    mutationFn: (name: string) => call(api.DELETE("/admin/vcenters/{name}", { params: { path: { name } } })),
    onSuccess: (_, name) => {
      void qc.invalidateQueries({ queryKey: ["admin", "vcenters"] });
      toast.success(t("vcenters.removedToast", { name }));
    },
    onError: (e) => failed(e, t("vcenters.notRemoved")),
  });
  return (
    <Panel
      title={t("vcenters.title")}
      description={t("vcenters.description")}
      flush
      actions={
        <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
          {t("vcenters.add")}
        </Button>
      }
    >
      <DataTable
        label={t("vcenters.title")}
        columns={columns(t)}
        rows={list.data?.items}
        rowKey={(vc) => vc.name}
        minWidth={640}
        loading={list.isPending}
        error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
        empty={<EmptyState icon={Server} title={t("vcenters.emptyTitle")} body={t("vcenters.emptyBody")} />}
        rowMenuLabel={(vc) => t("vcenters.actionsFor", { name: vc.name })}
        rowMenu={(vc) =>
          vc.primary ? null : (
            <>
              <MenuItem onSelect={() => setEditing(vc)}>{t("vcenters.changeItem")}</MenuItem>
              <MenuSeparator />
              <MenuItem danger onSelect={() => setRemoving(vc)}>
                {t("vcenters.remove")}
              </MenuItem>
            </>
          )
        }
      />
      {editing && <VCenterSheet vc={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("vcenters.stopTitle", { name: removing.name })}
          description={t("vcenters.stopDescription")}
          consequences={t("vcenters.stopConsequences")}
          confirmLabel={t("vcenters.stop")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.name, { onSuccess: () => setRemoving(null) })}
        />
      )}
    </Panel>
  );
}
