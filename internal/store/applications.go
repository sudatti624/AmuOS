package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
)

// ApplicationConfig contains registry metadata only. It never launches a process.
type ApplicationConfig struct {
	Runtime      string      `json:"runtime"`
	ReadRoles    []rbac.Role `json:"read_roles"`
	OperateRoles []rbac.Role `json:"operate_roles"`
}

type Application struct {
	HostID    string            `json:"host_id"`
	Name      string            `json:"name"`
	Config    ApplicationConfig `json:"config"`
	Revision  int64             `json:"revision"`
	Archived  bool              `json:"archived"`
	CreatedAt int64             `json:"created_at"`
	UpdatedAt int64             `json:"updated_at"`
}

func validApplicationName(name string) bool {
	if len(name) < 1 || len(name) > 63 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return name[len(name)-1] != '-'
}

func (c ApplicationConfig) valid() bool {
	if c.Runtime != "python" && c.Runtime != "node" && c.Runtime != "binary" {
		return false
	}
	for _, roles := range [][]rbac.Role{c.ReadRoles, c.OperateRoles} {
		if len(roles) > 3 {
			return false
		}
		seen := map[rbac.Role]bool{}
		for _, role := range roles {
			if seen[role] || role != rbac.Admin && role != rbac.Operator && role != rbac.Viewer {
				return false
			}
			seen[role] = true
		}
	}
	for _, role := range c.OperateRoles {
		if role == rbac.Viewer || !slices.Contains(c.ReadRoles, role) {
			return false
		}
	}
	return true
}

func applicationVisible(p rbac.Principal, a Application) bool {
	if !p.Enabled {
		return false
	}
	if p.Allows(rbac.HostManage, a.HostID) {
		return true
	}
	for _, g := range p.Grants {
		if (g.All || g.HostID == a.HostID) && slices.Contains(a.Config.ReadRoles, g.Role) {
			return true
		}
	}
	return false
}

func applicationHost(tx *sql.Tx, p rbac.Principal, host string, mutate bool) error {
	state, err := hostState(tx, p, host)
	if err != nil {
		return err
	}
	if state == "" {
		return ErrNotFound
	}
	if mutate && !p.Allows(rbac.HostManage, host) {
		return ErrDenied
	}
	if mutate && state == "revoked" {
		return ErrConflict
	}
	return nil
}

func scanApplication(row interface{ Scan(...any) error }) (Application, error) {
	var a Application
	var encoded string
	err := row.Scan(&a.HostID, &a.Name, &encoded, &a.Revision, &a.Archived, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal([]byte(encoded), &a.Config)
	}
	return a, err
}

const applicationColumns = "host_id,name,config,revision,archived,created_at,updated_at"

func (s *Store) Applications(ctx context.Context, token, host string, now time.Time) ([]Application, error) {
	items := []Application{}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if err = applicationHost(tx, p, host, false); err != nil {
			return err
		}
		rows, err := tx.Query("SELECT "+applicationColumns+" FROM applications WHERE host_id=? AND archived=0 ORDER BY name", host)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			a, err := scanApplication(rows)
			if err != nil {
				return err
			}
			if applicationVisible(p, a) {
				items = append(items, a)
			}
		}
		return rows.Err()
	})
	return items, err
}

func (s *Store) Application(ctx context.Context, token, host, name string, now time.Time) (Application, error) {
	var a Application
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if err = applicationHost(tx, p, host, false); err != nil {
			return err
		}
		a, err = scanApplication(tx.QueryRow("SELECT "+applicationColumns+" FROM applications WHERE host_id=? AND name=?", host, name))
		if err != nil {
			return err
		}
		if a.Archived || !applicationVisible(p, a) {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return Application{}, err
	}
	return a, nil
}

// PutApplication uses a stable host/name resource and an optimistic revision.
// Revision zero creates; replaying identical input never creates a second record.
func (s *Store) PutApplication(ctx context.Context, token, csrf, host, name string, expected int64, config ApplicationConfig, now time.Time) (Application, error) {
	return s.mutateApplication(ctx, token, csrf, host, name, expected, &config, now)
}
func (s *Store) ArchiveApplication(ctx context.Context, token, csrf, host, name string, expected int64, now time.Time) (Application, error) {
	return s.mutateApplication(ctx, token, csrf, host, name, expected, nil, now)
}

func (s *Store) mutateApplication(ctx context.Context, token, csrf, host, name string, expected int64, config *ApplicationConfig, now time.Time) (Application, error) {
	var result Application
	var rejected error
	action := "application.put"
	if config == nil {
		action = "application.archive"
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, hash, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		var before any
		changed := false
		mutate := func() error {
			if !security.EqualDigest(csrf, hash) {
				return ErrDenied
			}
			if !validApplicationName(name) || expected < 0 || expected >= 1<<53-1 || config != nil && !config.valid() {
				return ErrInput
			}
			if err := applicationHost(tx, p, host, true); err != nil {
				return err
			}
			old, err := scanApplication(tx.QueryRow("SELECT "+applicationColumns+" FROM applications WHERE host_id=? AND name=?", host, name))
			exists := err == nil
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			if config == nil {
				if !exists {
					return ErrNotFound
				}
				if expected == 0 {
					return ErrConflict
				}
				if old.Archived && old.Revision == expected+1 {
					before = old
					result = old
					return nil
				}
				if old.Archived || old.Revision != expected {
					return ErrConflict
				}
				before = old
				result = old
				result.Archived = true
			} else {
				// Canonical role order gives semantic retries the same representation.
				c := *config
				c.ReadRoles = append([]rbac.Role{}, c.ReadRoles...)
				c.OperateRoles = append([]rbac.Role{}, c.OperateRoles...)
				slices.Sort(c.ReadRoles)
				slices.Sort(c.OperateRoles)
				encoded, _ := json.Marshal(c)
				if exists {
					previous, _ := json.Marshal(old.Config)
					if !old.Archived && old.Revision == expected+1 && string(encoded) == string(previous) {
						before = old
						result = old
						return nil
					}
					if old.Archived || old.Revision != expected {
						return ErrConflict
					}
					before = old
				} else {
					if expected != 0 {
						return ErrConflict
					}
					var active, total int
					if err := tx.QueryRow("SELECT count(*),coalesce(sum(archived=0),0) FROM applications WHERE host_id=?", host).Scan(&total, &active); err != nil {
						return err
					}
					if active >= 128 || total >= 1024 {
						return ErrConflict
					}
				}
				result = Application{HostID: host, Name: name, Config: c, CreatedAt: now.Unix()}
				if exists {
					result.CreatedAt = old.CreatedAt
				}
			}
			result.Revision = expected + 1
			result.UpdatedAt = now.Unix()
			encoded, err := json.Marshal(result.Config)
			if err != nil {
				return err
			}
			_, err = tx.Exec("INSERT INTO applications("+applicationColumns+") VALUES(?,?,?,?,?,?,?) ON CONFLICT(host_id,name) DO UPDATE SET config=excluded.config,revision=excluded.revision,archived=excluded.archived,updated_at=excluded.updated_at", host, name, string(encoded), result.Revision, result.Archived, result.CreatedAt, result.UpdatedAt)
			changed = err == nil
			return err
		}
		rejected = mutate()
		if rejected != nil {
			reason := ""
			switch {
			case errors.Is(rejected, ErrDenied):
				reason = "forbidden"
			case errors.Is(rejected, ErrNotFound):
				reason = "not_found"
			case errors.Is(rejected, ErrConflict):
				reason = "conflict"
			case errors.Is(rejected, ErrInput):
				reason = "invalid_input"
			default:
				return rejected
			}
			return audit(ctx, tx, now, p.ID, action, "", host, "denied", reason)
		}
		return auditWithDetails(ctx, tx, now, p.ID, action, name, host, "success", "", map[string]any{"before": before, "after": result, "changed": changed})
	})
	if err != nil {
		return Application{}, err
	}
	if rejected != nil {
		return Application{}, rejected
	}
	return result, nil
}
