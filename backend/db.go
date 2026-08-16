package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var db *sql.DB

func initDB() error {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./data/atoms.db"
	}
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create db dir: %w", err)
	}

	var err error
	db, err = sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)")
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		log.Printf("warn: enable foreign_keys: %v", err)
	}

	if err := migrateSchema(); err != nil {
		return fmt.Errorf("migrate schema: %w", err)
	}

	log.Printf("db ready at %s", dbPath)
	return nil
}

func migrateSchema() error {
	if _, err := db.Exec("DROP TABLE IF EXISTS messages"); err != nil {
		return err
	}
	if _, err := db.Exec("DROP TABLE IF EXISTS compactions"); err != nil {
		return err
	}

	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		email TEXT UNIQUE NOT NULL,
		password_hash TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS projects (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		owner_id INTEGER NOT NULL,
		name TEXT NOT NULL DEFAULT 'Untitled Project',
		prompt TEXT DEFAULT '',
		slug TEXT UNIQUE,
		is_published INTEGER DEFAULT 0,
		is_generating INTEGER DEFAULT 0,
		generating_started_at DATETIME,
		stream_offset INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (owner_id) REFERENCES users(id)
	);

	CREATE INDEX IF NOT EXISTS idx_projects_owner ON projects(owner_id);
	CREATE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug);
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	if err := migrateProjectsColumns(); err != nil {
		return err
	}
	return nil
}

func migrateProjectsColumns() error {
	rows, err := db.Query("PRAGMA table_info(projects)")
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, dflt, pk interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	rows.Close()
	adds := []struct {
		col string
		def string
	}{
		{"is_generating", "INTEGER DEFAULT 0"},
		{"generating_started_at", "DATETIME"},
		{"stream_offset", "INTEGER DEFAULT 0"},
	}
	for _, a := range adds {
		if !have[a.col] {
			if _, err := db.Exec("ALTER TABLE projects ADD COLUMN " + a.col + " " + a.def); err != nil {
				return fmt.Errorf("add %s: %w", a.col, err)
			}
		}
	}
	return nil
}

type Message struct {
	ProjectID int64  `json:"-"`
	Role      string `json:"role"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolID    string `json:"tool_id,omitempty"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	CreatedAt    string `json:"created_at"`
}

func getUserByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	err := db.QueryRowContext(ctx, "SELECT id, email, password_hash, created_at FROM users WHERE email = ?", email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func getUserByID(ctx context.Context, id int64) (*User, error) {
	var u User
	err := db.QueryRowContext(ctx, "SELECT id, email, password_hash, created_at FROM users WHERE id = ?", id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &u, err
}

func createUser(ctx context.Context, email, hash string) (int64, error) {
	res, err := db.ExecContext(ctx, "INSERT INTO users (email, password_hash) VALUES (?, ?)", email, hash)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

type Project struct {
	ID                  int64  `json:"id"`
	OwnerID             int64  `json:"owner_id"`
	Name                string `json:"name"`
	Prompt              string `json:"prompt,omitempty"`
	Slug                string `json:"slug,omitempty"`
	IsPublished         bool   `json:"is_published"`
	IsGenerating        bool   `json:"is_generating"`
	GeneratingStartedAt string `json:"generating_started_at,omitempty"`
	StreamOffset        int64  `json:"stream_offset"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
}

func listProjects(ctx context.Context, ownerID int64) ([]Project, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, owner_id, name, COALESCE(prompt, ''), COALESCE(slug, ''),
		       is_published, is_generating, COALESCE(generating_started_at, ''),
		       COALESCE(stream_offset, 0), created_at, updated_at
		FROM projects WHERE owner_id = ? ORDER BY updated_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ps []Project
	for rows.Next() {
		var p Project
		var pub int
		if err := rows.Scan(&p.ID, &p.OwnerID, &p.Name, &p.Prompt, &p.Slug,
			&pub, &p.IsGenerating, &p.GeneratingStartedAt,
			&p.StreamOffset, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.IsPublished = pub == 1
		ps = append(ps, p)
	}
	return ps, rows.Err()
}

func getProject(ctx context.Context, id, ownerID int64) (*Project, error) {
	var p Project
	var pub int
	err := db.QueryRowContext(ctx, `
		SELECT id, owner_id, name, COALESCE(prompt, ''), COALESCE(slug, ''),
		       is_published, is_generating, COALESCE(generating_started_at, ''),
		       COALESCE(stream_offset, 0), created_at, updated_at
		FROM projects WHERE id = ? AND owner_id = ?`, id, ownerID).
		Scan(&p.ID, &p.OwnerID, &p.Name, &p.Prompt, &p.Slug,
			&pub, &p.IsGenerating, &p.GeneratingStartedAt,
			&p.StreamOffset, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.IsPublished = pub == 1
	return &p, nil
}

func getProjectBySlug(ctx context.Context, slug string) (*Project, error) {
	var p Project
	var pub int
	err := db.QueryRowContext(ctx, `
		SELECT id, owner_id, name, COALESCE(prompt, ''), COALESCE(slug, ''),
		       is_published, is_generating, COALESCE(generating_started_at, ''),
		       COALESCE(stream_offset, 0), created_at, updated_at
		FROM projects WHERE slug = ? AND is_published = 1`, slug).
		Scan(&p.ID, &p.OwnerID, &p.Name, &p.Prompt, &p.Slug,
			&pub, &p.IsGenerating, &p.GeneratingStartedAt,
			&p.StreamOffset, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.IsPublished = pub == 1
	return &p, nil
}

func createProject(ctx context.Context, ownerID int64, name string) (int64, error) {
	if name == "" {
		name = "Untitled Project"
	}
	res, err := db.ExecContext(ctx, "INSERT INTO projects (owner_id, name) VALUES (?, ?)", ownerID, name)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func updateProject(ctx context.Context, id, ownerID int64, name, prompt string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE projects SET name = ?, prompt = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND owner_id = ?`,
		name, prompt, id, ownerID)
	return err
}

func setProjectPublished(ctx context.Context, id, ownerID int64, slug string, published bool) (string, error) {
	pub := 0
	if published {
		pub = 1
	}
	_, err := db.ExecContext(ctx, `
		UPDATE projects SET is_published = ?, slug = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND owner_id = ?`,
		pub, slug, id, ownerID)
	if err != nil && published && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		for i := 0; i < 5; i++ {
			slug = makeSlug()
			_, err = db.ExecContext(ctx, `
				UPDATE projects SET is_published = ?, slug = ?, updated_at = CURRENT_TIMESTAMP
				WHERE id = ? AND owner_id = ?`,
				pub, slug, id, ownerID)
			if err == nil {
				return slug, nil
			}
			if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return slug, err
			}
		}
	}
	return slug, err
}

func setProjectGenerating(ctx context.Context, id int64, generating bool) error {
	if generating {
		_, err := db.ExecContext(ctx, `
			UPDATE projects SET is_generating = 1, generating_started_at = CURRENT_TIMESTAMP
			WHERE id = ?`, id)
		return err
	}
	_, err := db.ExecContext(ctx, `
		UPDATE projects SET is_generating = 0, generating_started_at = NULL
		WHERE id = ?`, id)
	return err
}

func deleteProject(ctx context.Context, id, ownerID int64) (bool, error) {
	dir := projectDir(id)
	if err := os.RemoveAll(dir); err != nil {
		log.Printf("warn: remove project files %s: %v", dir, err)
	}
	res, err := db.ExecContext(ctx, "DELETE FROM projects WHERE id = ? AND owner_id = ?", id, ownerID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func updateStreamOffset(ctx context.Context, id int64, offset int64) error {
	_, err := db.ExecContext(ctx, "UPDATE projects SET stream_offset = ? WHERE id = ?", offset, id)
	return err
}
