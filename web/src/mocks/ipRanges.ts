// rvav's IP ranges in the mock backend (ADR-0063): ranges of a network
// EfficientIP doesn't manage, the addresses they gave, the networks they
// map, and what the reconcile report finds in them. Checks follow the
// server's where the UI shows them.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { audited } from "./governance";
import { networks, VCENTER } from "./inventory";
import type { Db } from "./seed";

type S = components["schemas"];

type Allocation = S["IpAllocation"] & {
  rangeId: string;
  /** No deployment has its VM any more: an orphan of the reconcile report. */
  orphan?: boolean;
};

interface RangeState {
  ranges: Omit<S["IpRange"], "size" | "used" | "free">[];
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

function present(st: RangeState, r: RangeState["ranges"][number]): S["IpRange"] {
  const size = sizeOf(r);
  const used = st.allocations.filter((a) => a.rangeId === r.id).length;
  return { ...r, size, used, free: Math.max(0, size - used) };
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

/** The server's checks of a range, those the form shows next to a field. */
function check(st: RangeState, w: S["IpRangeWrite"], id?: string): S["FieldError"][] {
  const out: S["FieldError"][] = [];
  if (!w.name.trim()) out.push(err("/name", "required", "Name the range."));
  if (!w.vcenter.trim()) out.push(err("/vcenter", "required", "Name the vCenter the network is in."));
  if (!w.network.trim()) out.push(err("/network", "required", "Name the port group or NSX segment."));
  else if (w.vcenter !== VCENTER || !networks.some((n) => n.name === w.network))
    out.push(err("/network", "unknown", `The inventory of ${w.vcenter} has no network ${w.network}.`));
  const p = prefixOf(w.cidr);
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
  addr("/gateway", w.gateway, "gateway");
  if (start !== undefined && end !== undefined && end < start)
    out.push(err("/end_address", "before_start", `The end comes before the start, ${w.start_address}.`));
  trimAll(w.dns_servers).forEach((d, i) => {
    if (toInt(d) === undefined && !d.includes(":")) out.push(err(`/dns_servers/${i}`, "invalid", "An IP address."));
  });
  trimAll(w.excluded).forEach((e, i) => {
    const s = spanOf(e);
    if (!s)
      out.push(
        err(`/excluded/${i}`, "invalid", "An IPv4 address, or two joined by a dash such as 10.51.0.50-10.51.0.59."),
      );
    else if (p && (s[0] < p.base || s[1] > p.last))
      out.push(err(`/excluded/${i}`, "outside", `The addresses must be in the subnet ${subnet}.`));
  });
  if (out.length > 0) return out;
  const r = { ...w, excluded: trimAll(w.excluded) };
  if (sizeOf(r) === 0)
    out.push(
      err("/end_address", "no_address", "The range gives no address: its exclusions and the gateway take them all."),
    );
  for (const o of st.ranges) {
    if (o.id === id || o.vcenter !== w.vcenter) continue;
    const os = toInt(o.start_address)!;
    const oe = toInt(o.end_address)!;
    if (o.network === w.network && (o.cidr !== subnet || o.gateway !== w.gateway.trim()))
      out.push(
        err(
          "/cidr",
          "differs",
          `The range ${o.name} of ${w.network} says otherwise: ranges of one network share its subnet.`,
        ),
      );
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
  if (old && given.length > 0) {
    if (old.vcenter !== w.vcenter || old.network !== w.network || old.cidr !== subnet || old.gateway !== w.gateway)
      out.push(
        err(
          "/network",
          "in_use",
          `VMs hold ${given.length} addresses from this range: its vCenter, network, subnet and gateway can't change.`,
        ),
      );
    const ok = usable({ ...r, cidr: subnet });
    for (const g of given)
      if (!ok(toInt(g.address)!))
        out.push(err("/excluded", "in_use", `A VM holds ${g.address}: the range must keep it.`));
  }
  return out;
}

export function ipRangeHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const st = () => rangeState(db);
  const find = (id: unknown) => st().ranges.find((r) => r.id === id);
  const save = async (request: Request, id?: string) => {
    await wait();
    const w = (await request.json()) as S["IpRangeWrite"];
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
      dns_domain: (w.dns_domain ?? "").trim().replace(/\.$/, ""),
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
      audited(db, "ip_range.delete", "ip_range", r.id, { name: r.name });
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
      st().allocations.splice(st().allocations.indexOf(a), 1);
      audited(db, "ip_range.release", "ip_range", r.id, { address: a.address, hostname: a.hostname });
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}
