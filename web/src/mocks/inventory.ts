// Fictional vCenter and EfficientIP inventory used by the mock value sources.
import type { EnvironmentKey } from "@/lib/api/types";

export const DOMAIN = "corp.example";
export const VCENTER = "vc01.corp.example";

export interface Cluster {
  name: string;
  env: EnvironmentKey;
  hosts: number;
  cpuFreePct: number;
  memFreePct: number;
}

export const clusters: Cluster[] = [
  { name: "GVA-DEV-CL01", env: "dev", hosts: 4, cpuFreePct: 22, memFreePct: 29 },
  { name: "GVA-TST-CL01", env: "tst", hosts: 4, cpuFreePct: 64, memFreePct: 52 },
  { name: "GVA-PRD-CL02", env: "prd", hosts: 6, cpuFreePct: 58, memFreePct: 47 },
  { name: "GVA-PRD-CL01", env: "prd", hosts: 8, cpuFreePct: 41, memFreePct: 33 },
];

export const datastores: Record<string, { name: string; freeTb: number; tier: string }[]> = {
  "GVA-DEV-CL01": [
    { name: "vsan-gva-dev-cl01", freeTb: 3.2, tier: "silver" },
    { name: "nfs-gva-bronze-01", freeTb: 9.8, tier: "bronze" },
  ],
  "GVA-TST-CL01": [{ name: "vsan-gva-tst-cl01", freeTb: 11.6, tier: "silver" }],
  "GVA-PRD-CL02": [{ name: "vsan-gva-prd-cl02", freeTb: 22.9, tier: "gold" }],
  "GVA-PRD-CL01": [
    { name: "vsan-gva-prd-cl01", freeTb: 18.4, tier: "gold" },
    { name: "nfs-gva-silver-02", freeTb: 6.1, tier: "silver" },
  ],
};

export interface Network {
  name: string;
  env: EnvironmentKey;
  subnet: string;
  gateway: string;
  free: number;
  nextHost: number;
}

export const networks: Network[] = [
  { name: "dvpg-app-dev-vl310", env: "dev", subnet: "10.31.10.0/24", gateway: "10.31.10.1", free: 63, nextHost: 57 },
  { name: "dvpg-app-tst-vl410", env: "tst", subnet: "10.41.10.0/24", gateway: "10.41.10.1", free: 118, nextHost: 24 },
  { name: "dvpg-web-prd-vl210", env: "prd", subnet: "10.21.10.0/24", gateway: "10.21.10.1", free: 87, nextHost: 88 },
  { name: "dvpg-db-prd-vl230", env: "prd", subnet: "10.22.30.0/25", gateway: "10.22.30.1", free: 31, nextHost: 40 },
];

export const dnsZones = ["corp.example", "web.corp.example", "data.corp.example", "10.in-addr.arpa"];

export function prefix(subnet: string) {
  return subnet.split(".").slice(0, 3).join(".");
}

export const sizes = {
  S: { label: "Small", vcpu: 2, memoryGb: 4, diskGb: 60, hint: "tools, agents" },
  M: { label: "Medium", vcpu: 4, memoryGb: 16, diskGb: 80, hint: "most apps" },
  L: { label: "Large", vcpu: 8, memoryGb: 32, diskGb: 120, hint: "JVM, search" },
  XL: { label: "X-Large", vcpu: 8, memoryGb: 48, diskGb: 120, hint: "needs approval in prd" },
} as const;

export type SizeKey = keyof typeof sizes;

export const templates = {
  rhel9: { name: "tpl-rhel9.4-2026.10", os: "Red Hat Enterprise Linux 9.4" },
  ubuntu: { name: "tpl-ubuntu24.04-2026.10", os: "Ubuntu 24.04 LTS" },
  windows: { name: "tpl-win2025-std-2026.09", os: "Windows Server 2025 Standard" },
} as const;
