// Lease rules and lifetimes (ADR-0048), as the server decides them: the
// rules by priority, then the project's override, then the environment's
// lease; a project admin's rule never gives more than what administrators set.
import type { components } from "@/lib/api/schema";
import { adminState } from "./administration";
import { celEval } from "./cel";
import type { Db } from "./seed";

type S = components["schemas"];

export type LeaseTerms = Pick<
  S["LeasePolicy"],
  "default_days" | "max_days" | "max_extensions" | "grace_days" | "expiry_action" | "enforcement" | "max_lifetime_days"
>;

const looserErr = (field: string, message: string): S["FieldError"] => ({ field, code: "looser", message });
const invalidErr = (field: string, code: string, message: string): S["FieldError"] => ({ field, code, message });

/** As the server's leaseTerms: what a lease policy or rule gives, checked. */
export function leaseTermsErrors(l: LeaseTerms): S["FieldError"][] {
  const errors: S["FieldError"][] = [];
  for (const k of ["default_days", "max_days", "max_lifetime_days"] as const) {
    const v = l[k];
    if (v != null && v < 1) errors.push(invalidErr(`/${k}`, "minimum", "At least a day, or none."));
  }
  if (l.default_days != null && l.max_days != null && l.default_days > l.max_days)
    errors.push(invalidErr("/default_days", "maximum", "The default can't be longer than the maximum."));
  if (l.max_lifetime_days != null && l.max_days != null && l.max_days > l.max_lifetime_days)
    errors.push(invalidErr("/max_days", "maximum", "The maximum can't be longer than the lifetime."));
  else if (l.max_lifetime_days != null && l.default_days != null && l.default_days > l.max_lifetime_days)
    errors.push(invalidErr("/default_days", "maximum", "The default can't be longer than the lifetime."));
  if (l.max_extensions < 0 || l.grace_days < 0)
    errors.push(invalidErr("/max_extensions", "minimum", "Extensions and grace days are zero or more."));
  return errors;
}

/** As the server's looser: what l gives beyond ref, named name (ADR-0045, ADR-0048). */
export function looserLease(l: LeaseTerms, ref: LeaseTerms, name: string): S["FieldError"][] {
  if (ref.default_days == null && ref.max_days == null && ref.max_lifetime_days == null) return [];
  const out: S["FieldError"][] = [];
  if (ref.default_days != null && l.default_days == null)
    out.push(looserErr("/default_days", `${name} gives deployments a lease: give them one.`));
  if (ref.max_days != null && (l.max_days == null || l.max_days > ref.max_days))
    out.push(looserErr("/max_days", `${name} allows ${ref.max_days} days at most.`));
  if (l.max_extensions > ref.max_extensions)
    out.push(looserErr("/max_extensions", `${name} allows ${ref.max_extensions} extensions at most.`));
  if (l.grace_days > ref.grace_days)
    out.push(looserErr("/grace_days", `${name} gives ${ref.grace_days} grace days at most.`));
  const rank = { notify_only: 0, power_off_then_delete: 1, delete: 2 };
  if (rank[l.expiry_action] < rank[ref.expiry_action])
    out.push(looserErr("/expiry_action", `${name}'s leases end with ${ref.expiry_action.replaceAll("_", " ")}.`));
  if (ref.max_lifetime_days != null && (l.max_lifetime_days == null || l.max_lifetime_days > ref.max_lifetime_days))
    out.push(looserErr("/max_lifetime_days", `${name} lets deployments live ${ref.max_lifetime_days} days at most.`));
  if (l.enforcement === "soft" && ref.enforcement !== "soft")
    out.push(looserErr("/enforcement", `${name}'s lease is hard: yours must be too.`));
  return out;
}

const envName = (db: Db, key: string) => db.environments.find((e) => e.key === key)?.name ?? key;
const projectName = (db: Db, id: string) => db.projects.find((p) => p.id === id)?.name ?? id;

/** As the server's adminLease: the project's override an administrator set, else the environment's lease. */
export function adminLease(db: Db, projectId: string, env: string): { terms: LeaseTerms; name: string } | undefined {
  const all = adminState(db).leases.filter((l) => l.environment === env);
  const set = all.find((l) => l.project_id === projectId && l.set_by_admin);
  if (set) return { terms: set, name: `${projectName(db, projectId)} in ${envName(db, env)}` };
  const own = all.find((l) => !l.project_id);
  return own ? { terms: own, name: envName(db, env) } : undefined;
}

/** As the server's looserRule: what a project admin's rule gives beyond the administrators' lease, where it applies. */
export function looserRule(db: Db, projectId: string, env: string | null | undefined, terms: LeaseTerms) {
  const envs = env ? [env] : (db.projects.find((p) => p.id === projectId)?.environments ?? []);
  const out: S["FieldError"][] = [];
  for (const e of envs) {
    const ref = adminLease(db, projectId, e);
    if (!ref) continue;
    for (const x of looserLease(terms, ref.terms, ref.name)) if (!out.some((y) => y.field === x.field)) out.push(x);
  }
  return out;
}

/** The lease rules in the order they're tried: by priority, global ones first, then by name. */
export function orderedRules(rules: S["LeaseRule"][]): S["LeaseRule"][] {
  return [...rules].sort(
    (a, b) =>
      (a.priority ?? 100) - (b.priority ?? 100) ||
      Number(Boolean(a.project_id)) - Number(Boolean(b.project_id)) ||
      a.name.localeCompare(b.name) ||
      a.id.localeCompare(b.id),
  );
}

export interface LeaseChoice {
  terms: LeaseTerms;
  /** What decides, by name: a rule, "<project> in <environment>" or the environment; null for nothing. */
  policy: string | null;
  rule?: S["LeaseRule"];
  considered: S["PolicyConsidered"][];
}

const noLease: LeaseTerms = {
  default_days: null,
  max_days: null,
  max_extensions: 0,
  grace_days: 7,
  expiry_action: "notify_only",
  enforcement: "hard",
  max_lifetime_days: null,
};

/** As the server's chooseLease: the lease a request gets, and why each rule and policy decides or not. */
export function chooseLease(
  db: Db,
  projectId: string,
  env: string,
  vars: Record<string, unknown>,
  strict: boolean,
  full = true,
): LeaseChoice {
  const st = adminState(db);
  const override = st.leases.find((l) => l.environment === env && l.project_id === projectId);
  const own = st.leases.find((l) => l.environment === env && !l.project_id);
  const base = override ?? own;
  const baseName = override ? `${projectName(db, projectId)} in ${envName(db, env)}` : envName(db, env);
  const considered: S["PolicyConsidered"][] = [];
  const matched: { rule: S["LeaseRule"]; at: number; failed: boolean }[] = [];
  for (const r of orderedRules(st.leaseRules)) {
    if (!full && r.project_id && r.project_id !== projectId) continue;
    const c: S["PolicyConsidered"] = { id: r.id, name: r.name, applies: false, why: "" };
    if (r.project_id && r.project_id !== projectId) c.why = "Only for another project.";
    else if (r.environment && r.environment !== env) c.why = `Only for ${envName(db, r.environment)}.`;
    else {
      let match: boolean | undefined;
      let failure = "";
      try {
        match = Boolean(celEval(r.condition, vars, true));
      } catch (e) {
        failure = e instanceof Error ? e.message : String(e);
      }
      if (match === undefined && !strict)
        c.why = `Its condition needs inputs not given here (${failure}); on a request it applies unless it gives more than the lease without rules.`;
      else if (match === undefined && base && looserLease(r, base, baseName).length > 0)
        c.why = `Its condition can't be evaluated (${failure}), and it gives more than ${baseName}.`;
      else if (match === undefined) {
        c.why = `Its condition can't be evaluated (${failure}): it applies, as it gives no more than the lease without rules.`;
        matched.push({ rule: r, at: considered.length, failed: true });
      } else if (!match) c.why = `Its condition ${r.condition} is false here.`;
      else matched.push({ rule: r, at: considered.length, failed: false });
    }
    considered.push(c);
  }
  const ruleAuthority = matched.find((t) => !t.rule.project_id || t.rule.set_by_admin);
  const authority = ruleAuthority
    ? { terms: ruleAuthority.rule as LeaseTerms, name: `the rule ${ruleAuthority.rule.name}` }
    : adminLease(db, projectId, env);
  let rule: S["LeaseRule"] | undefined;
  for (const t of matched) {
    const c = considered[t.at]!;
    if (rule) {
      c.why = "An earlier rule decides.";
      continue;
    }
    if (t.rule.project_id && !t.rule.set_by_admin && authority) {
      const looser = looserLease(t.rule, authority.terms, authority.name);
      if (looser.length) {
        c.why = `It would decide, but a project admin's rule can't give more than ${authority.name}: ${looser[0]!.message}`;
        continue;
      }
    }
    c.applies = true;
    if (!t.failed) c.why = `Decides: ${t.rule.condition}.`;
    rule = t.rule;
  }
  if (override)
    considered.push({
      id: `lease:${env}:${projectId}`,
      name: baseName,
      applies: !rule,
      why: rule ? "A lease rule decides first." : "The project's own lease in this environment.",
    });
  if (own)
    considered.push({
      id: `lease:${env}`,
      name: envName(db, env),
      applies: !rule && !override,
      why: rule
        ? "A lease rule decides first."
        : override
          ? "The project's own lease overrides it."
          : "The environment's lease.",
    });
  if (rule) return { terms: rule, policy: rule.name, rule, considered };
  return { terms: base ?? noLease, policy: base ? baseName : null, considered };
}

/** As the server's leaseTooLong: why a lease of days is longer than the choice allows, "" when it isn't. */
export function leaseTooLong(db: Db, c: LeaseChoice, env: string, days: number | null | undefined): string {
  const soft = c.terms.enforcement === "soft";
  const where = c.rule ? `under ${c.rule.name}` : `in ${c.policy ?? envName(db, env)}`;
  const { max_days: max, max_lifetime_days: life } = c.terms;
  if (days == null) return "";
  if (life != null && days > life && (max == null || life < max))
    return `Deployments ${where} ${soft ? "should live" : "live"} ${life} days at most.`;
  if (max != null && days > max) return `Leases ${where} ${soft ? "should be" : "can be"} at most ${max} days.`;
  return "";
}

/** The lease a deployment is under now, as the server's deployment row reads it: its rule, else the policies. */
export function deploymentLease(db: Db, d: S["DeploymentDetail"]): { terms: LeaseTerms; policy: string } | undefined {
  const st = adminState(db);
  const ruleId = st.leaseRuleOf[d.id];
  const rule = st.leaseRules.find(
    (r) =>
      r.id === ruleId &&
      (!r.project_id || r.project_id === d.project.id) &&
      (!r.environment || r.environment === d.environment),
  );
  if (rule) return { terms: rule, policy: rule.name };
  const override = st.leases.find((l) => l.environment === d.environment && l.project_id === d.project.id);
  if (override) return { terms: override, policy: `${d.project.name} in ${envName(db, d.environment)}` };
  const own = st.leases.find((l) => l.environment === d.environment && !l.project_id);
  return own ? { terms: own, policy: envName(db, d.environment) } : undefined;
}

/** The whole days a deployment may still live, as the server's lifetimeLeft; undefined without a lifetime. */
export function lifetimeLeft(d: S["DeploymentDetail"], lifetime: number | null | undefined, now = Date.now()) {
  if (lifetime == null) return undefined;
  const ends = new Date(d.created_at).getTime() + lifetime * 86_400_000;
  return Math.max(Math.floor((ends - now) / 86_400_000), 0);
}
