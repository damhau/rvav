package seed

type item struct {
	slug, name, summary, description, category, typ, icon string
	tags                                                  []string
	featured                                              bool
	awxTemplate                                           string
	version                                               string
	schema, ui, initial, blueprint                        map[string]any
	projects                                              []string
}

type option struct{ value, label, meta string }

func options(list ...option) []map[string]any {
	out := make([]map[string]any, len(list))
	for i, o := range list {
		out[i] = map[string]any{"value": o.value, "label": o.label}
		if o.meta != "" {
			out[i]["meta"] = o.meta
		}
	}
	return out
}

// describedOptions are checklist options: the text after the label is a
// description, and the value (the AWX template) shows as meta.
func describedOptions(list ...option) []map[string]any {
	out := make([]map[string]any, len(list))
	for i, o := range list {
		out[i] = map[string]any{"value": o.value, "label": o.label, "meta": o.value}
		if o.meta != "" {
			out[i]["description"] = o.meta
		}
	}
	return out
}

func enumOf(list []option) []string {
	out := make([]string, len(list))
	for i, o := range list {
		out[i] = o.value
	}
	return out
}

var sizes = map[string]any{
	"S":  map[string]any{"label": "Small", "vcpu": 2, "memory_gb": 4, "disk_gb": 60},
	"M":  map[string]any{"label": "Medium", "vcpu": 4, "memory_gb": 16, "disk_gb": 80},
	"L":  map[string]any{"label": "Large", "vcpu": 8, "memory_gb": 32, "disk_gb": 120},
	"XL": map[string]any{"label": "X-Large", "vcpu": 8, "memory_gb": 48, "disk_gb": 120},
}

// vmItem builds a VM blueprint whose form matches the request mockup:
// role and name, size, placement and network, then AWX post-configuration.
func vmItem(template, osFamily, required string, post []option, defaultPost []string) (schema, ui, initial, blueprint map[string]any) {
	schema = map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"role", "size", "cluster", "datastore", "network", "post_config"},
		"properties": map[string]any{
			"role":       map[string]any{"type": "string", "pattern": "^[a-z]{2,12}$"},
			"vm_name":    map[string]any{"type": "string", "readOnly": true},
			"size":       map[string]any{"type": "string", "enum": []string{"S", "M", "L", "XL"}},
			"cluster":    map[string]any{"type": "string", "minLength": 1},
			"datastore":  map[string]any{"type": "string", "minLength": 1},
			"network":    map[string]any{"type": "string", "minLength": 1},
			"ip_address": map[string]any{"type": "string", "readOnly": true},
			"post_config": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string", "enum": enumOf(post)},
				"uniqueItems": true,
				"contains":    map[string]any{"const": required},
			},
		},
	}
	ui = map[string]any{
		"layout": map[string]any{
			"kind": "page",
			"sections": []map[string]any{
				{"key": "basics", "title": "Basics", "columns": 2, "fields": []string{"role", "vm_name"}},
				{"key": "size", "title": "Size", "columns": 1, "fields": []string{"size"}},
				{"key": "placement", "title": "Placement and network", "columns": 2, "fields": []string{"cluster", "datastore", "network", "ip_address"}},
				{"key": "post_config", "title": "Configuration after boot", "description": "AWX job templates, run in this order", "columns": 1, "fields": []string{"post_config"}},
			},
		},
		"fields": map[string]any{
			"role": map[string]any{"widget": "text", "label": "Role", "placeholder": "app", "expose_to_inventory": true,
				"help": "Lower case, 2–12 letters. Used in the VM name."},
			"vm_name": map[string]any{"widget": "computed", "label": "VM name",
				"help":         "From the naming policy {project}-{environment}-{role}{seq:03}. Checked free in vCenter and DNS.",
				"value_source": map[string]any{"kind": "builtin", "name": "naming.preview"},
				"depends_on":   []string{"$environment", "role", "network"}},
			"size": map[string]any{"widget": "radio_cards", "label": "Size", "approver_editable": true, "options": options(
				option{"S", "Small", "2 vCPU · 4 GB · 60 GB"},
				option{"M", "Medium", "4 vCPU · 16 GB · 80 GB"},
				option{"L", "Large", "8 vCPU · 32 GB · 120 GB"},
				option{"XL", "X-Large", "8 vCPU · 48 GB · 120 GB"},
			)},
			"cluster": map[string]any{"widget": "vsphere_cluster", "label": "Cluster",
				"value_source": map[string]any{"kind": "builtin", "name": "vsphere.clusters"},
				"depends_on":   []string{"$environment"}},
			"datastore": map[string]any{"widget": "vsphere_datastore", "label": "Datastore",
				"help":         "Only datastores your project may use in this cluster.",
				"value_source": map[string]any{"kind": "builtin", "name": "vsphere.datastores", "params": map[string]any{"cluster": "${inputs.cluster}"}},
				"depends_on":   []string{"$environment", "cluster"}},
			"network": map[string]any{"widget": "vsphere_network", "label": "Network",
				"value_source": map[string]any{"kind": "builtin", "name": "vsphere.networks", "params": map[string]any{"cluster": "${inputs.cluster}"}},
				"depends_on":   []string{"$environment", "cluster"}},
			"ip_address": map[string]any{"widget": "computed", "label": "IP address",
				"help":         "Reserved in EfficientIP when the request starts, with A and PTR records.",
				"value_source": map[string]any{"kind": "builtin", "name": "ipam.next_free_address", "params": map[string]any{"network": "${inputs.network}"}},
				"depends_on":   []string{"network"}},
			"post_config": map[string]any{"widget": "checklist", "label": "Configuration after boot", "options": describedOptions(post...)},
		},
	}
	initial = map[string]any{"role": "app", "size": "M", "post_config": defaultPost}
	awxTemplates := map[string]any{}
	for _, o := range post {
		awxTemplates[o.value] = map[string]any{"title": o.label, "job_template": o.value}
	}
	blueprint = map[string]any{
		"kind":     "vm",
		"naming":   "{project}-{environment}-{inputs.role}{seq:03}",
		"template": template,
		"guest":    map[string]any{"os_family": osFamily, "domain": "corp.example", "time_zone": "Europe/Zurich"},
		"sizes":    sizes,
		"bindings": map[string]any{
			"vcpu": "sizes[inputs.size].vcpu", "memory_gb": "sizes[inputs.size].memory_gb", "disk_gb": "sizes[inputs.size].disk_gb",
			"cluster": "inputs.cluster", "datastore": "inputs.datastore", "network": "inputs.network",
			"post_config": "inputs.post_config",
		},
		"awx_templates": awxTemplates,
	}
	return schema, ui, initial, blueprint
}

// awxItem builds a form for an AWX job template; inputs become extra_vars.
func awxItem(template string, workflow bool, title string, props map[string]any, required []string,
	fields map[string]any, order []string) (schema, ui, initial, blueprint map[string]any) {
	schema = map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object",
		"additionalProperties": false, "required": required, "properties": props,
	}
	ui = map[string]any{
		"layout": map[string]any{"kind": "page", "sections": []map[string]any{{"key": "inputs", "title": title, "columns": 2, "fields": order}}},
		"fields": fields,
	}
	return schema, ui, map[string]any{}, map[string]any{
		"kind": "awx",
		"awx":  map[string]any{"title": title, "job_template": template, "workflow": workflow},
	}
}

var linuxPost = []option{
	{"linux-baseline", "Corporate baseline", "chrony, sshd hardening, journald, Satellite registration"},
	{"idm-join", "Join IdM", "Enrols the host in idm.corp.example and applies HBAC rules"},
	{"monitoring-agent", "Monitoring agent", "node_exporter on port 9100, registered in Prometheus"},
	{"role-nginx", "nginx", "nginx 1.27 with the corporate TLS defaults"},
}

func catalogItems() []item {
	all := []string{"web", "data", "fin", "core"}
	var items []item

	add := func(it item, schema, ui, initial, bp map[string]any) {
		it.schema, it.ui, it.initial, it.blueprint = schema, ui, initial, bp
		items = append(items, it)
	}

	s, u, in, bp := vmItem("tpl-rhel9.4-2026.10", "linux", "linux-baseline", linuxPost, []string{"linux-baseline", "idm-join", "monitoring-agent"})
	add(item{slug: "rhel9-server", name: "RHEL 9 server", category: "compute", typ: "vm_blueprint", icon: "server",
		summary:     "Red Hat Enterprise Linux 9.4 VM with the corporate baseline, IdM join and node_exporter.",
		description: "Clones `tpl-rhel9.4-2026.10`, reserves an address in EfficientIP, creates DNS records and runs the AWX jobs you pick.",
		tags:        []string{"linux", "rhel"}, featured: true, version: "3.2", projects: all}, s, u, in, bp)

	s, u, in, bp = vmItem("tpl-ubuntu24.04-2026.10", "linux", "linux-baseline", linuxPost, []string{"linux-baseline", "monitoring-agent"})
	add(item{slug: "ubuntu-server", name: "Ubuntu 24.04 server", category: "compute", typ: "vm_blueprint", icon: "server",
		summary: "Ubuntu LTS with cloud-init, the corporate baseline and the monitoring agent.",
		tags:    []string{"linux", "ubuntu"}, featured: true, version: "2.4", projects: all}, s, u, in, bp)

	s, u, in, bp = vmItem("tpl-win2025-std-2026.09", "windows", "win-baseline", []option{
		{"win-baseline", "Corporate baseline", "CIS level 1 GPO, WSUS, domain join"},
		{"monitoring-agent", "Monitoring agent", "windows_exporter, registered in Prometheus"},
	}, []string{"win-baseline", "monitoring-agent"})
	add(item{slug: "windows-server", name: "Windows Server 2025", category: "compute", typ: "vm_blueprint", icon: "monitor",
		summary: "Domain-joined Windows Server 2025 Standard with the CIS level 1 GPO set.",
		tags:    []string{"windows"}, version: "1.8", projects: all}, s, u, in, bp)

	add(item{slug: "postgres-cluster", name: "PostgreSQL 16 cluster", category: "databases", typ: "composite", icon: "database",
		summary: "Three-node Patroni cluster with etcd, an HAProxy VIP and pgBackRest backups.",
		tags:    []string{"postgres"}, version: "1.3", projects: []string{"data", "fin", "core"}},
		map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object",
			"required": []string{"name"}, "properties": map[string]any{"name": map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]{2,20}$"}}},
		map[string]any{"layout": map[string]any{"kind": "page", "sections": []map[string]any{{"key": "basics", "title": "Cluster", "fields": []string{"name"}}}},
			"fields": map[string]any{"name": map[string]any{"widget": "text", "label": "Cluster name"}}},
		map[string]any{}, map[string]any{"kind": "composite"})

	s, u, in, bp = awxItem("pg-create-role", false, "Create database user",
		map[string]any{
			"database":   map[string]any{"type": "string", "minLength": 1},
			"role_name":  map[string]any{"type": "string", "pattern": "^[a-z_][a-z0-9_]{2,40}$"},
			"privileges": map[string]any{"type": "string", "enum": []string{"read", "write", "owner"}},
		}, []string{"database", "role_name", "privileges"},
		map[string]any{
			"database":   map[string]any{"widget": "text", "label": "Database", "placeholder": "ledger"},
			"role_name":  map[string]any{"widget": "text", "label": "Role name", "placeholder": "app_reporting"},
			"privileges": map[string]any{"widget": "segmented", "label": "Privileges", "options": options(option{"read", "Read", ""}, option{"write", "Read and write", ""}, option{"owner", "Owner", ""})},
		}, []string{"database", "role_name", "privileges"})
	add(item{slug: "db-user", name: "Create database user", category: "databases", typ: "awx_job_template", icon: "key-round",
		summary: "Creates a PostgreSQL role with a generated password stored in Vault.", awxTemplate: "pg-create-role",
		version: "AWX #47", projects: all}, s, u, in, bp)

	s, u, in, bp = awxItem("fw-flow-request", true, "Open firewall flow",
		map[string]any{
			"source":      map[string]any{"type": "string", "minLength": 1},
			"destination": map[string]any{"type": "string", "minLength": 1},
			"port":        map[string]any{"type": "integer", "minimum": 1, "maximum": 65535},
			"protocol":    map[string]any{"type": "string", "enum": []string{"tcp", "udp"}},
		}, []string{"source", "destination", "port", "protocol"},
		map[string]any{
			"source":      map[string]any{"widget": "text", "label": "Source", "help": "Host, deployment or CIDR"},
			"destination": map[string]any{"widget": "text", "label": "Destination"},
			"port":        map[string]any{"widget": "number", "label": "Port"},
			"protocol":    map[string]any{"widget": "segmented", "label": "Protocol", "options": options(option{"tcp", "TCP", ""}, option{"udp", "UDP", ""})},
		}, []string{"source", "destination", "port", "protocol"})
	add(item{slug: "firewall-flow", name: "Open firewall flow", category: "network", typ: "awx_workflow", icon: "shield",
		summary: "Requests a flow on the perimeter and internal firewalls and opens the change ticket.", awxTemplate: "fw-flow-request",
		version: "AWX #12", projects: all}, s, u, in, bp)

	s, u, in, bp = awxItem("ddi-dns-record", false, "DNS record",
		map[string]any{
			"name":  map[string]any{"type": "string", "pattern": "^[a-z0-9-]{1,63}$"},
			"type":  map[string]any{"type": "string", "enum": []string{"A", "CNAME", "TXT"}},
			"value": map[string]any{"type": "string", "minLength": 1},
			"zone":  map[string]any{"type": "string", "minLength": 1},
		}, []string{"name", "type", "value", "zone"},
		map[string]any{
			"name":  map[string]any{"widget": "text", "label": "Name"},
			"type":  map[string]any{"widget": "segmented", "label": "Type", "options": options(option{"A", "A", ""}, option{"CNAME", "CNAME", ""}, option{"TXT", "TXT", ""})},
			"value": map[string]any{"widget": "text", "label": "Value"},
			"zone":  map[string]any{"widget": "dns_zone", "label": "Zone", "value_source": map[string]any{"kind": "builtin", "name": "dns.zones"}},
		}, []string{"name", "type", "value", "zone"})
	add(item{slug: "dns-record", name: "DNS record", category: "network", typ: "awx_job_template", icon: "globe",
		summary: "Adds an A, CNAME or TXT record in an EfficientIP zone your project owns.", awxTemplate: "ddi-dns-record",
		version: "AWX #31", projects: all}, s, u, in, bp)

	s, u, in, bp = awxItem("pki-renew-deploy", false, "Renew TLS certificate",
		map[string]any{
			"common_name": map[string]any{"type": "string", "minLength": 3},
			"hosts":       map[string]any{"type": "string", "minLength": 1},
		}, []string{"common_name", "hosts"},
		map[string]any{
			"common_name": map[string]any{"widget": "text", "label": "Common name", "placeholder": "checkout.corp.example"},
			"hosts":       map[string]any{"widget": "text", "label": "Deploy to hosts", "help": "Comma-separated host names"},
		}, []string{"common_name", "hosts"})
	add(item{slug: "tls-cert", name: "Renew TLS certificate", category: "security", typ: "awx_job_template", icon: "lock",
		summary: "Issues a certificate from the internal CA and deploys it to the hosts you pick.", awxTemplate: "pki-renew-deploy",
		version: "AWX #22", projects: all}, s, u, in, bp)

	s, u, in, bp = awxItem("idm-hbac-group", false, "IdM access group",
		map[string]any{
			"group_name": map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]{2,40}$"},
			"hosts":      map[string]any{"type": "string", "minLength": 1},
			"sudo":       map[string]any{"type": "boolean"},
		}, []string{"group_name", "hosts"},
		map[string]any{
			"group_name": map[string]any{"widget": "text", "label": "Group name"},
			"hosts":      map[string]any{"widget": "text", "label": "Hosts", "help": "Comma-separated host names"},
			"sudo":       map[string]any{"widget": "toggle", "label": "Allow sudo"},
		}, []string{"group_name", "hosts", "sudo"})
	add(item{slug: "idm-group", name: "IdM access group", category: "identity", typ: "awx_job_template", icon: "users",
		summary: "Creates an IdM group with sudo and HBAC rules for a set of hosts.", awxTemplate: "idm-hbac-group",
		version: "AWX #9", projects: all}, s, u, in, bp)

	return items
}

// untouched is true while no catalog admin has changed the item (every
// change of theirs is audited): the seed keeps its items current until then,
// and leaves them alone after.
func untouched(item string) string {
	return `not exists (select 1 from audit_events a where a.target_type = 'catalog_item' and a.target_id = ` + item +
		` and a.action like 'catalog.%')`
}

func seedCatalog(exec func(string, ...any) error) error {
	for i, c := range []struct{ key, name string }{
		{"compute", "Compute"}, {"databases", "Databases"}, {"network", "Network"}, {"security", "Security"}, {"identity", "Identity"},
	} {
		if err := exec(`insert into catalog_categories (key, name, sort_order) values ($1,$2,$3)
			on conflict (key) do update set name = excluded.name, sort_order = excluded.sort_order`, c.key, c.name, i); err != nil {
			return err
		}
	}
	for _, it := range catalogItems() {
		itemID, versionID := "itm_"+it.slug, "ver_"+it.slug+"_1"
		source := "native"
		var awxName *string
		if it.awxTemplate != "" {
			source, awxName = "awx", &it.awxTemplate
		}
		if err := exec(`insert into catalog_items (id, slug, name, summary, description_md, category, type, icon, tags,
			featured, source_kind, awx_template_name, published_version_id)
			values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			on conflict (id) do update set name = excluded.name, summary = excluded.summary,
			description_md = excluded.description_md, category = excluded.category, type = excluded.type,
			icon = excluded.icon, tags = excluded.tags, featured = excluded.featured, source_kind = excluded.source_kind,
			awx_template_name = excluded.awx_template_name, published_version_id = excluded.published_version_id
			where not exists (select 1 from catalog_item_versions v where v.item_id = excluded.id and v.id <> $13)
			  and `+untouched("excluded.id"),
			itemID, it.slug, it.name, it.summary, it.description, it.category, it.typ, it.icon, nonNil(it.tags),
			it.featured, source, awxName, versionID); err != nil {
			return err
		}
		if err := exec(`insert into catalog_item_versions (id, item_id, number, state, form_schema, ui_schema,
			initial_values, blueprint, published_at)
			values ($1,$2,$3,'published',$4,$5,$6,$7, now())
			on conflict (id) do update set number = excluded.number, form_schema = excluded.form_schema,
			ui_schema = excluded.ui_schema, initial_values = excluded.initial_values, blueprint = excluded.blueprint
			where not exists (select 1 from catalog_item_versions v where v.item_id = excluded.item_id and v.id <> excluded.id)
			  and `+untouched("excluded.item_id"),
			versionID, itemID, it.version, j(it.schema), j(it.ui), j(it.initial), j(it.blueprint)); err != nil {
			return err
		}
		for _, p := range it.projects {
			if err := exec(`insert into catalog_entitlements (item_id, project_id) select $1::text, $2::text
				where `+untouched("$1::text")+` on conflict do nothing`, itemID, ProjectID(p)); err != nil {
				return err
			}
		}
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
