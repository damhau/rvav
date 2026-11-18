// The users people sign in to a deployment's VMs as, and the private key
// rvav made for it, handed to its owner once (ADR-0039).
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { CopyText, Time } from "@/components/common/misc";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Field, Textarea } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import { api, call, isApiError } from "@/lib/api/client";
import type { DeploymentDetail, GeneratedKey, PrivateKey, RemoteAccess as Access } from "@/lib/api/types";
import { useCurrentProject } from "@/lib/project";
import { keys } from "@/lib/query";
import { copyText } from "@/lib/utils";

const how: Record<Access["kind"], string> = {
  ssh_key: "With the SSH key given for it",
  generated_key: "With the deployment's key",
  password: "With the password given for it",
};

/** A file name for the key: the deployment's name, as ssh-keygen would name it. */
function keyFile(d: DeploymentDetail) {
  return `${d.name.replace(/[^A-Za-z0-9._-]+/g, "-")}.pem`;
}

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
      toast.error(isApiError(e) ? (e.problem.detail ?? e.problem.title) : "The private key didn't come");
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
        toast.success("Copied");
      },
      () => toast.error("Not copied", { description: "Your browser refused: select the key and copy it, or save it." }),
    );
  const host = d.primary_address?.fqdn ?? d.primary_address?.ip ?? "<address>";
  const file = keyFile(d);
  const shown = key
    ? {
        footer: (
          <Button variant="primary" onClick={() => close(false)}>
            Done
          </Button>
        ),
      }
    : { onSubmit: () => void take(), submitLabel: "Show the private key", pending: taking };
  return (
    <Sheet
      open={open}
      onOpenChange={close}
      title={key ? "Your private key" : "Download the private key"}
      description={`For ${generated.username}, key ${generated.fingerprint}`}
      {...shown}
    >
      {key ? (
        <>
          <p role="status" className="rounded-md border border-ok bg-ok-soft p-3 text-secondary">
            Save it now: rvav no longer has it.
          </p>
          <Field label="Private key" htmlFor="private-key">
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
              Save as {file}
            </Button>
            <Button onClick={() => void copy(key.private_key)}>Copy</Button>
          </div>
          <div className="flex flex-col gap-1 text-secondary">
            <span className="text-muted">Then sign in with it:</span>
            <code className="rounded bg-sunken px-2 py-1 font-mono break-all">
              chmod 600 {file} && ssh -i {file} {key.username}@{host}
            </code>
          </div>
        </>
      ) : (
        <>
          <p className="text-body">
            rvav gives the private key once. As it shows it to you, it forgets it: no one, you included, can download it
            again.
          </p>
          <p className="text-secondary text-muted">
            Save it where only you can read it, such as <span className="font-mono">~/.ssh</span> or your password
            manager.
          </p>
        </>
      )}
      <ConfirmDialog
        open={leaving}
        onOpenChange={setLeaving}
        title="Close without the private key?"
        description="You haven't saved or copied it, and rvav no longer has it: once you close, no one can download it again."
        confirmLabel="Close without it"
        cancelLabel="Back to the key"
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
  const { me } = useCurrentProject();
  if (generated.available) {
    if (me.user.id !== d.owner.id)
      return (
        <p className="text-secondary text-muted">
          Only the deployment's owner, {d.owner.name}, can download its private key.
        </p>
      );
    return (
      <div className="flex flex-col items-start gap-2">
        <p className="text-secondary text-muted">
          rvav made a key pair for this deployment. You can download its private key once.
        </p>
        <Button onClick={onTake}>Download the private key</Button>
      </div>
    );
  }
  if (generated.downloaded_at)
    return (
      <p className="text-secondary text-muted">
        {generated.downloaded_by?.name ?? "Someone"} downloaded the private key <Time iso={generated.downloaded_at} />.
        rvav no longer has it.
      </p>
    );
  return (
    <p className="text-secondary text-muted">
      rvav no longer has the private key: it was forgotten when the VMs were deleted.
    </p>
  );
}

/** The Remote access panel: shown when the blueprint gave the VMs a user. */
export function RemoteAccessPanel({ d }: { d: DeploymentDetail }) {
  const [open, setOpen] = useState(false);
  const access = d.remote_access ?? [];
  if (access.length === 0) return null;
  const multi = access.length > 1 || access[0]!.component !== "vm";
  return (
    <Panel title="Remote access" description="The user you sign in to the VMs as">
      <div className="flex flex-col gap-3">
        <ul className="flex flex-col gap-2">
          {access.map((a) => (
            <li key={a.component} className="flex min-w-0 flex-col gap-0.5">
              <span className="text-body">
                {multi && <span className="text-muted">{a.component}: </span>}
                <span className="font-mono">{a.username}</span>
              </span>
              <span className="text-secondary text-muted">{how[a.kind]}</span>
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
