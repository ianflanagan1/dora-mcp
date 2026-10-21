// Package store reads DORA records from a SQLite database.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/ianflanagan1/dora-mcp/internal/dora"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// schema is the SQL that creates the database's tables.
//
//go:embed schema.sql
var schema string

// ErrNotFound is returned by Open when there is no database at the path.
var ErrNotFound = errors.New("database not found")

// ErrExists is returned by Create when there is already a file at the path.
var ErrExists = errors.New("database already exists")

// Store reads DORA records from a SQLite database opened read-only.
type Store struct {
	db *sql.DB
}

// Open opens the database at path read-only. Nothing done through the Store
// can change the data.
func Open(ctx context.Context, path string) (*Store, error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	db, err := sql.Open("sqlite", dsn(path, "mode=ro&_pragma=query_only(1)"))
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Create creates a database at path holding exactly r. The path must not
// already exist. If r is inconsistent, such as a change of a deployment it
// does not contain, Create returns an error and leaves no file behind.
func Create(ctx context.Context, path string, r dora.Records) (err error) {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%w: %s", ErrExists, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn(path, "_pragma=foreign_keys(1)"))
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, db.Close())
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	return write(ctx, db, r)
}

func write(ctx context.Context, db *sql.DB, r dora.Records) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	exec := func(query string, args ...any) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}

	if err := exec(schema); err != nil {
		return fmt.Errorf("creating schema: %w", err)
	}
	serviceIDs := map[string]int64{}
	for i, s := range r.Services {
		serviceIDs[s.Name] = int64(i + 1)
		if err := exec(`INSERT INTO services (id, name, team) VALUES (?, ?, ?)`, i+1, s.Name, s.Team); err != nil {
			return fmt.Errorf("service %q: %w", s.Name, err)
		}
	}
	serviceID := func(name string) (int64, error) {
		id, ok := serviceIDs[name]
		if !ok {
			return 0, fmt.Errorf("unknown service %q", name)
		}
		return id, nil
	}

	for _, d := range r.Deployments {
		sid, err := serviceID(d.Service)
		if err == nil {
			err = exec(`INSERT INTO deployments (id, service_id, deployed_at, remediation) VALUES (?, ?, ?, ?)`,
				d.ID, sid, formatTime(d.DeployedAt), d.Remediation)
		}
		if err != nil {
			return fmt.Errorf("deployment %d: %w", d.ID, err)
		}
	}
	for _, c := range r.Changes {
		if err := exec(`INSERT INTO changes (id, deployment_id, sha, committed_at) VALUES (?, ?, ?, ?)`,
			c.ID, c.DeploymentID, c.SHA, formatTime(c.CommittedAt)); err != nil {
			return fmt.Errorf("change %d: %w", c.ID, err)
		}
	}
	for _, i := range r.Incidents {
		var resolvedAt *string
		if i.ResolvedAt != nil {
			s := formatTime(*i.ResolvedAt)
			resolvedAt = &s
		}
		sid, err := serviceID(i.Service)
		if err == nil {
			err = exec(`INSERT INTO incidents (id, service_id, caused_by_deployment_id, severity, title, started_at, resolved_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				i.ID, sid, i.CausedByDeploymentID, i.Severity, i.Title, formatTime(i.StartedAt), resolvedAt)
		}
		if err != nil {
			return fmt.Errorf("incident %d: %w", i.ID, err)
		}
	}
	return tx.Commit()
}

func dsn(path, query string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs), RawQuery: query}).String()
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Services returns every service, ordered by name.
func (s *Store) Services(ctx context.Context) ([]dora.Service, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, team FROM services ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r *sql.Rows) (dora.Service, error) {
		var svc dora.Service
		err := r.Scan(&svc.Name, &svc.Team)
		return svc, err
	})
}

// DataRange returns the times of the first and last deployments, or two zero
// times when there are none.
func (s *Store) DataRange(ctx context.Context) (first, last time.Time, err error) {
	var firstAt, lastAt sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT MIN(deployed_at), MAX(deployed_at) FROM deployments`).Scan(&firstAt, &lastAt)
	if err != nil || !firstAt.Valid {
		return time.Time{}, time.Time{}, err
	}
	if first, err = parseTime(firstAt.String); err != nil {
		return time.Time{}, time.Time{}, err
	}
	last, err = parseTime(lastAt.String)
	return first, last, err
}

// Filter selects records for a window, optionally for one service or one
// team. An empty Service or Team does not narrow the selection.
type Filter struct {
	Service string
	Team    string
	Window  dora.Window
}

func (f Filter) args() []any {
	return []any{formatTime(f.Window.From), formatTime(f.Window.To), f.Service, f.Service, f.Team, f.Team}
}

// Deployments returns the deployments made in the window, oldest first.
func (s *Store) Deployments(ctx context.Context, f Filter) ([]dora.Deployment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, s.name, d.deployed_at, d.remediation
		FROM deployments d JOIN services s ON s.id = d.service_id
		WHERE d.deployed_at >= ? AND d.deployed_at < ? AND (? = '' OR s.name = ?) AND (? = '' OR s.team = ?)
		ORDER BY d.deployed_at, d.id`, f.args()...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanDeployment)
}

// Deployment returns the deployment with the id, and whether there is one.
func (s *Store) Deployment(ctx context.Context, id int64) (dora.Deployment, bool, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, s.name, d.deployed_at, d.remediation
		FROM deployments d JOIN services s ON s.id = d.service_id
		WHERE d.id = ?`, id)
	if err != nil {
		return dora.Deployment{}, false, err
	}
	found, err := collect(rows, scanDeployment)
	if err != nil || len(found) == 0 {
		return dora.Deployment{}, false, err
	}
	return found[0], true, nil
}

func scanDeployment(r *sql.Rows) (dora.Deployment, error) {
	var d dora.Deployment
	var deployedAt string
	if err := r.Scan(&d.ID, &d.Service, &deployedAt, &d.Remediation); err != nil {
		return d, err
	}
	var err error
	d.DeployedAt, err = parseTime(deployedAt)
	return d, err
}

// Changes returns the changes shipped by deployments made in the window.
func (s *Store) Changes(ctx context.Context, f Filter) ([]dora.Change, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.deployment_id, c.sha, c.committed_at
		FROM changes c
		JOIN deployments d ON d.id = c.deployment_id
		JOIN services s ON s.id = d.service_id
		WHERE d.deployed_at >= ? AND d.deployed_at < ? AND (? = '' OR s.name = ?) AND (? = '' OR s.team = ?)
		ORDER BY c.committed_at, c.id`, f.args()...)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanChange)
}

// ChangesShippedBy returns the changes the deployment shipped, oldest first.
func (s *Store) ChangesShippedBy(ctx context.Context, deploymentID int64) ([]dora.Change, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, deployment_id, sha, committed_at FROM changes
		WHERE deployment_id = ?
		ORDER BY committed_at, id`, deploymentID)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanChange)
}

// FindChanges returns at most limit changes whose SHA starts with prefix,
// oldest first. The prefix is compared as it is, with no wildcards.
func (s *Store) FindChanges(ctx context.Context, prefix string, limit int) ([]dora.Change, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, deployment_id, sha, committed_at FROM changes
		WHERE substr(sha, 1, ?) = ?
		ORDER BY committed_at, id
		LIMIT ?`, len(prefix), prefix, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanChange)
}

func scanChange(r *sql.Rows) (dora.Change, error) {
	var c dora.Change
	var committedAt string
	if err := r.Scan(&c.ID, &c.DeploymentID, &c.SHA, &committedAt); err != nil {
		return c, err
	}
	var err error
	c.CommittedAt, err = parseTime(committedAt)
	return c, err
}

// Incidents returns the incidents that started in the window, oldest first.
func (s *Store) Incidents(ctx context.Context, f Filter) ([]dora.Incident, error) {
	return s.incidents(ctx, `i.started_at >= ? AND i.started_at < ?
		AND (? = '' OR s.name = ?) AND (? = '' OR s.team = ?)`, f.args()...)
}

// IncidentsCausedByDeployments returns the incidents caused by deployments
// made in the window, whenever the incidents started.
func (s *Store) IncidentsCausedByDeployments(ctx context.Context, f Filter) ([]dora.Incident, error) {
	return s.incidents(ctx, `i.caused_by_deployment_id IN (
			SELECT id FROM deployments WHERE deployed_at >= ? AND deployed_at < ?)
		AND (? = '' OR s.name = ?) AND (? = '' OR s.team = ?)`, f.args()...)
}

// IncidentsCausedBy returns the incidents the deployment caused, oldest first.
func (s *Store) IncidentsCausedBy(ctx context.Context, deploymentID int64) ([]dora.Incident, error) {
	return s.incidents(ctx, `i.caused_by_deployment_id = ?`, deploymentID)
}

// incidents selects the incidents that meet condition, which takes args as
// its parameters. The condition is always a constant from this file.
func (s *Store) incidents(ctx context.Context, condition string, args ...any) ([]dora.Incident, error) {
	//nolint:gosec // condition is a constant, not input
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, s.name, i.caused_by_deployment_id, i.severity, i.title, i.started_at, i.resolved_at
		FROM incidents i JOIN services s ON s.id = i.service_id
		WHERE `+condition+`
		ORDER BY i.started_at, i.id`, args...)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r *sql.Rows) (dora.Incident, error) {
		var i dora.Incident
		var causedBy sql.NullInt64
		var startedAt string
		var resolvedAt sql.NullString
		if err := r.Scan(&i.ID, &i.Service, &causedBy, &i.Severity, &i.Title, &startedAt, &resolvedAt); err != nil {
			return i, err
		}
		if causedBy.Valid {
			i.CausedByDeploymentID = &causedBy.Int64
		}
		if i.StartedAt, err = parseTime(startedAt); err != nil {
			return i, err
		}
		if resolvedAt.Valid {
			t, err := parseTime(resolvedAt.String)
			if err != nil {
				return i, err
			}
			i.ResolvedAt = &t
		}
		return i, nil
	})
}

func collect[T any](rows *sql.Rows, scan func(*sql.Rows) (T, error)) ([]T, error) {
	defer func() { _ = rows.Close() }()
	items := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}
