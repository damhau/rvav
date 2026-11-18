package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Templates and size presets offered to projects and environments, and the
// offers of those and of naming policies (ADR-0020).

// Offer is where something is offered; nil means all projects or all
// environments.
type Offer struct {
	ProjectID   *string `db:"project_id" json:"project_id"`
	Environment *string `db:"environment" json:"environment"`
}

// Offers lists what each key of a kind is offered to.
func (s *Store) Offers(ctx context.Context, kind string) (map[string][]Offer, error) {
	rows, _ := s.Pool.Query(ctx, `select key, project_id, environment from offers where kind = $1
		order by key, project_id nulls first, environment nulls first`, kind)
	type row struct {
		Key string `db:"key"`
		Offer
	}
	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[row])
	if err != nil {
		return nil, err
	}
	out := map[string][]Offer{}
	for _, r := range list {
		out[r.Key] = append(out[r.Key], r.Offer)
	}
	return out, nil
}

// OfferedKeys lists the keys of a kind offered to a project and environment.
func (s *Store) OfferedKeys(ctx context.Context, kind, projectID, environment string) ([]string, error) {
	rows, _ := s.Pool.Query(ctx, `select distinct key from offers where kind = $1
		and (project_id is null or project_id = $2) and (environment is null or environment = $3) order by key`,
		kind, projectID, environment)
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func setOffers(ctx context.Context, tx pgx.Tx, kind, key string, offers []Offer) error {
	if _, err := tx.Exec(ctx, `delete from offers where kind = $1 and key = $2`, kind, key); err != nil {
		return err
	}
	for _, o := range offers {
		if _, err := tx.Exec(ctx, `insert into offers (kind, key, project_id, environment) values ($1, $2, $3, $4)
			on conflict do nothing`, kind, key, o.ProjectID, o.Environment); err != nil {
			return err
		}
	}
	return nil
}

type VMTemplate struct {
	Key           string    `db:"key"`
	Label         string    `db:"label"`
	OSFamily      string    `db:"os_family"`
	Customization string    `db:"customization"`
	Description   string    `db:"description"`
	CreatedAt     time.Time `db:"created_at"`
	// Sources are where it is, one per vCenter (ADR-0037).
	Sources []TemplateSource `db:"sources"`
	// UserData is cloud-init user data of its own, and Constraints tags
	// its VMs get, both on top of the blueprint's (ADR-0037).
	UserData    string              `db:"user_data"`
	Constraints TemplateConstraints `db:"constraints"`
	// LinkedClone: its VMs are linked clones, unless the blueprint says.
	LinkedClone bool `db:"linked_clone"`
	// CloudbaseInit: a Windows template whose VMs cloudbase-init configures
	// from guestinfo (ADR-0039).
	CloudbaseInit bool `db:"cloudbase_init"`
}

// TemplateConstraints are a template's constraint tags by kind (ADR-0035).
type TemplateConstraints struct {
	Compute []string `json:"compute,omitempty"`
	Storage []string `json:"storage,omitempty"`
	Network []string `json:"network,omitempty"`
}

// TemplateSource is a catalogue template in one vCenter: "" for the
// vCenter integration's.
type TemplateSource struct {
	VCenter  string `json:"vcenter"`
	Template string `json:"template"`
	// Library, when set, is the content library Template is an item of.
	Library string `json:"content_library"`
}

const templateCols = `key, label, os_family, customization, description, created_at, user_data, constraints, linked_clone, cloudbase_init,
	coalesce((select json_agg(json_build_object('vcenter', s.vcenter, 'template', s.template, 'content_library', s.content_library)
		order by s.vcenter) from vm_template_sources s where s.template_key = vm_templates.key), '[]') as sources`

func (s *Store) VMTemplates(ctx context.Context) ([]VMTemplate, error) {
	rows, _ := s.Pool.Query(ctx, `select `+templateCols+` from vm_templates order by label, key`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[VMTemplate])
}

func (s *Store) GetVMTemplate(ctx context.Context, key string) (VMTemplate, error) {
	rows, _ := s.Pool.Query(ctx, `select `+templateCols+` from vm_templates where key = $1`, key)
	t, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[VMTemplate])
	return t, notFound(err)
}

// SaveVMTemplate creates a template (create: ErrInUse when the key exists)
// or replaces one, with its sources and offers.
func (s *Store) SaveVMTemplate(ctx context.Context, t VMTemplate, offers []Offer, create bool) error {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if create {
			if _, err := tx.Exec(ctx, `insert into vm_templates (key, label, os_family, customization, description, user_data, constraints,
				linked_clone, cloudbase_init) values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, t.Key, t.Label, t.OSFamily, t.Customization,
				t.Description, t.UserData, t.Constraints, t.LinkedClone, t.CloudbaseInit); err != nil {
				return err
			}
		} else {
			tag, err := tx.Exec(ctx, `update vm_templates set label = $2, os_family = $3, customization = $4, description = $5,
				user_data = $6, constraints = $7, linked_clone = $8, cloudbase_init = $9 where key = $1`, t.Key, t.Label, t.OSFamily,
				t.Customization, t.Description, t.UserData, t.Constraints, t.LinkedClone, t.CloudbaseInit)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		if _, err := tx.Exec(ctx, `delete from vm_template_sources where template_key = $1`, t.Key); err != nil {
			return err
		}
		for _, src := range t.Sources {
			if _, err := tx.Exec(ctx, `insert into vm_template_sources (template_key, vcenter, template, content_library)
				values ($1, $2, $3, $4)`, t.Key, src.VCenter, src.Template, src.Library); err != nil {
				return err
			}
		}
		return setOffers(ctx, tx, "template", t.Key, offers)
	})
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	return err
}

func (s *Store) DeleteVMTemplate(ctx context.Context, key string) error {
	return deleteOffered(ctx, s, "template", `delete from vm_templates where key = $1`, key)
}

type SizePreset struct {
	Key       string    `db:"key"`
	Label     string    `db:"label"`
	VCPU      int       `db:"vcpu"`
	MemoryGB  int       `db:"memory_gb"`
	DiskGB    int       `db:"disk_gb"`
	CreatedAt time.Time `db:"created_at"`
}

// SizePresets lists presets, smallest first.
func (s *Store) SizePresets(ctx context.Context) ([]SizePreset, error) {
	rows, _ := s.Pool.Query(ctx, `select key, label, vcpu, memory_gb, disk_gb, created_at from size_presets
		order by vcpu, memory_gb, disk_gb, key`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[SizePreset])
}

func (s *Store) SaveSizePreset(ctx context.Context, z SizePreset, offers []Offer, create bool) error {
	err := s.Tx(ctx, func(tx pgx.Tx) error {
		if create {
			if _, err := tx.Exec(ctx, `insert into size_presets (key, label, vcpu, memory_gb, disk_gb) values ($1, $2, $3, $4, $5)`,
				z.Key, z.Label, z.VCPU, z.MemoryGB, z.DiskGB); err != nil {
				return err
			}
		} else {
			tag, err := tx.Exec(ctx, `update size_presets set label = $2, vcpu = $3, memory_gb = $4, disk_gb = $5 where key = $1`,
				z.Key, z.Label, z.VCPU, z.MemoryGB, z.DiskGB)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return ErrNotFound
			}
		}
		return setOffers(ctx, tx, "size", z.Key, offers)
	})
	if IsUniqueViolation(err) {
		return ErrInUse
	}
	return err
}

func (s *Store) DeleteSizePreset(ctx context.Context, key string) error {
	return deleteOffered(ctx, s, "size", `delete from size_presets where key = $1`, key)
}

func deleteOffered(ctx context.Context, s *Store, kind, sql, key string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `delete from offers where kind = $1 and key = $2`, kind, key); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, sql, key)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return err
	})
}
