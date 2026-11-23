// rvav's IP ranges in the mock backend (ADR-0063): ranges of a network
// EfficientIP doesn't manage, the addresses they gave, the networks they
// map, and what the reconcile report finds in them. Checks follow the
// server's where the UI shows them.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { audited } from "./governance";
import { networks, VCENTER } from "./inventory";
import type { Db } from "./seed";
import { unknownField } from "./strict";

type S = components["schemas"];

type Allocation = S["IpAllocation"] & {
  rangeId: string;
  /** No deployment has its VM any more: an orphan of the reconcile report. */
  orphan?: boolean;
};

interface RangeState {
  ranges: Omit<S["IpRange"], "size" | "used" | "taken" | "free">[];
  allocations: Allocation[];
  /** VMs of the inventory on a range's address, as vCenter reports them. */
  inventory: { vm: string; address: string; deployment_id?: string; deployment_name?: string }[];
  seq: number;
}

const toInt = (a: string): number | undefined => {
  const parts = a.trim().split(".");
  if (parts.length !== 4 || parts.some((p) => !/^\d{1,3}$/.test(p) || Number(p) > 255)) return undefined;
  return parts.reduce((n, p) => n * 256 + Number(p), 0);
};
const toAddr = (n: number) => [n >>> 24, (n >>> 16) & 255, (n >>> 8) & 255, n & 255].join(".");

function prefixOf(cidr: string): { base: number; last: number; bits: number } | undefined {
  const [addr, bits] = cidr.trim().split("/");
  const n = addr === undefined ? undefined : toInt(addr);
  const b = Number(bits);
  if (n === undefined || !/^\d+$/.test(bits ?? "") || b < 8 || b > 32) return undefined;
  const size = 2 ** (32 - b);
  const base = n - (n % size);
  return { base, last: base + size - 1, bits: b };
}

function spanOf(s: string): [number, number] | undefined {
  const [from, to] = s.split("-");
  const a = toInt(from ?? "");
  const b = to === undefined ? a : toInt(to);
  return a === undefined || b === undefined || b < a ? undefined : [a, b];
}

/** The addresses a range hands out, as the server's Usable. */
function usable(r: Pick<S["IpRange"], "cidr" | "start_address" | "end_address" | "gateway" | "excluded">) {
  const p = prefixOf(r.cidr);
  const start = toInt(r.start_address);
  const end = toInt(r.end_address);
  const gateway = toInt(r.gateway);
  const spans = r.excluded.map(spanOf).filter((s) => s !== undefined);
  return (n: number) =>
    p !== undefined &&
    start !== undefined &&
    end !== undefined &&
    n >= start &&
    n <= end &&
    n !== gateway &&
    !(p.bits <= 30 && (n === p.base || n === p.last)) &&
    !spans.some(([a, b]) => n >= a && n <= b);
}

function sizeOf(r: S["IpRangeWrite"] & { excluded: string[] }): number {
  const start = toInt(r.start_address);
  const end = toInt(r.end_address);
  if (start === undefined || end === undefined || end < start) return 0;
  const ok = usable(r);
  let n = 0;
  for (let a = start; a <= end; a++) if (ok(a)) n++;
  return n;
}

const ago = (days: number) => new Date(Date.now() - days * 86_400_000).toISOString();

export function rangeState(db: Db): RangeState {
  const d = db as Db & { ipRanges?: RangeState };
  if (d.ipRanges) return d.ipRanges;
  const lab = {
    id: "ipr_lab",
    name: "Test lab",
    description: "The test lab's VMs, outside EfficientIP.",
    vcenter: VCENTER,
    network: "dvpg-app-tst-vl410",
    cidr: "10.41.10.0/24",
    start_address: "10.41.10.200",
    end_address: "10.41.10.239",
    gateway: "10.41.10.1",
    dns_servers: ["10.41.10.53"],
    dns_search_domains: ["corp.example"],
    dns_domain: "lab.corp.example",
    excluded: ["10.41.10.210", "10.41.10.220-10.41.10.224"],
    created_at: ago(40),
    updated_at: ago(12),
  };
  const dep = { deployment_id: "dep_mock_lab", deployment_name: "web-tst-lab" };
  d.ipRanges = db.fresh
    ? { ranges: [], allocations: [], inventory: [], seq: 0 }
    : {
        ranges: [lab],
        allocations: [
          { rangeId: lab.id, address: "10.41.10.200", hostname: "web-tst-lab001", fqdn: "web-tst-lab001.lab.corp.example", ...dep, request_id: "req_lab", allocated_at: ago(12) },
          { rangeId: lab.id, address: "10.41.10.201", hostname: "web-tst-lab002", fqdn: "web-tst-lab002.lab.corp.example", ...dep, request_id: "req_lab", allocated_at: ago(12) },
          { rangeId: lab.id, address: "10.41.10.202", hostname: "web-tst-lab002", fqdn: "web-tst-lab002-backup.lab.corp.example", adapter: "backup", ...dep, request_id: "req_lab", allocated_at: ago(12) },
          { rangeId: lab.id, address: "10.41.10.205", hostname: "web-tst-lab009", fqdn: "web-tst-lab009.lab.corp.example", deployment_id: "dep_mock_gone", request_id: "req_gone", allocated_at: ago(30), orphan: true },
        ], // prettier-ignore
        inventory: [
          { vm: "web-tst-lab001", address: "10.41.10.200", ...dep },
          { vm: "lab-tst-build01", address: "10.41.10.230" },
          { vm: "web-tst-old01", address: "10.41.10.201" },
        ],
        seq: 1,
      };
  return d.ipRanges;
}

interface Held {
  address: string;
  holder: string;
  from: "vcenter" | "dns";
}

/** The addresses the range would give that it didn't, as the server's takenIn: a VM of the inventory holds them, or a DNS server is on them. */
function takenIn(st: RangeState, r: Parameters<typeof usable>[0] & { id?: string; dns_servers: string[] }): Held[] {
  const ok = usable(r);
  const given = new Set(st.allocations.filter((a) => a.rangeId === r.id).map((a) => a.address));
  const out: Held[] = [];
  const add = (address: string, holder: string, from: Held["from"]) => {
    const n = toInt(address);
    if (n === undefined || !ok(n) || given.has(address) || out.some((h) => h.address === address)) return;
    out.push({ address, holder, from });
  };
  r.dns_servers.forEach((d) => add(d, d, "dns"));
  st.inventory.forEach((v) => add(v.address, v.vm, "vcenter"));
  return out.sort((a, b) => toInt(a.address)! - toInt(b.address)!);
}

function present(st: RangeState, r: RangeState["ranges"][number]): S["IpRange"] {
  const size = sizeOf(r);
  const used = st.allocations.filter((a) => a.rangeId === r.id).length;
  const taken = takenIn(st, r).length;
  return { ...r, size, used, taken, free: Math.max(0, size - used - taken) };
}

/**
 * The next n addresses a network's IP ranges would give, after those hinted
 * already, as the server's hintsInRanges: fewer when they are full,
 * undefined for a network without ranges.
 */
export function rangeHints(db: Db, network: string, n: number, hinted: Set<string>): string[] | undefined {
  const st = rangeState(db);
  const rs = st.ranges.filter((r) => r.network === network && r.vcenter === VCENTER);
  if (rs.length === 0) return undefined;
  const held = new Set(st.allocations.map((a) => a.address));
  const out: string[] = [];
  for (const r of rs) {
    const ok = usable(r);
    const taken = new Set(takenIn(st, r).map((h) => h.address));
    const start = toInt(r.start_address) ?? 0;
    const end = toInt(r.end_address) ?? -1;
    for (let a = start; a <= end && out.length < n; a++) {
      const ip = toAddr(a);
      if (!ok(a) || held.has(ip) || taken.has(ip) || hinted.has(`${r.id} ${ip}`)) continue;
      hinted.add(`${r.id} ${ip}`);
      out.push(ip);
    }
  }
  return out;
}

/** The networks with ranges, once per vCenter, as the network mappings list shows them. */
export function rangeMappings(db: Db): S["NetworkMapping"][] {
  const seen = new Set<string>();
  const out: S["NetworkMapping"][] = [];
  for (const r of rangeState(db).ranges) {
    const k = `${r.vcenter}/${r.network}`;
    if (seen.has(k)) continue;
    seen.add(k);
    out.push({
      network: r.network,
      vcenter: r.vcenter,
      subnet: r.cidr,
      gateway: r.gateway,
      dns_zone: r.dns_domain,
      dns_servers: r.dns_servers,
      source: "rvav",
    });
  }
  return out;
}

/** What the reconcile report finds in the ranges. */
export function rangeReconcile(db: Db): Pick<S["IpamReconcile"], "orphan_allocations" | "unallocated"> {
  const st = rangeState(db);
  const rangeOf = (id: string) => st.ranges.find((r) => r.id === id);
  const orphan_allocations = st.allocations.flatMap((a) => {
    const r = rangeOf(a.rangeId);
    if (!a.orphan || !r) return [];
    const { rangeId: _r, orphan: _o, fqdn: _f, ...rest } = a;
    return [{ ...rest, range_id: r.id, range_name: r.name, vcenter: r.vcenter, network: r.network }];
  });
  const unallocated = st.inventory.flatMap((v) => {
    const n = toInt(v.address);
    const r = st.ranges.find((x) => n !== undefined && usable(x)(n));
    if (!r) return [];
    const holder = st.allocations.find((a) => a.rangeId === r.id && a.address === v.address);
    if (holder?.hostname === v.vm) return [];
    return [
      {
        ...v,
        vcenter: r.vcenter,
        range_id: r.id,
        range_name: r.name,
        network: r.network,
        ...(holder ? { held_by: holder.hostname } : {}),
      },
    ];
  });
  return { orphan_allocations, unallocated };
}

function problem(status: number, title: string, errors?: S["FieldError"][], detail?: string) {
  return HttpResponse.json(
    {
      type: "https://rvav.dev/problems/x",
      title,
      status,
      request_id: "req_mock",
      ...(errors ? { errors } : {}),
      ...(detail ? { detail } : {}),
    },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

const err = (field: string, code: string, message: string): S["FieldError"] => ({ field, code, message });

const trimAll = (list?: string[]) => (list ?? []).map((s) => s.trim()).filter(Boolean);
const sameList = (a: string[], b: string[]) => a.length === b.length && a.every((x, i) => x === b[i]);
// As the server's domainName.
const domainName = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$/i;

/** The server's checks of a range, those the form shows next to a field. */
function check(st: RangeState, w: S["IpRangeWrite"], id?: string): S["FieldError"][] {
  const out: S["FieldError"][] = [];
  if (!w.name.trim()) out.push(err("/name", "required", "Name the range."));
  if (!w.vcenter.trim()) out.push(err("/vcenter", "required", "Name the vCenter the network is in."));
  if (!w.network.trim()) out.push(err("/network", "required", "Name the port group or NSX segment."));
  else if (w.vcenter !== VCENTER || !networks.some((n) => n.name === w.network))
    out.push(err("/network", "unknown", `The inventory of ${w.vcenter} has no network ${w.network}.`));
  const parsed = prefixOf(w.cidr);
  // A subnet with host bits set, such as 10.31.0.7/24, is refused, not made 10.31.0.0/24.
  const p = parsed && toInt(w.cidr.trim().split("/")[0]!) === parsed.base ? parsed : undefined;
  if (!p) out.push(err("/cidr", "invalid", "An IPv4 subnet such as 10.51.0.0/24."));
  const subnet = p ? `${toAddr(p.base)}/${p.bits}` : "";
  const addr = (field: string, value: string, what: string) => {
    const n = toInt(value);
    if (n === undefined) out.push(err(field, "invalid", "An IPv4 address."));
    else if (p && (n < p.base || n > p.last))
      out.push(err(field, "outside", `The ${what} must be in the subnet ${subnet}.`));
    return n;
  };
  const start = addr("/start_address", w.start_address, "start");
  const end = addr("/end_address", w.end_address, "end");
  const gateway = addr("/gateway", w.gateway, "gateway");
  if (start !== undefined && end !== undefined && end < start)
    out.push(err("/end_address", "before_start", `The end comes before the start, ${w.start_address}.`));
  if (p && p.bits <= 30 && (gateway === p.base || gateway === p.last))
    out.push(err("/gateway", "invalid", "The gateway can't be the subnet's network or broadcast address."));
  trimAll(w.dns_servers).forEach((d, i) => {
    if (toInt(d) === undefined && !d.includes(":")) out.push(err(`/dns_servers/${i}`, "invalid", "An IP address."));
  });
  trimAll(w.dns_search_domains).forEach((d, i) => {
    if (!domainName.test(d.replace(/\.$/, "")))
      out.push(err(`/dns_search_domains/${i}`, "invalid", "A domain such as lab.corp.example."));
  });
  const domain = (w.dns_domain ?? "").trim();
  const bare = domain.replace(/\.$/, "");
  if (domain && (!bare || !domainName.test(bare)))
    out.push(err("/dns_domain", "invalid", "A domain such as lab.corp.example, or empty for the blueprint's."));
  else if (bare !== domain) out.push(err("/dns_domain", "not_canonical", `Without the final dot: ${bare}.`));
  trimAll(w.excluded).forEach((e, i) => {
    const s = spanOf(e);
    const canonical = s && (s[0] === s[1] ? toAddr(s[0]) : `${toAddr(s[0])}-${toAddr(s[1])}`);
    if (!s)
      out.push(
        err(`/excluded/${i}`, "invalid", "An IPv4 address, or two joined by a dash such as 10.51.0.50-10.51.0.59."),
      );
    else if (p && (s[0] < p.base || s[1] > p.last))
      out.push(err(`/excluded/${i}`, "outside", `The addresses must be in the subnet ${subnet}.`));
    else if (canonical !== e) out.push(err(`/excluded/${i}`, "not_canonical", `Write it ${canonical}.`));
  });
  if (out.length > 0) return out;
  const r = { ...w, excluded: trimAll(w.excluded) };
  const inSpan = usable({ ...r, cidr: subnet });
  trimAll(w.dns_servers).forEach((d, i) => {
    const n = toInt(d);
    if (n !== undefined && inSpan(n))
      out.push(err(`/dns_servers/${i}`, "in_range", `The range would give ${d}, a DNS server, to a VM: exclude it.`));
  });
  if (out.length > 0) return out;
  if (sizeOf(r) === 0)
    out.push(
      err("/end_address", "no_address", "The range gives no address: its exclusions and the gateway take them all."),
    );
  for (const o of st.ranges) {
    if (o.id === id || o.vcenter !== w.vcenter) continue;
    const os = toInt(o.start_address)!;
    const oe = toInt(o.end_address)!;
    if (o.network === w.network) {
      const differs = (field: string, what: string, same: boolean) => {
        if (!same)
          out.push(
            err(
              field,
              "differs",
              `The range ${o.name} of ${w.network} says otherwise: ranges of one network share its ${what}.`,
            ),
          );
      };
      differs("/cidr", "subnet", o.cidr === subnet);
      differs("/gateway", "gateway", o.gateway === w.gateway.trim());
      differs("/dns_servers", "DNS servers", sameList(o.dns_servers, trimAll(w.dns_servers)));
      differs("/dns_search_domains", "search domains", sameList(o.dns_search_domains, trimAll(w.dns_search_domains)));
      differs("/dns_domain", "DNS domain", o.dns_domain === domain);
    }
    if (start! <= oe && os <= end!)
      out.push(
        err(
          "/start_address",
          "overlaps",
          `The range overlaps ${o.name} (${o.start_address} to ${o.end_address}, on ${o.network}).`,
        ),
      );
  }
  const given = st.allocations.filter((a) => a.rangeId === id);
  const old = st.ranges.find((o) => o.id === id);
  const was = old ? usable(old) : () => false;
  const held = takenIn(st, { ...r, id, cidr: subnet, dns_servers: [] }).filter((h) => !was(toInt(h.address)!));
  const heldList = [
    ...held.slice(0, 5).map((h) => `${h.address} (${h.holder}, as vCenter reports)`),
    ...(held.length > 5 ? [`and ${held.length - 5} more`] : []),
  ].join(", ");
  if (held.length === 1)
    out.push(
      err(
        "/excluded",
        "held",
        `A VM holds an address the range would give: ${heldList}. Exclude it, or keep the range apart from it.`,
      ),
    );
  else if (held.length > 1)
    out.push(
      err(
        "/excluded",
        "held",
        `VMs hold addresses the range would give: ${heldList}. Exclude them, or keep the range apart from them.`,
      ),
    );
  if (old && given.length > 0) {
    const holders = given.length === 1 ? "A VM holds 1 address" : `VMs hold ${given.length} addresses`;
    const kept: [string, boolean][] = [
      ["/vcenter", old.vcenter === w.vcenter],
      ["/network", old.network === w.network],
      ["/cidr", old.cidr === subnet],
      ["/gateway", old.gateway === w.gateway],
    ];
    for (const [field, same] of kept)
      if (!same)
        out.push(
          err(field, "in_use", `${holders} from this range: its vCenter, network, subnet and gateway can't change.`),
        );
    const ok = usable({ ...r, cidr: subnet });
    const spans = r.excluded.map(spanOf);
    for (const g of given) {
      const n = toInt(g.address)!;
      if (ok(n)) continue;
      const span = spans.findIndex((s) => s !== undefined && n >= s[0] && n <= s[1]);
      const field =
        n < start!
          ? "/start_address"
          : n > end!
            ? "/end_address"
            : n === gateway
              ? "/gateway"
              : span >= 0
                ? `/excluded/${span}`
                : "/cidr";
      // An address lost to a new gateway or subnet is refused there already, as the server does.
      if (field !== "/gateway" && field !== "/cidr")
        out.push(err(field, "in_use", `A VM holds ${g.address}: the range must keep it.`));
    }
  }
  return out;
}

const rangeFields = [
  "name",
  "description",
  "vcenter",
  "network",
  "cidr",
  "start_address",
  "end_address",
  "gateway",
  "dns_servers",
  "dns_search_domains",
  "dns_domain",
  "excluded",
] as const;

export function ipRangeHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const st = () => rangeState(db);
  const find = (id: unknown) => st().ranges.find((r) => r.id === id);
  const forbidden = () =>
    db.me.permissions.includes("infrastructure.manage")
      ? undefined
      : problem(403, "Not allowed", undefined, "You do not have permission to do this.");
  const save = async (request: Request, id?: string) => {
    await wait();
    const refused = forbidden();
    if (refused) return refused;
    const w = (await request.json()) as S["IpRangeWrite"];
    const strict = unknownField(w, rangeFields);
    if (strict) return strict;
    const errors = check(st(), w, id);
    if (errors.length > 0) return problem(422, "Some inputs need attention", errors);
    const p = prefixOf(w.cidr)!;
    const now = new Date().toISOString();
    const old = id ? find(id) : undefined;
    const r = {
      id: id ?? `ipr_${++st().seq}`,
      name: w.name.trim(),
      description: w.description?.trim() ?? "",
      vcenter: w.vcenter.trim(),
      network: w.network.trim(),
      cidr: `${toAddr(p.base)}/${p.bits}`,
      start_address: w.start_address.trim(),
      end_address: w.end_address.trim(),
      gateway: w.gateway.trim(),
      dns_servers: trimAll(w.dns_servers),
      dns_search_domains: trimAll(w.dns_search_domains),
      dns_domain: (w.dns_domain ?? "").trim(),
      excluded: trimAll(w.excluded),
      created_at: old?.created_at ?? now,
      updated_at: now,
    };
    if (old) Object.assign(old, r);
    else st().ranges.push(r);
    audited(db, id ? "ip_range.update" : "ip_range.create", "ip_range", r.id, { name: r.name });
    return HttpResponse.json(present(st(), r), { status: id ? 200 : 201 });
  };
  return [
    http.get(`${base}/admin/ip-ranges`, async () => {
      await wait();
      return HttpResponse.json({ items: st().ranges.map((r) => present(st(), r)) });
    }),
    http.post(`${base}/admin/ip-ranges`, ({ request }) => save(request)),
    http.get(`${base}/admin/ip-ranges/:id`, async ({ params }) => {
      await wait();
      const r = find(params.id);
      return r ? HttpResponse.json(present(st(), r)) : problem(404, "Not found");
    }),
    http.put(`${base}/admin/ip-ranges/:id`, ({ params, request }) =>
      find(params.id) ? save(request, String(params.id)) : problem(404, "Not found"),
    ),
    http.delete(`${base}/admin/ip-ranges/:id`, async ({ params }) => {
      await wait();
      const refused = forbidden();
      if (refused) return refused;
      const r = find(params.id);
      if (!r) return problem(404, "Not found");
      if (st().allocations.some((a) => a.rangeId === r.id))
        return problem(
          409,
          "Conflict",
          undefined,
          `VMs hold addresses from ${r.name}: delete them, or move their adapters, first.`,
        );
      st().ranges.splice(st().ranges.indexOf(r), 1);
      audited(db, "ip_range.delete", "ip_range", r.id, { name: r.name }, r.name);
      return new HttpResponse(null, { status: 204 });
    }),
    http.get(`${base}/admin/ip-ranges/:id/allocations`, async ({ params }) => {
      await wait();
      const r = find(params.id);
      if (!r) return problem(404, "Not found");
      const items = st()
        .allocations.filter((a) => a.rangeId === r.id)
        .sort((a, b) => toInt(a.address)! - toInt(b.address)!)
        .map(({ rangeId: _r, orphan: _o, ...a }) => a);
      return HttpResponse.json({ items });
    }),
    http.delete(`${base}/admin/ip-ranges/:id/allocations/:address`, async ({ params }) => {
      await wait();
      const r = find(params.id);
      const a = st().allocations.find((x) => x.rangeId === params.id && x.address === params.address);
      if (!r || !a) return problem(404, "Not found");
      if (!a.orphan)
        return problem(
          409,
          "Conflict",
          undefined,
          `${a.hostname} still holds ${a.address}: delete the VM, or move its adapter, and rvav gives the address back.`,
        );
      const seen = st()
        .inventory.filter((v) => v.address === a.address)
        .map((v) => v.vm)
        .sort()[0];
      if (seen)
        return problem(
          409,
          "Conflict",
          undefined,
          `vCenter ${r.vcenter} still reports VM ${seen} on ${a.address}: delete the VM, or change its address, first.`,
        );
      st().allocations.splice(st().allocations.indexOf(a), 1);
      audited(db, "ip_range.release", "ip_range", r.id, { address: a.address, hostname: a.hostname });
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
