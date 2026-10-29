// Onboarding in the mock backend (S13.3): the VMs rvav doesn't manage, and
// adopting them into deployments, with the server's checks.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import type { DeploymentDetail, DnsRecordResource, IpAddressResource, VmResource } from "@/lib/api/types";
import { leasePolicyOf, memberRole } from "./administration";
import type { Db } from "./seed";

type S = components["schemas"];

interface Unmanaged {
  vm: S["OnboardingVm"];
  /** EfficientIP has its address and A and PTR records. */
  ipam: boolean;
}

const VCENTER = "vc01.corp.example";
const pad = (n: number, w = 2) => String(n).padStart(w, "0");

function unmanaged(db: Db): Unmanaged[] {
  const d = db as Db & { unmanaged?: Unmanaged[] };
  if (d.unmanaged) return d.unmanaged;
  let moref = 9100;
  const add = (name: string, ip: string | undefined, os: string, cpu: number, memGb: number, cluster: string, on: boolean, ipam: boolean) => ({
    vm: {
      vcenter: VCENTER, moref: `vm-${moref++}`, name, power_state: on ? "on" : "off", guest_os: os, cpu_count: cpu,
      memory_mb: memGb * 1024, cluster, host: `esx-gva-${cluster.slice(4, 7).toLowerCase()}-0${(moref % 4) + 1}.corp.example`,
      ...(ip ? { ip_address: ip, hostname: `${name}.corp.example` } : {}),
    } satisfies S["OnboardingVm"],
    ipam: ipam && Boolean(ip),
  }); // prettier-ignore
  d.unmanaged = [
    ...Array.from({ length: 12 }, (_, i) =>
      add(`erp-app${pad(i + 1)}`, `10.31.20.${11 + i}`, "Red Hat Enterprise Linux 8 (64-bit)", 4, 16, "GVA-PRD-CL01", true, true),
    ),
    ...Array.from({ length: 40 }, (_, i) =>
      add(`fs-gva${pad(i + 1, 3)}`, `10.31.30.${11 + i}`, "Microsoft Windows Server 2019 (64-bit)", 2, 8, "GVA-PRD-CL02", (i + 1) % 7 !== 0, i % 2 === 0),
    ),
    ...Array.from({ length: 8 }, (_, i) =>
      add(`app-old${pad(i + 1)}`, i % 3 ? `10.31.10.${201 + i}` : undefined, "CentOS 7 (64-bit)", 2, 4, "GVA-DEV-CL01", i % 2 === 0, false),
    ),
  ].sort((a, b) => a.vm.name.localeCompare(b.vm.name)); // prettier-ignore
  return d.unmanaged;
}

function problem(status: number, title: string, errors?: S["FieldError"][]) {
  return HttpResponse.json(
    { type: "https://rvav.dev/problems/x", title, status, request_id: "req_mock", ...(errors ? { errors } : {}) },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}
const err = (field: string, code: string, message: string): S["FieldError"] => ({ field, code, message });

export function onboardingHandlers(db: Db, base: string, wait: () => Promise<void>) {
  return [
    http.get(`${base}/admin/onboarding/vms`, async ({ request }) => {
      await wait();
      const u = new URL(request.url);
      const q = (u.searchParams.get("q") ?? "").toLowerCase();
      const vcenter = u.searchParams.get("vcenter");
      const power = u.searchParams.get("power_state");
      const match = unmanaged(db).filter(
        ({ vm }) =>
          (!q || vm.name.includes(q) || vm.ip_address?.startsWith(q) || vm.hostname?.includes(q)) &&
          (!vcenter || vm.vcenter === vcenter) &&
          (!power || vm.power_state === power),
      );
      const limit = Number(u.searchParams.get("limit") ?? 50);
      const from = Number(u.searchParams.get("cursor") ?? 0);
      const items = match.slice(from, from + limit).map((x) => x.vm);
      return HttpResponse.json({
        items,
        total: match.length,
        ...(from + limit < match.length ? { next_cursor: String(from + limit) } : {}),
      } satisfies S["OnboardingVmPage"]);
    }),

    http.post(`${base}/admin/onboarding`, async ({ request }) => {
      await wait();
      const b = (await request.json()) as S["OnboardingDraft"];
      const errors: S["FieldError"][] = [];
      const project = db.projects.find((p) => p.id === b.project_id);
      if (!project) errors.push(err("/project_id", "not_found", "No such project."));
      else {
        if (!project.environments.includes(b.environment))
          errors.push(err("/environment", "invalid", `${project.name} has no environment ${b.environment}.`));
        const role = memberRole(db, project.id, b.owner_id);
        if (!role || role === "viewer")
          errors.push(
            err(
              "/owner_id",
              "invalid",
              `Choose a member of ${project.name} (not a viewer): owners run the actions on their deployments.`,
            ),
          );
      }
      const item = db.items.find((i) => i.id === b.catalog_item_id || i.slug === b.catalog_item_id);
      if (!item || item.type !== "vm_blueprint")
        errors.push(
          err(
            "/catalog_item_id",
            "invalid",
            "Choose a published VM item: it gives the deployments their OS family and day-2 actions.",
          ),
        );
      else if (project && !item.projectIds.includes(project.id))
        errors.push(err("/catalog_item_id", "invalid", `${item.name} isn't offered in ${project.name}.`));
      // The lease policy's default when none is given, and its maximum.
      const policy = project ? leasePolicyOf(db, project.id, b.environment) : undefined;
      const days = b.lease_days ?? policy?.default_days ?? undefined;
      if (b.lease_days !== undefined && b.lease_days < 1)
        errors.push(err("/lease_days", "too_short", "A lease is at least one day."));
      else if (days && policy?.max_days && days > policy.max_days)
        errors.push(
          err("/lease_days", "too_long", `Leases in ${b.environment} can be at most ${policy.max_days} days.`),
        );
      if (b.vms.length === 0 || b.vms.length > 200) {
        errors.push(err("/vms", "out_of_range", "Adopt from 1 to 200 VMs at once."));
        return problem(422, "Some inputs need attention", errors);
      }
      if (
        b.deployment_name !== undefined &&
        b.deployment_name !== "" &&
        !/^[a-z0-9][a-z0-9._-]{0,62}$/.test(b.deployment_name)
      )
        errors.push(
          err(
            "/deployment_name",
            "pattern",
            "Lower-case letters, digits, dots and dashes, up to 63, for example web-prd-erp.",
          ),
        );
      const all = unmanaged(db);
      const picked = b.vms.map((r, i) => {
        const x = all.find(({ vm }) => vm.vcenter === r.vcenter && vm.moref === r.moref);
        if (!x)
          errors.push(
            err(`/vms/${i}`, "not_found", `${r.vcenter} has no VM ${r.moref} that rvav doesn't manage: a deployment has it, or the inventory hasn't read it yet.`),
          );
        return x;
      }); // prettier-ignore
      if (errors.length || !project || !item) return problem(422, "Some inputs need attention", errors);

      const owner = db.users.find((u) => u.id === b.owner_id)!;
      const expires = days ? new Date(Date.now() + days * 86_400_000).toISOString() : null;
      const groups = b.deployment_name ? [picked as Unmanaged[]] : (picked as Unmanaged[]).map((x) => [x]);
      const out: S["OnboardingResult"] = { deployments: [] };
      for (const group of groups) {
        const resources: DeploymentDetail["resources"] = [];
        let addresses = 0;
        let records = 0;
        for (const { vm, ipam } of group) {
          const fqdn = vm.hostname;
          const ip = vm.ip_address;
          resources.push({
            id: `res_ob${vm.moref}`, type: "vsphere.vm", name: vm.name, sync_state: "in_sync",
            vm: {
              moref: vm.moref, instance_uuid: `4219${vm.moref.slice(3)}-0000-4000-8000-000000000000`, vcenter: vm.vcenter,
              power_state: vm.power_state, guest_state: vm.power_state === "on" ? "running" : "not_running", guest_os: vm.guest_os,
              cpu_count: vm.cpu_count, memory_mb: vm.memory_mb, disks: [{ label: "Hard disk 1", capacity_gb: 80 }],
              nics: [{ network: "dvpg-app-prd-vl210", label: "Network adapter 1", ...(ip ? { ip_addresses: [ip] } : {}) }],
              cluster: vm.cluster, host: vm.host, ...(ipam && fqdn ? { fqdn } : {}), snapshot_count: 0,
            },
          } satisfies VmResource); // prettier-ignore
          if (ipam && ip && fqdn) {
            addresses += 1;
            records += 2;
            resources.push(
              { id: `res_obip${vm.moref}`, type: "ipam.address", name: ip, sync_state: "in_sync", address: ip, subnet: `${ip.split(".").slice(0, 3).join(".")}.0/24` } satisfies IpAddressResource,
              { id: `res_oba${vm.moref}`, type: "dns.record", name: `A ${fqdn}`, sync_state: "in_sync", fqdn, record_type: "A", value: ip, zone: "corp.example" } satisfies DnsRecordResource,
              { id: `res_obp${vm.moref}`, type: "dns.record", name: `PTR ${ip}`, sync_state: "in_sync", fqdn: ip.split(".").reverse().join(".") + ".in-addr.arpa", record_type: "PTR", value: fqdn, zone: "in-addr.arpa" } satisfies DnsRecordResource,
            ); // prettier-ignore
          }
        }
        const first = group[0]!.vm;
        const vms = resources.filter((r): r is VmResource => r.type === "vsphere.vm");
        const dep: DeploymentDetail = {
          id: `dep_ob${first.moref.slice(3)}`, name: b.deployment_name || first.name, status: "ready",
          project: { id: project.id, code: project.code, name: project.name }, environment: b.environment,
          catalog_item: { id: item.id, slug: item.slug, name: item.name }, version: item.version, owner,
          created_at: new Date().toISOString(),
          lease: { expires_at: expires, extensions_used: 0, extensions_max: expires ? (policy?.max_extensions ?? 0) : null, max_days: policy?.max_days ?? null, grace_days: policy?.grace_days ?? 7, expiry_action: policy?.expiry_action ?? "notify_only" },
          vm_count: vms.length, vms_powered_on: vms.filter((v) => v.vm.power_state === "on").length,
          primary_address: first.ip_address ? { ip: first.ip_address, fqdn: first.hostname ?? "" } : null,
          sync_state: "in_sync", inputs: {}, resources,
        }; // prettier-ignore
        db.deployments.unshift(dep);
        out.deployments.push({ id: dep.id, name: dep.name, vms: group.map((x) => x.vm.name), addresses, records });
      }
      const adopted = new Set(b.vms.map((r) => `${r.vcenter}/${r.moref}`));
      (db as Db & { unmanaged?: Unmanaged[] }).unmanaged = all.filter(
        ({ vm }) => !adopted.has(`${vm.vcenter}/${vm.moref}`),
      );
      return HttpResponse.json(out);
    }),
  ];
}
