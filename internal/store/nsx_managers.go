package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// NSXManager is an NSX Manager added besides the NSX integration's
// (ADR-0062); its password is sealed like a vCenter's.
type NSXManager struct {
	Name       string    `db:"name"`
	URL        string    `db:"url"`
	Username   string    `db:"username"`
	CertFile   string    `db:"cert_file"`
	KeyFile    string    `db:"key_file"`
	Insecure   bool      `db:"insecure"`
	CertSHA256 string    `db:"cert_sha256"`
	KeyID      string    `db:"key_id"`
	Nonce      []byte    `db:"nonce"`
	Ciphertext []byte    `db:"ciphertext"`
	CreatedAt  time.Time `db:"created_at"`
	UpdatedAt  time.Time `db:"updated_at"`
}

const nsxManagerCols = `name, url, username, cert_file, key_file, insecure, cert_sha256, key_id, nonce, ciphertext, created_at, updated_at`

func (s *Store) NSXManagers(ctx context.Context) ([]NSXManager, error) {
	rows, _ := s.Pool.Query(ctx, `select `+nsxManagerCols+` from nsx_managers order by name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[NSXManager])
}

func (s *Store) GetNSXManager(ctx context.Context, name string) (NSXManager, error) {
	rows, _ := s.Pool.Query(ctx, `select `+nsxManagerCols+` from nsx_managers where name = $1`, name)
	v, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[NSXManager])
	return v, notFound(err)
}

// SaveNSXManager adds the NSX Manager (create: ErrInUse when the name is
// taken) or changes it; a nil ciphertext keeps the password, and a
// principal identity (a certificate file) drops it.
func (s *Store) SaveNSXManager(ctx context.Context, v NSXManager, create bool) error {
	if v.CertFile != "" {
		v.KeyID, v.Nonce, v.Ciphertext = "", nil, nil
	}
	if create {
		_, err := s.Pool.Exec(ctx, `insert into nsx_managers (name, url, username, cert_file, key_file, insecure, cert_sha256,
			key_id, nonce, ciphertext) values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, v.Name, v.URL, v.Username, v.CertFile, v.KeyFile,
			v.Insecure, v.CertSHA256, v.KeyID, v.Nonce, v.Ciphertext)
		if IsUniqueViolation(err) {
			return ErrInUse
		}
		return err
	}
	tag, err := s.Pool.Exec(ctx, `update nsx_managers set url = $2, username = $3, cert_file = $4, key_file = $5, insecure = $6,
		cert_sha256 = $7, key_id = case when $4 <> '' then '' when $9::bytea is null then key_id else $8 end,
		nonce = case when $4 <> '' then null else coalesce($9, nonce) end,
		ciphertext = case when $4 <> '' then null else coalesce($10, ciphertext) end, updated_at = now() where name = $1`,
		v.Name, v.URL, v.Username, v.CertFile, v.KeyFile, v.Insecure, v.CertSHA256, v.KeyID, v.Nonce, v.Ciphertext)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ResealNSXManager replaces an NSX Manager's sealed password, unless it was
// replaced since it was read (nonce); false then.
func (s *Store) ResealNSXManager(ctx context.Context, name string, nonce []byte, keyID string, newNonce, ciphertext []byte) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `update nsx_managers set key_id = $3, nonce = $4, ciphertext = $5 where name = $1 and nonce = $2`,
		name, nonce, keyID, newNonce, ciphertext)
	return tag.RowsAffected() == 1, err
}

// ManagerPlannedError is a request waiting or running whose plan makes
// NSX objects on the NSX Manager being removed.
type ManagerPlannedError struct{ Request string }

func (e *ManagerPlannedError) Error() string {
	return fmt.Sprintf("request %s makes NSX objects on the NSX Manager", e.Request)
}

// DeleteNSXManager removes the NSX Manager and unlinks its vCenters;
// ErrInUse while a deployment not deleted has a segment, a group, a
// policy or a membership there, a ManagerPlannedError while a request
// waiting or running is to make one.
func (s *Store) DeleteNSXManager(ctx context.Context, name string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		var used bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from resources r join deployments d on d.id = r.deployment_id
			where r.type like 'nsx.%' and r.properties->>'nsx_manager' = $1 and d.status <> 'deleted')`, name).Scan(&used); err != nil {
			return err
		}
		if used {
			return ErrInUse
		}
		var number string
		err := tx.QueryRow(ctx, `select number from requests
			where status in ('pending_approval', 'scheduled', 'queued', 'running', 'cancelling')
			and jsonb_path_exists(plan, '$.** ? (@.nsx == $m)', jsonb_build_object('m', $1::text))
			order by submitted_at limit 1`, name).Scan(&number)
		if err == nil {
			return &ManagerPlannedError{Request: number}
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		tag, err := tx.Exec(ctx, `delete from nsx_managers where name = $1`, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		_, err = tx.Exec(ctx, `delete from vcenter_nsx where manager = $1`, name)
		return err
	})
}

// VCenterNSX is which NSX Manager serves each vCenter: by vCenter, ”
// the vCenter integration's; the Manager ” is the NSX integration's. A
// vCenter not there has none.
func (s *Store) VCenterNSX(ctx context.Context) (map[string]string, error) {
	rows, err := s.Pool.Query(ctx, `select vcenter, manager from vcenter_nsx`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var vc, m string
		if err := rows.Scan(&vc, &m); err != nil {
			return nil, err
		}
		out[vc] = m
	}
	return out, rows.Err()
}

// NSXManagerOf is the NSX Manager serving a vCenter (” for the vCenter
// integration's, and so is a name rvav doesn't know, as vcenters.Set reads
// it); false when none does.
func (s *Store) NSXManagerOf(ctx context.Context, vcenter string) (string, bool, error) {
	var m string
	err := s.Pool.QueryRow(ctx, `select manager from vcenter_nsx where vcenter =
		case when exists (select 1 from vcenters where name = $1) then $1 else '' end`, vcenter).Scan(&m)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	return m, err == nil, err
}

// lockVCenterNSX makes changes to which NSX Manager serves which vCenter
// one at a time: two at once each deleted what the other then inserted
// too, and one failed on the key.
func lockVCenterNSX(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext('rvav.vcenter_nsx'))`)
	return err
}

// LinkVCenters makes an NSX Manager serve these vCenters, and no others;
// a vCenter another served moves.
func (s *Store) LinkVCenters(ctx context.Context, manager string, vcenters []string) error {
	return s.Tx(ctx, func(tx pgx.Tx) error {
		if err := lockVCenterNSX(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `delete from vcenter_nsx where manager = $1 or vcenter = any($2::text[])`, manager, vcenters); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `insert into vcenter_nsx (vcenter, manager) select v, $1 from unnest($2::text[]) v`, manager, vcenters)
		return err
	})
}
