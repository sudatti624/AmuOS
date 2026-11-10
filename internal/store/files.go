package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/sudatti624/AmuOS/internal/fileops"
	"github.com/sudatti624/AmuOS/internal/rbac"
	"github.com/sudatti624/AmuOS/internal/security"
)

type FileRoot struct {
	Alias      string      `json:"alias"`
	ReadRoles  []rbac.Role `json:"read_roles"`
	WriteRoles []rbac.Role `json:"write_roles"`
}

func fileAllowed(p rbac.Principal, host string, root FileRoot, write bool) bool {
	if !p.Enabled {
		return false
	}
	roles := root.ReadRoles
	if write {
		roles = root.WriteRoles
	}
	for _, g := range p.Grants {
		if g.Role == rbac.Owner && g.All {
			return true
		}
		if (g.All || g.HostID == host) && slices.Contains(roles, g.Role) {
			return true
		}
	}
	return false
}
func fileRoot(tx *sql.Tx, host, alias string) (FileRoot, string, error) {
	var encoded string
	err := tx.QueryRow("SELECT policy FROM file_roots WHERE host_id=? AND alias=?", host, alias).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return FileRoot{}, "", ErrNotFound
	}
	if err != nil {
		return FileRoot{}, "", err
	}
	var root FileRoot
	err = json.Unmarshal([]byte(encoded), &root)
	return root, encoded, err
}
func (s *Store) FileRoots(ctx context.Context, token, host string, now time.Time) ([]FileRoot, error) {
	roots := []FileRoot{}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		if state, err := hostState(tx, p, host); err != nil {
			return err
		} else if state == "" {
			return ErrNotFound
		}
		rows, err := tx.Query("SELECT policy FROM file_roots WHERE host_id=? ORDER BY alias", host)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				return err
			}
			var root FileRoot
			if err := json.Unmarshal([]byte(encoded), &root); err != nil {
				return err
			}
			if p.Allows(rbac.HostManage, host) || fileAllowed(p, host, root, false) || fileAllowed(p, host, root, true) {
				roots = append(roots, root)
			}
		}
		return rows.Err()
	})
	return roots, err
}
func (s *Store) SetFileRoots(ctx context.Context, token, csrf, host string, roots []FileRoot, now time.Time) error {
	if len(roots) > 16 {
		return ErrInput
	}
	seen := map[string]bool{}
	for i := range roots {
		if roots[i].ReadRoles == nil {
			roots[i].ReadRoles = []rbac.Role{}
		}
		if roots[i].WriteRoles == nil {
			roots[i].WriteRoles = []rbac.Role{}
		}
		root := roots[i]
		if !fileops.ValidName(root.Alias) || seen[root.Alias] || len(root.ReadRoles) > 3 || len(root.WriteRoles) > 2 {
			return ErrInput
		}
		seen[root.Alias] = true
		for _, r := range root.ReadRoles {
			if r != rbac.Admin && r != rbac.Operator && r != rbac.Viewer {
				return ErrInput
			}
		}
		for _, r := range root.WriteRoles {
			if r != rbac.Admin && r != rbac.Operator || !slices.Contains(root.ReadRoles, r) {
				return ErrInput
			}
		}
	}
	return s.accountMutationDetails(ctx, token, csrf, "file.roots.set", host, now, map[string]any{"roots": roots}, func(tx *sql.Tx, p rbac.Principal) error {
		state, err := hostState(tx, p, host)
		if err != nil {
			return err
		}
		if state == "" {
			return ErrNotFound
		}
		if !p.Allows(rbac.HostManage, host) {
			return ErrDenied
		}
		if state == "revoked" {
			return ErrConflict
		}
		if _, err := tx.Exec("DELETE FROM file_roots WHERE host_id=?", host); err != nil {
			return err
		}
		for _, root := range roots {
			encoded, err := json.Marshal(root)
			if err != nil {
				return err
			}
			if _, err := tx.Exec("INSERT INTO file_roots(host_id,alias,policy) VALUES(?,?,?)", host, root.Alias, string(encoded)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) CreateFileTask(ctx context.Context, token, csrf, host string, q fileops.Request, now time.Time) (Task, error) {
	if q.Validate() != nil || !ValidIdempotencyKey(idempotencyKey(ctx)) {
		return Task{}, ErrInput
	}
	encoded, _ := json.Marshal(q)
	var task Task
	err := s.accountMutationDetails(ctx, token, csrf, "file.request", host, now, map[string]any{"root": q.Root, "operation": q.Action, "path": q.Path, "destination": q.Destination}, func(tx *sql.Tx, p rbac.Principal) error {
		state, err := hostState(tx, p, host)
		if err != nil {
			return err
		}
		if state == "" {
			return ErrNotFound
		}
		if state == "revoked" {
			return ErrConflict
		}
		root, policy, err := fileRoot(tx, host, q.Root)
		if err != nil {
			return err
		}
		if !fileAllowed(p, host, root, !fileops.ReadAction(q.Action)) {
			return ErrDenied
		}
		task, err = scanTask(tx.QueryRow("SELECT "+taskColumns+" FROM tasks WHERE requester_id=? AND host_id=? AND operation='file' AND key_hash=?", p.ID, host, security.Digest(idempotencyKey(ctx))))
		if err == nil {
			var hash []byte
			if err = tx.QueryRow("SELECT input_hash FROM tasks WHERE id=?", task.ID).Scan(&hash); err != nil {
				return err
			}
			if !security.EqualDigest(string(encoded), hash) {
				return ErrConflict
			}
			task.Replayed = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var count int
		if err := tx.QueryRow("SELECT count(*) FROM tasks WHERE host_id=? AND state IN ('queued','dispatching','running')", host).Scan(&count); err != nil {
			return err
		}
		if count >= taskOpenLimit {
			return ErrConflict
		}
		task = Task{ID: security.Token(), Operation: "file", HostID: host, State: "queued", CreatedAt: now.Unix(), UpdatedAt: now.Unix()}
		if _, err := tx.Exec("INSERT INTO tasks(id,requester_id,operation,host_id,input_hash,key_hash,state,accept_deadline,request_id,created_at,updated_at) VALUES(?,?,'file',?,?,?,'queued',?,?,?,?)", task.ID, p.ID, host, security.Digest(string(encoded)), security.Digest(idempotencyKey(ctx)), now.Add(taskAcceptWindow).Unix(), RequestID(ctx), now.Unix(), now.Unix()); err != nil {
			return err
		}
		_, err = tx.Exec("INSERT INTO file_tasks(task_id,request,policy) VALUES(?,?,?)", task.ID, string(encoded), policy)
		return err
	})
	return task, err
}
func fileDelivery(tx *sql.Tx, id, host, requester string) (*fileops.Request, bool, error) {
	var encoded, policy string
	if err := tx.QueryRow("SELECT request,policy FROM file_tasks WHERE task_id=?", id).Scan(&encoded, &policy); err != nil {
		return nil, false, err
	}
	var q fileops.Request
	if err := json.Unmarshal([]byte(encoded), &q); err != nil {
		return nil, false, err
	}
	p, err := principal(tx, requester)
	if err != nil {
		return nil, false, err
	}
	root, current, err := fileRoot(tx, host, q.Root)
	if errors.Is(err, ErrNotFound) {
		return &q, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &q, current == policy && fileAllowed(p, host, root, !fileops.ReadAction(q.Action)), nil
}
func (s *Store) FileTaskResult(ctx context.Context, token, id string, now time.Time) (fileops.Result, error) {
	var result fileops.Result
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		p, _, err := authenticate(tx, token, now)
		if err != nil {
			return err
		}
		var host, requester, state string
		err = tx.QueryRow("SELECT host_id,requester_id,state FROM tasks WHERE id=? AND operation='file'", id).Scan(&host, &requester, &state)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if requester != p.ID || !p.Allows(rbac.HostRead, host) {
			return ErrNotFound
		}
		if h, err := hostState(tx, p, host); err != nil {
			return err
		} else if h == "" || h == "revoked" {
			return ErrNotFound
		}
		_, allowed, err := fileDelivery(tx, id, host, requester)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrDenied
		}
		if state != "succeeded" && state != "failed" {
			return ErrConflict
		}
		var encoded sql.NullString
		if err := tx.QueryRow("SELECT result FROM file_tasks WHERE task_id=?", id).Scan(&encoded); err != nil {
			return err
		}
		if !encoded.Valid {
			return ErrNotFound
		}
		if err := json.Unmarshal([]byte(encoded.String), &result); err != nil {
			return err
		}
		return audit(ctx, tx, now, p.ID, "file.result.read", id, host, "success", "")
	})
	return result, err
}
