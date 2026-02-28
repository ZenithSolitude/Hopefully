package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func Init(dataDir string) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("mkdir data: %w", err)
	}
	path := filepath.Join(dataDir, "hopefully.db")
	var err error
	DB, err = sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	DB.SetMaxOpenConns(1)
	if err := migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	log.Printf("db: %s", path)
	return nil
}

func migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			username   TEXT    UNIQUE NOT NULL,
			password   TEXT    NOT NULL,
			full_name  TEXT    NOT NULL DEFAULT '',
			email      TEXT    NOT NULL DEFAULT '',
			is_admin   INTEGER NOT NULL DEFAULT 0,
			is_active  INTEGER NOT NULL DEFAULT 1,
			created_at TEXT    NOT NULL DEFAULT (datetime('now')),
			last_login TEXT
		)`,

		`CREATE TABLE IF NOT EXISTS roles (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT    UNIQUE NOT NULL,
			description TEXT    NOT NULL DEFAULT '',
			permissions TEXT    NOT NULL DEFAULT '[]',
			is_system   INTEGER NOT NULL DEFAULT 0
		)`,

		`CREATE TABLE IF NOT EXISTS user_roles (
			user_id INTEGER NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
			role_id INTEGER NOT NULL REFERENCES roles(id)  ON DELETE CASCADE,
			PRIMARY KEY (user_id, role_id)
		)`,

		`CREATE TABLE IF NOT EXISTS modules (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			name         TEXT    UNIQUE NOT NULL,
			version      TEXT    NOT NULL DEFAULT '0.0.0',
			description  TEXT    NOT NULL DEFAULT '',
			author       TEXT    NOT NULL DEFAULT '',
			status       TEXT    NOT NULL DEFAULT 'inactive',
			source_type  TEXT    NOT NULL DEFAULT '',
			source_url   TEXT    NOT NULL DEFAULT '',
			manifest     TEXT    NOT NULL DEFAULT '{}',
			error_log    TEXT    NOT NULL DEFAULT '',
			installed_at TEXT    NOT NULL DEFAULT (datetime('now'))
		)`,

		// Начальные роли
		`INSERT OR IGNORE INTO roles (name, description, permissions, is_system)
		 VALUES ('admin', 'Администратор', '["*"]', 1)`,
		`INSERT OR IGNORE INTO roles (name, description, permissions, is_system)
		 VALUES ('user',  'Пользователь',  '["dashboard.view","modules.view"]', 1)`,
	}

	for _, s := range stmts {
		if _, err := DB.Exec(s); err != nil {
			return fmt.Errorf("%w\nSQL: %s", err, s)
		}
	}
	return nil
}
