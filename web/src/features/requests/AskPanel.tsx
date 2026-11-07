// A composite step that waits for a person (ADR-0033), on the request's page:
// those it names answer here, a form or approve and reject, and everyone who
// reads the request sees who it waits for, as for an approval (ADR-0011).
// What it asks comes from the server for those who may read it.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { toast } from "sonner";
import { ApprovalHistory, dueText } from "@/components/common/approvals";
import { Time } from "@/components/common/misc";
import { Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, call, isApiError } from "@/lib/api/client";
import type { Approval, ApprovalQuestionField } from "@/lib/api/types";
import { keys } from "@/lib/query";

type Value = string | number | boolean | undefined;

const initial = (f: ApprovalQuestionField): Value => {
  const d = f.default as Value | null;
  return d ?? (f.type === "boolean" ? false : "");
};

const fieldId = (approval: string, key: string) => `ask-${approval}-${key}`;

function Question({
  id,
  field,
  value,
  error,
  onChange,
}: {
  id: string;
  field: ApprovalQuestionField;
  value: Value;
  error?: string;
  onChange: (v: Value) => void;
}) {
  if (field.type === "boolean")
    return (
      <div className="flex flex-col gap-1">
        <div className="flex items-center gap-2 text-xs font-medium">
          <Checkbox id={id} checked={value === true} onChange={onChange} />
          <label htmlFor={id}>{field.label}</label>
        </div>
        {error && <p className="text-xs text-bad">{error}</p>}
      </div>
    );
  const required = field.required ? <span className="text-xs text-faint">Required</span> : undefined;
  if (field.type === "choice")
    return (
      <Field label={field.label} htmlFor={id} error={error} aside={required}>
        <Select
          id={id}
          value={typeof value === "string" ? value : ""}
          placeholder="Choose"
          options={(field.options ?? []).map((o) => ({ value: o, label: o }))}
          onChange={onChange}
        />
      </Field>
    );
  return (
    <Field label={field.label} htmlFor={id} error={error} aside={required}>
      <Input
        id={id}
        type={field.type === "number" ? "number" : "text"}
        value={value === undefined ? "" : String(value)}
        onChange={(e) => onChange(e.target.value)}
      />
    </Field>
  );
}

/** What a question's field holds, as the answer gives it: a number as such, blank left out. */
function answerValue(f: ApprovalQuestionField, v: Value): Value {
  if (v === "" || v === undefined) return undefined;
  return f.type === "number" ? Number(v) : v;
}

/** The answer of someone a step asks: its form, or approve and reject. */
function AnswerForm({ approval, onAnswered }: { approval: Approval; onAnswered: () => void }) {
  const qc = useQueryClient();
  const question = approval.question;
  const form = question?.form ?? [];
  const commentId = `ask-${approval.id}-comment`;
  const commentRef = useRef<HTMLTextAreaElement>(null);
  const [values, setValues] = useState<Record<string, Value>>(() =>
    Object.fromEntries(form.map((f) => [f.key, initial(f)])),
  );
  const [comment, setComment] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});

  const answer = useMutation({
    mutationFn: (decision: "approve" | "reject") =>
      call(
        api.POST("/approvals/{approvalId}/decision", {
          params: { path: { approvalId: approval.id } },
          body: {
            decision,
            comment: comment.trim() || undefined,
            answer:
              form.length > 0
                ? Object.fromEntries(
                    form.flatMap((f) => {
                      const v = answerValue(f, values[f.key]);
                      return v === undefined ? [] : [[f.key, v]];
                    }),
                  )
                : undefined,
          },
        }),
      ),
    onSuccess: (a, decision) => {
      qc.setQueryData(keys.approvals.detail(a.id), a);
      void qc.invalidateQueries({ queryKey: keys.approvals.lists() });
      void qc.invalidateQueries({ queryKey: keys.approvals.forRequest(a.request.id) });
      void qc.invalidateQueries({ queryKey: keys.requests.detail(a.request.number) });
      toast.success(decision === "reject" ? `Rejected: ${a.level.name}` : `Answer sent: ${a.level.name}`);
      onAnswered();
    },
    onError: (e) => {
      const found = isApiError(e) ? (e.problem.errors ?? []) : [];
      const next = Object.fromEntries(found.map((f) => [f.field, f.message]));
      setErrors(next);
      const first = found.find((f) => f.field.startsWith("/answer/") || f.field === "/comment");
      if (first) {
        if (first.field === "/comment") commentRef.current?.focus();
        else document.getElementById(fieldId(approval.id, first.field.slice("/answer/".length)))?.focus();
        return;
      }
      // Answered by someone else meanwhile, or no longer asked.
      void qc.invalidateQueries({ queryKey: keys.approvals.forRequest(approval.request.id) });
      toast.error(isApiError(e) ? e.problem.title : "The answer was not sent", {
        description: isApiError(e) ? (found[0]?.message ?? e.problem.detail) : undefined,
      });
    },
  });

  const reject = () => {
    if (!comment.trim()) {
      setErrors({ "/comment": "Say why you reject it: the requester sees this comment." });
      commentRef.current?.focus();
      return;
    }
    answer.mutate("reject");
  };

  return (
    <form
      aria-label={`Your answer: ${approval.level.name}`}
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        answer.mutate("approve");
      }}
    >
      <div className="flex flex-col gap-0.5 text-[13px]">
        <span className="font-medium">{approval.level.name}</span>
        {question?.message && <p className="whitespace-pre-wrap">{question.message}</p>}
        {approval.due_at && (
          <span className="text-xs text-muted">
            Due <Time iso={approval.due_at} />
            {dueText(approval) && `, ${dueText(approval)}`}
          </span>
        )}
      </div>
      {form.map((f) => (
        <Question
          key={f.key}
          id={fieldId(approval.id, f.key)}
          field={f}
          value={values[f.key]}
          error={errors[`/answer/${f.key}`]}
          onChange={(v) => {
            setValues((old) => ({ ...old, [f.key]: v }));
            setErrors((old) => ({ ...old, [`/answer/${f.key}`]: "" }));
          }}
        />
      ))}
      <Field
        label="Comment"
        htmlFor={commentId}
        error={errors["/comment"]}
        aside={<span className="text-xs text-faint">{form.length > 0 ? "Optional" : "Required to reject"}</span>}
      >
        <Textarea
          id={commentId}
          ref={commentRef}
          value={comment}
          onChange={(e) => {
            setComment(e.target.value);
            setErrors((old) => ({ ...old, "/comment": "" }));
          }}
        />
      </Field>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" variant="primary" disabled={answer.isPending}>
          {form.length > 0 ? "Send answer" : "Approve"}
        </Button>
        {form.length === 0 && (
          <Button type="button" variant="danger" disabled={answer.isPending} onClick={reject}>
            Reject
          </Button>
        )}
      </div>
    </form>
  );
}

/** A request's steps that wait for a person: those to answer, and what the others got. */
export function AskPanel({ rows }: { rows: Approval[] }) {
  const box = useRef<HTMLDivElement>(null);
  const mine = rows.filter((a) => a.can_decide);
  const others = rows.filter((a) => !a.can_decide);
  return (
    <Panel title="Waits for a person">
      {/* The form goes with its answer: the focus stays here, not on the page's top. */}
      <div ref={box} tabIndex={-1} className="flex flex-col gap-4 outline-none">
        {mine.map((a) => (
          <AnswerForm key={a.id} approval={a} onAnswered={() => box.current?.focus()} />
        ))}
        {others.length > 0 && (
          <div className={mine.length > 0 ? "border-t border-line pt-3.5" : undefined}>
            <ApprovalHistory levels={others} label="Steps that wait for a person" />
          </div>
        )}
      </div>
    </Panel>
  );
}
