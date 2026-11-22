// A file the requester attaches (F1.2, ADR-0040). It goes to rvav once
// chosen, which checks its type, size and content and keeps it for the
// request; the form holds its record.
import { useMutation, useMutationState } from "@tanstack/react-query";
import { Download, FileText, LoaderCircle, Upload, X } from "lucide-react";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { useTranslation } from "react-i18next";
import { i18n } from "@/lib/i18n";
import { formatList, formatSize } from "@/lib/format";
import { Button, IconButton } from "@/components/ui/button";
import { FieldNote } from "@/components/ui/input";
import { api, call, isApiError } from "@/lib/api/client";
import type { FileRef, UiField } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import type { WidgetProps } from "./widgets";

const defaultMB = 10;

const sendingKey = ["forms", "file"];

/** The files the form's file fields are still sending: until they're sent, it can't be submitted. */
export function useSendingFiles(): string[] {
  return useMutationState({
    filters: { mutationKey: sendingKey, status: "pending" },
    select: (m) => (m.state.variables as File).name,
  });
}

/** What the form says while files are still sending, if any. */
export function sendingText(names: string[]): string | undefined {
  if (names.length === 0) return undefined;
  return i18n.t("forms:file.wait", { names: formatList(names), count: names.length });
}

/** Whether an input's value is a file's record. */
export function isFileRef(v: unknown): v is FileRef {
  if (typeof v !== "object" || v === null) return false;
  const f = v as Partial<FileRef>;
  return (
    typeof f.id === "string" && f.id.startsWith("fil_") && typeof f.name === "string" && typeof f.size === "number"
  );
}

const oneDecimal = { minimumFractionDigits: 1, maximumFractionDigits: 1 };
const whole = { maximumFractionDigits: 0 };

/** A size for people: 912 B, 14.2 KB, 3.1 MB. */
export function fileSize(bytes: number): string {
  if (bytes < 1024) return formatSize(bytes, "B", whole);
  const kb = bytes / 1024;
  if (kb < 1024) return formatSize(kb, "KB", kb < 10 ? oneDecimal : whole);
  const mb = kb / 1024;
  return formatSize(mb, "MB", mb < 10 ? oneDecimal : whole);
}

/** What a file field takes, said under it. */
export function fileTakes(ui: UiField): string {
  const types = ui.accept ?? [];
  const size = formatSize(ui.max_size_mb ?? defaultMB, "MB");
  return types.length
    ? i18n.t("forms:file.takes", { types: types.join(", "), size })
    : i18n.t("forms:file.takesAny", { size });
}

/** A request's file, downloaded as itself: rvav never shows it in the page. */
export function FileDownload({ file }: { file: FileRef }) {
  const { t } = useTranslation("forms");
  const download = useMutation({
    mutationFn: async () => {
      const blob = await call(api.GET("/files/{fileId}", { params: { path: { fileId: file.id } }, parseAs: "blob" }));
      const url = URL.createObjectURL(blob);
      Object.assign(document.createElement("a"), { href: url, download: file.name }).click();
      URL.revokeObjectURL(url);
    },
    onError: (e) =>
      toast.error(isApiError(e) ? e.problem.title : t("file.downloadFailed", { name: file.name }), {
        description: isApiError(e) ? e.problem.detail : undefined,
      }),
  });
  return (
    <Button
      size="sm"
      variant="quiet"
      className="h-auto max-w-full justify-start px-1 py-0.5 font-sans"
      disabled={download.isPending}
      onClick={() => download.mutate()}
    >
      <Download />
      <span className="truncate">{file.name}</span>
      <span className="text-faint">{fileSize(file.size)}</span>
    </Button>
  );
}

export function FileWidget({
  id,
  name,
  ui,
  value,
  onChange,
  onBlur,
  ctx,
  invalid,
  describedBy,
  readOnly,
}: WidgetProps) {
  const { t } = useTranslation("forms");
  const input = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string>();
  const [dragging, setDragging] = useState(false);
  const file = isFileRef(value) ? value : undefined;
  const upload = useMutation({
    mutationKey: sendingKey,
    mutationFn: (f: File) =>
      call(
        api.POST("/forms/files", {
          params: { query: { version_id: ctx.versionId, project_id: ctx.projectId, field: name, name: f.name } },
          body: f as unknown as string,
          bodySerializer: (b: unknown) => b as BodyInit,
          headers: { "Content-Type": "application/octet-stream" },
        }),
      ),
    onSuccess: (ref) => {
      setError(undefined);
      onChange(ref);
    },
    onError: (e, f) =>
      setError(isApiError(e) ? (e.problem.detail ?? e.problem.title) : t("file.sendFailed", { name: f.name })),
    onSettled: onBlur,
  });
  const send = (f: File | undefined) => {
    if (!f) return;
    const max = ui.max_size_mb ?? defaultMB;
    if (f.size > max * 1024 * 1024)
      setError(t("file.tooBig", { name: f.name, size: fileSize(f.size), label: ui.label, max: formatSize(max, "MB") }));
    else if (f.size === 0) setError(t("file.empty", { name: f.name }));
    else upload.mutate(f);
  };
  const takesId = `${id}-takes`;
  const errorId = `${id}-error`;
  const described = [describedBy, error ? errorId : takesId].filter(Boolean).join(" ") || undefined;

  if (readOnly)
    return file ? (
      <p className="m-0 flex items-center gap-1.5 text-secondary">
        <FileText className="size-4 text-faint" aria-hidden />
        {file.name} <span className="text-faint">{fileSize(file.size)}</span>
      </p>
    ) : (
      <p className="m-0 text-secondary text-faint">{t("file.none")}</p>
    );

  return (
    <div
      className={cn(
        "flex min-w-0 flex-col gap-1.5 rounded-md border border-dashed border-line-strong p-2.5",
        dragging && "border-accent bg-sunken",
        (invalid || error) && "border-bad",
      )}
      onDragOver={(e) => {
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        send(e.dataTransfer.files[0]);
      }}
    >
      <input
        ref={input}
        type="file"
        hidden
        tabIndex={-1}
        accept={ui.accept?.join(",")}
        onChange={(e) => {
          send(e.target.files?.[0]);
          e.target.value = "";
        }}
      />
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        {file && (
          <span className="flex min-w-0 items-center gap-1.5 text-secondary">
            <FileText className="size-4 shrink-0 text-faint" aria-hidden />
            <span className="truncate font-medium">{file.name}</span>
            <span className="text-faint">{fileSize(file.size)}</span>
          </span>
        )}
        <Button
          id={id}
          size="sm"
          aria-labelledby={`${id}-label ${id}-action`}
          aria-describedby={described}
          aria-invalid={invalid || Boolean(error) || undefined}
          disabled={upload.isPending}
          onClick={() => input.current?.click()}
        >
          {upload.isPending ? <LoaderCircle className="animate-spin" /> : <Upload />}
          <span id={`${id}-action`}>{t(file ? "file.replace" : "file.choose")}</span>
        </Button>
        {file && (
          <IconButton
            label={t("remove", { name: file.name })}
            disabled={upload.isPending}
            onClick={() => {
              setError(undefined);
              onChange(undefined);
              document.getElementById(id)?.focus();
            }}
          >
            <X />
          </IconButton>
        )}
        <span role="status" className="text-caption text-muted">
          {upload.isPending ? t("file.sending", { name: upload.variables.name }) : ""}
        </span>
      </div>
      <FieldNote id={error ? errorId : takesId} error={error} hint={fileTakes(ui)} />
    </div>
  );
}
