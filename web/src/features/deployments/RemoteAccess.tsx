// The users people sign in to a deployment's VMs as, and the private key
// rvav made for it, handed to its owner once (ADR-0039).
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { CopyText, Time } from "@/components/common/misc";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Field, Textarea } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import { api, call, isApiError } from "@/lib/api/client";
import type { DeploymentDetail, GeneratedKey, PrivateKey } from "@/lib/api/types";
import { useCurrentProject } from "@/lib/project";
import { keys } from "@/lib/query";
import { copyText } from "@/lib/utils";

/** A file name for the key: the deployment's name, as ssh-keygen would name it. */
function keyFile(d: DeploymentDetail) {
  return `${d.name.replace(/[^A-Za-z0-9._-]+/g, "-")}.pem`;
}

/** The shell commands that sign in with the key: the same in every language. */
const signIn = (file: string, username: string, host: string) =>
  `chmod 600 ${file} && ssh -i ${file} ${username}@${host}`;

function save(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "application/x-pem-file" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}

function KeySheet({
  d,
  generated,
  open,
  onOpenChange,
}: {
  d: DeploymentDetail;
  generated: GeneratedKey;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation("deployments");
  const qc = useQueryClient();
  const [key, setKey] = useState<PrivateKey | null>(null);
  const [taking, setTaking] = useState(false);
  // Not a useMutation: the mutation cache keeps a result for gcTime, even
  // after reset(), and the key must live in this sheet's state only.
  const take = async () => {
    setTaking(true);
    try {
      setKey(
        await call(api.POST("/deployments/{deploymentId}/private-key", { params: { path: { deploymentId: d.id } } })),
      );
    } catch (e) {
      toast.error(isApiError(e) ? (e.problem.detail ?? e.problem.title) : t("access.notCome"));
    } finally {
      setTaking(false);
      void qc.invalidateQueries({ queryKey: keys.deployments.detail(d.id) });
    }
  };
  const keyRef = useRef<HTMLTextAreaElement>(null);
  // The button that asked for it is gone: the focus goes to the key.
  useEffect(() => {
    if (key) keyRef.current?.focus();
  }, [key]);
  // Saved or copied: closing then needs no question.
  const [kept, setKept] = useState(false);
  const [leaving, setLeaving] = useState(false);
  const close = (o: boolean) => {
    if (!o && key && !kept) {
      setLeaving(true);
      return;
    }
    if (!o) {
      setKey(null);
      setKept(false);
    }
    onOpenChange(o);
  };
  const copy = (text: string) =>
    copyText(text).then(
      () => {
        setKept(true);
        toast.success(t("access.copied"));
      },
      () => toast.error(t("access.notCopied"), { description: t("access.notCopiedBody") }),
    );
  const host = d.primary_address?.fqdn ?? d.primary_address?.ip ?? "<address>";
  const file = keyFile(d);
  const shown = key
    ? {
        footer: (
          <Button variant="primary" onClick={() => close(false)}>
            {t("access.done")}
          </Button>
        ),
      }
    : { onSubmit: () => void take(), submitLabel: t("access.show"), pending: taking };
  return (
    <Sheet
      open={open}
      onOpenChange={close}
      title={key ? t("access.yours") : t("access.download")}
      description={t("access.for", { username: generated.username, fingerprint: generated.fingerprint })}
      {...shown}
    >
      {key ? (
        <>
          <p role="status" className="rounded-md border border-ok bg-ok-soft p-3 text-secondary">
            {t("access.saveNow")}
          </p>
          <Field label={t("access.privateKey")} htmlFor="private-key">
            <Textarea
              ref={keyRef}
              id="private-key"
              readOnly
              rows={9}
              value={key.private_key}
              className="font-mono text-caption"
              onFocus={(e) => e.currentTarget.select()}
              onCopy={() => setKept(true)}
            />
          </Field>
          <div className="flex flex-wrap gap-2">
            <Button
              onClick={() => {
                save(file, key.private_key);
                setKept(true);
              }}
            >
              {t("access.saveAs", { file })}
            </Button>
            <Button onClick={() => void copy(key.private_key)}>{t("access.copy")}</Button>
          </div>
          <div className="flex flex-col gap-1 text-secondary">
            <span className="text-muted">{t("access.signIn")}</span>
            <code className="rounded bg-sunken px-2 py-1 font-mono break-all">{signIn(file, key.username, host)}</code>
          </div>
        </>
      ) : (
        <>
          <p className="text-body">{t("access.once")}</p>
          <p className="text-secondary text-muted">
            <Trans t={t} i18nKey="access.where" components={{ path: <span className="font-mono" /> }} />
          </p>
        </>
      )}
      <ConfirmDialog
        open={leaving}
        onOpenChange={setLeaving}
        title={t("access.leaveTitle")}
        description={t("access.leaveBody")}
        confirmLabel={t("access.leave")}
        cancelLabel={t("access.back")}
        onConfirm={() => {
          setLeaving(false);
          setKey(null);
          setKept(false);
          onOpenChange(false);
        }}
      />
    </Sheet>
  );
}

/** Who may still download the private key, or who did. */
function KeyState({ d, generated, onTake }: { d: DeploymentDetail; generated: GeneratedKey; onTake: () => void }) {
  const { t } = useTranslation("deployments");
  const { me } = useCurrentProject();
  if (generated.available) {
    if (me.user.id !== d.owner.id)
      return <p className="text-secondary text-muted">{t("access.onlyOwner", { name: d.owner.name })}</p>;
    return (
      <div className="flex flex-col items-start gap-2">
        <p className="text-secondary text-muted">{t("access.madePair")}</p>
        <Button onClick={onTake}>{t("access.download")}</Button>
      </div>
    );
  }
  if (generated.downloaded_at)
    return (
      <p className="text-secondary text-muted">
        <Trans
          t={t}
          i18nKey="access.downloaded"
          values={{ name: generated.downloaded_by?.name ?? t("access.someone") }}
          components={{ time: <Time iso={generated.downloaded_at} /> }}
        />
      </p>
    );
  return <p className="text-secondary text-muted">{t("access.forgotten")}</p>;
}

/** The Remote access panel: shown when the blueprint gave the VMs a user. */
export function RemoteAccessPanel({ d }: { d: DeploymentDetail }) {
  const { t } = useTranslation("deployments");
  const [open, setOpen] = useState(false);
  const access = d.remote_access ?? [];
  if (access.length === 0) return null;
  const multi = access.length > 1 || access[0]!.component !== "vm";
  return (
    <Panel title={t("access.title")} description={t("access.lead")}>
      <div className="flex flex-col gap-3">
        <ul className="flex flex-col gap-2">
          {access.map((a) => (
            <li key={a.component} className="flex min-w-0 flex-col gap-0.5">
              <span className="text-body">
                {multi && <span className="text-muted">{t("access.component", { component: a.component })}</span>}
                <span className="font-mono">{a.username}</span>
              </span>
              <span className="text-secondary text-muted">{t(`access.how.${a.kind}`)}</span>
              {a.fingerprint && <CopyText value={a.fingerprint} className="text-caption" />}
            </li>
          ))}
        </ul>
        {d.generated_key && (
          <>
            <KeyState d={d} generated={d.generated_key} onTake={() => setOpen(true)} />
            <KeySheet d={d} generated={d.generated_key} open={open} onOpenChange={setOpen} />
          </>
        )}
      </div>
    </Panel>
  );
}
