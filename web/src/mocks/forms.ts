// Request forms as the backend would serve them: JSON Schema for the inputs
// plus a UI schema for layout, widgets, value sources and rules.
import type { Option, RequestForm, UiField, UiSection } from "@/lib/api/types";
import { sizes } from "./inventory";

type JsonSchema = Record<string, unknown>;

interface FormSpec {
  properties: Record<string, JsonSchema>;
  required: string[];
  sections: UiSection[];
  fields: Record<string, UiField>;
  initial: Record<string, unknown>;
}

function build(itemId: string, versionId: string, spec: FormSpec): RequestForm {
  return {
    item_id: itemId,
    version_id: versionId,
    schema: {
      $schema: "https://json-schema.org/draft/2020-12/schema",
      type: "object",
      properties: spec.properties,
      required: spec.required,
      additionalProperties: false,
    },
    ui_schema: { layout: { kind: "page", sections: spec.sections }, fields: spec.fields },
    initial_values: spec.initial,
  };
}

const sizeOptions: Option[] = Object.entries(sizes).map(([value, s]) => ({
  value,
  label: s.label,
  meta: `${s.vcpu} vCPU · ${s.memoryGb} GB · ${s.diskGb} GB`,
}));

interface PostConfig {
  value: string;
  label: string;
  required?: boolean;
}

function vmBlueprint(itemId: string, versionId: string, postConfig: PostConfig[], defaults: string[]): RequestForm {
  return build(itemId, versionId, {
    properties: {
      role: { type: "string", title: "Role", pattern: "^[a-z]{2,12}$", minLength: 2, maxLength: 12 },
      vm_name: { type: "string", readOnly: true },
      size: { type: "string", enum: Object.keys(sizes) },
      cluster: { type: "string" },
      datastore: { type: "string" },
      network: { type: "string" },
      ip_address: { type: "string", readOnly: true },
      post_config: { type: "array", items: { enum: postConfig.map((p) => p.value) }, uniqueItems: true },
    },
    required: ["role", "size", "cluster", "datastore", "network"],
    sections: [
      { key: "basics", title: "Basics", columns: 2, fields: ["role", "vm_name"] },
      { key: "size", title: "Size", columns: 1, fields: ["size", "size_note"] },
      {
        key: "placement",
        title: "Placement and network",
        columns: 2,
        fields: ["cluster", "datastore", "network", "ip_address"],
      },
      {
        key: "config",
        title: "Configuration after boot",
        description: "AWX job templates, run in this order once the VM has an IP address.",
        columns: 1,
        fields: ["post_config"],
      },
    ],
    fields: {
      role: {
        widget: "text",
        label: "Role",
        placeholder: "app",
        help: "Lower case, 2–12 letters. Used in the VM name.",
      },
      vm_name: {
        widget: "computed",
        label: "VM name",
        help: "From the naming policy {project}-{env}-{role}{seq:03}. Checked free in vCenter and DNS.",
        value_source: { kind: "builtin", name: "naming.preview", params: { role: "${inputs.role}" } },
        depends_on: ["role"],
      },
      size: { widget: "radio_cards", label: "Size", options: sizeOptions },
      size_note: {
        widget: "info",
        label: "Needs approval",
        help: "More than 32 GB RAM in production needs approval from the Infrastructure leads.",
        rules: { visible: 'environment == "prd" && inputs.size == "XL"' },
      },
      cluster: {
        widget: "vsphere_cluster",
        label: "Cluster",
        value_source: { kind: "builtin", name: "vsphere.clusters" },
      },
      datastore: {
        widget: "vsphere_datastore",
        label: "Datastore",
        help: "Only datastores your project may use in this cluster.",
        value_source: { kind: "builtin", name: "vsphere.datastores", params: { cluster: "${inputs.cluster}" } },
        depends_on: ["cluster"],
      },
      network: {
        widget: "vsphere_network",
        label: "Network",
        value_source: { kind: "builtin", name: "vsphere.networks" },
      },
      ip_address: {
        widget: "computed",
        label: "IP address",
        help: "Next free address. Reserved in EfficientIP when the request starts, with A and PTR records.",
        value_source: { kind: "builtin", name: "ipam.next_free_address", params: { network: "${inputs.network}" } },
        depends_on: ["network"],
      },
      post_config: {
        widget: "checklist",
        label: "AWX job templates",
        options: postConfig.map((p) => ({
          value: p.value,
          label: p.label,
          meta: p.value,
          ...(p.required ? { disabled: true, disabled_reason: "Required" } : {}),
        })),
      },
    },
    initial: { role: "app", size: "M", post_config: defaults },
  });
}

const linuxPost: PostConfig[] = [
  { value: "linux-baseline", label: "Corporate baseline: chrony, sshd hardening, Satellite", required: true },
  { value: "idm-join", label: "Join IdM and apply the project's HBAC rules" },
  { value: "monitoring-agent", label: "Monitoring agent: node_exporter on port 9100" },
  { value: "role-nginx", label: "nginx 1.27 with the corporate TLS defaults" },
];

const windowsPost: PostConfig[] = [
  { value: "windows-baseline", label: "Corporate baseline: CIS level 1 GPOs, WSUS", required: true },
  { value: "ad-join", label: "Join corp.example and move to the project OU" },
  { value: "monitoring-agent", label: "Monitoring agent: windows_exporter" },
];

export function formFor(slug: string, itemId: string, versionId: string): RequestForm {
  switch (slug) {
    case "rhel9-server":
    case "ubuntu-server":
      return vmBlueprint(itemId, versionId, linuxPost, ["linux-baseline", "idm-join", "monitoring-agent"]);
    case "windows-server":
      return vmBlueprint(itemId, versionId, windowsPost, ["windows-baseline", "ad-join"]);
    case "postgres-cluster":
      return build(itemId, versionId, {
        properties: {
          role: { type: "string", pattern: "^[a-z]{2,12}$" },
          vm_name: { type: "string", readOnly: true },
          size: { type: "string", enum: Object.keys(sizes) },
          pg_version: { type: "string", enum: ["16.6", "15.10"] },
          network: { type: "string" },
          backup_retention_days: { type: "integer", minimum: 7, maximum: 90 },
        },
        required: ["role", "size", "pg_version", "network", "backup_retention_days"],
        sections: [
          {
            key: "basics",
            title: "Cluster",
            columns: 2,
            fields: ["role", "vm_name", "pg_version", "backup_retention_days"],
          },
          { key: "size", title: "Node size", columns: 1, fields: ["size"] },
          { key: "net", title: "Network", columns: 2, fields: ["network"] },
        ],
        fields: {
          role: { widget: "text", label: "Database name", help: "Lower case letters. Used in the node names." },
          vm_name: {
            widget: "computed",
            label: "First node",
            value_source: { kind: "builtin", name: "naming.preview", params: { role: "pg${inputs.role}" } },
            depends_on: ["role"],
          },
          pg_version: {
            widget: "select",
            label: "PostgreSQL version",
            options: [
              { value: "16.6", label: "16.6", meta: "recommended" },
              { value: "15.10", label: "15.10" },
            ],
          },
          backup_retention_days: { widget: "number", label: "Backup retention (days)", help: "pgBackRest to S3." },
          size: { widget: "radio_cards", label: "Node size", options: sizeOptions },
          network: {
            widget: "vsphere_network",
            label: "Network",
            value_source: { kind: "builtin", name: "vsphere.networks" },
          },
        },
        initial: { role: "", size: "L", pg_version: "16.6", backup_retention_days: 35 },
      });
    case "db-user":
      return build(itemId, versionId, {
        properties: {
          database: { type: "string" },
          role_name: { type: "string", pattern: "^[a-z][a-z0-9_]{2,30}$" },
          privileges: { type: "array", items: { enum: ["SELECT", "INSERT", "UPDATE", "DELETE"] }, minItems: 1 },
        },
        required: ["database", "role_name", "privileges"],
        sections: [
          { key: "user", title: "Database user", columns: 2, fields: ["database", "role_name", "privileges"] },
        ],
        fields: {
          database: {
            widget: "select",
            label: "Database",
            options: [
              { value: "ledger", label: "ledger", meta: "data-prd-pg-ledger" },
              { value: "checkout", label: "checkout", meta: "web-prd-pg-checkout" },
            ],
          },
          role_name: {
            widget: "text",
            label: "Role name",
            placeholder: "app_reporting",
            help: "The password is generated and stored in Vault under the project's path.",
          },
          privileges: {
            widget: "checklist",
            label: "Privileges",
            options: ["SELECT", "INSERT", "UPDATE", "DELETE"].map((v) => ({ value: v, label: v })),
          },
        },
        initial: { privileges: ["SELECT"] },
      });
    case "dns-record":
      return build(itemId, versionId, {
        properties: {
          zone: { type: "string" },
          name: { type: "string", pattern: "^[a-z0-9-]{1,63}$" },
          record_type: { type: "string", enum: ["A", "CNAME", "TXT"] },
          value: { type: "string", minLength: 1 },
          ttl: { type: "integer", minimum: 60, maximum: 86400 },
        },
        required: ["zone", "name", "record_type", "value"],
        sections: [
          { key: "record", title: "Record", columns: 2, fields: ["zone", "name", "record_type", "value", "ttl"] },
        ],
        fields: {
          zone: { widget: "dns_zone", label: "Zone", value_source: { kind: "builtin", name: "dns.zones" } },
          name: { widget: "text", label: "Name", placeholder: "status" },
          record_type: {
            widget: "segmented",
            label: "Type",
            options: ["A", "CNAME", "TXT"].map((v) => ({ value: v, label: v })),
          },
          value: { widget: "text", label: "Value", placeholder: "10.21.10.37 or a host name" },
          ttl: { widget: "number", label: "TTL (seconds)" },
        },
        initial: { record_type: "A", ttl: 3600 },
      });
    case "firewall-flow":
      return build(itemId, versionId, {
        properties: {
          source: { type: "string", minLength: 1 },
          destination: { type: "string", minLength: 1 },
          protocol: { type: "string", enum: ["TCP", "UDP"] },
          port: { type: "integer", minimum: 1, maximum: 65535 },
        },
        required: ["source", "destination", "protocol", "port"],
        sections: [{ key: "flow", title: "Flow", columns: 2, fields: ["source", "destination", "protocol", "port"] }],
        fields: {
          source: { widget: "text", label: "Source", placeholder: "web-prd-front" },
          destination: { widget: "text", label: "Destination", placeholder: "data-prd-pg-ledger" },
          protocol: {
            widget: "segmented",
            label: "Protocol",
            options: [
              { value: "TCP", label: "TCP" },
              { value: "UDP", label: "UDP" },
            ],
          },
          port: { widget: "number", label: "Port" },
        },
        initial: { protocol: "TCP" },
      });
    default:
      return build(itemId, versionId, {
        properties: {
          target: { type: "string", minLength: 1 },
          notes: { type: "string", maxLength: 500 },
        },
        required: ["target"],
        sections: [{ key: "main", title: "Details", columns: 1, fields: ["target", "notes"] }],
        fields: {
          target: { widget: "text", label: "Target", help: "Host, group or object this job applies to." },
          notes: { widget: "textarea", label: "Notes for the job" },
        },
        initial: {},
      });
  }
}
