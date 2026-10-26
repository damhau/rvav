export type Env = "dev" | "tst" | "prd";

export interface User {
  id: string;
  name: string;
  email: string;
  title: string;
}

export interface Project {
  id: string;
  code: string;
  name: string;
  quota: { vcpu: number; ramGB: number; storageTB: number; vms: number };
  used: { vcpu: number; ramGB: number; storageTB: number; vms: number };
}

export type ItemType = "vm_blueprint" | "awx_job_template" | "awx_workflow" | "composite";

export interface CatalogItem {
  slug: string;
  name: string;
  summary: string;
  category: "Compute" | "Databases" | "Network" | "Security" | "Identity";
  type: ItemType;
  icon: string;
  version: string;
  typicalMinutes: number;
  approval: "never" | "prd" | "always";
  source?: string;
  favourite?: boolean;
  requests30d: number;
}

export type PowerState = "on" | "off" | "suspended";

export interface VM {
  moref: string;
  name: string;
  fqdn: string;
  ip: string;
  power: PowerState;
  os: string;
  vcpu: number;
  ramGB: number;
  diskGB: number;
  cluster: string;
  datastore: string;
  network: string;
  template: string;
  tools: "running" | "notRunning";
  cpuPct: number;
  memPct: number;
  snapshots: { name: string; created: string; sizeGB: number }[];
}

export type DeploymentStatus = "ready" | "provisioning" | "failed" | "updating" | "deleting";

export interface Deployment {
  id: string;
  name: string;
  item: string; // catalog slug
  itemVersion: string;
  project: string; // project id
  env: Env;
  owner: string; // user id
  status: DeploymentStatus;
  created: string;
  leaseExpires: string | null;
  vms: VM[];
  inputs: Record<string, string | number | boolean>;
}

export type StepStatus = "done" | "running" | "pending" | "failed" | "skipped";

export interface TimelineStep {
  key: string;
  title: string;
  detail?: string;
  system?: "rvav" | "vCenter" | "EfficientIP" | "AWX" | "Policy";
  status: StepStatus;
  started?: string;
  finished?: string;
  children?: TimelineStep[];
}

export type RequestStatus =
  | "pending_approval"
  | "running"
  | "succeeded"
  | "failed"
  | "rejected"
  | "cancelled";

export interface ServiceRequest {
  id: string;
  item: string;
  itemVersion: string;
  title: string;
  project: string;
  env: Env;
  requester: string;
  submitted: string;
  status: RequestStatus;
  deployment?: string;
  steps: TimelineStep[];
  awxJob?: { id: number; template: string; log: string[] };
}

export interface Approval {
  id: string;
  request: string;
  reason: string;
  rule: string;
  level: string;
  due: string;
  summary: { label: string; value: string }[];
  editable?: { key: string; label: string; value: number; unit: string; max: number }[];
}

export interface AuditEntry {
  at: string;
  actor: string;
  action: string;
  target: string;
}
