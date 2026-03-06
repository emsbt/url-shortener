package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/emsbt/url-shortener/internal/model"
	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a URL is not found in the database.
var ErrNotFound = errors.New("url not found")

// ErrDuplicateID is returned when the given ID already exists.
var ErrDuplicateID = errors.New("id already exists")

// URLRepository defines the persistence contract for short URLs.
type URLRepository interface {
	Create(ctx context.Context, url *model.URL) error
	GetByID(ctx context.Context, id string) (*model.URL, error)
	IncrementClickCount(ctx context.Context, id string) error
	List(ctx context.Context, page, size int) ([]model.URL, int64, error)
	ExistsID(ctx context.Context, id string) (bool, error)
}

// sqliteRepository is the SQLite-backed implementation of URLRepository.
type sqliteRepository struct {
	db *sql.DB
}

// NewSQLiteRepository opens (or creates) the SQLite database at the given path
// and runs the schema migration.
func NewSQLiteRepository(dsn string) (URLRepository, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &sqliteRepository{db: db}, nil
}

// migrate creates the urls table if it does not already exist.
func migrate(db *sql.DB) error {
	const schema = `
	CREATE TABLE IF NOT EXISTS urls (
		id              TEXT PRIMARY KEY,
		original_url    TEXT NOT NULL,
		short_url       TEXT NOT NULL,
		created_at      DATETIME NOT NULL,
		expiration_date DATETIME,
		click_count     INTEGER NOT NULL DEFAULT 0
	);`

	_, err := db.Exec(schema)
	return err
}

// Create inserts a new URL record. Returns ErrDuplicateID on primary-key conflict.
func (r *sqliteRepository) Create(ctx context.Context, url *model.URL) error {
	const q = `
	INSERT INTO urls (id, original_url, short_url, created_at, expiration_date, click_count)
	VALUES (?, ?, ?, ?, ?, ?)`

	var expirationDate interface{}
	if url.ExpirationDate != nil {
		expirationDate = url.ExpirationDate.UTC().Format(time.RFC3339)
	}

	_, err := r.db.ExecContext(ctx, q,
		url.ID,
		url.OriginalURL,
		url.ShortURL,
		url.CreatedAt.UTC().Format(time.RFC3339),
		expirationDate,
		url.ClickCount,
	)
	if err != nil {
		// modernc sqlite surfaces constraint errors as generic errors containing
		// "UNIQUE constraint failed"
		if isDuplicateError(err) {
			return ErrDuplicateID
		}
		return fmt.Errorf("insert url: %w", err)
	}
	return nil
}

// GetByID retrieves a URL by its short ID. Returns ErrNotFound when missing.
func (r *sqliteRepository) GetByID(ctx context.Context, id string) (*model.URL, error) {
	const q = `
	SELECT id, original_url, short_url, created_at, expiration_date, click_count
	FROM urls
	WHERE id = ?`

	row := r.db.QueryRowContext(ctx, q, id)
	return scanURL(row)
}

// IncrementClickCount atomically increments the click counter for a URL.
func (r *sqliteRepository) IncrementClickCount(ctx context.Context, id string) error {
	const q = `UPDATE urls SET click_count = click_count + 1 WHERE id = ?`
	res, err := r.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("increment click count: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns a paginated slice of URLs together with the total count.
func (r *sqliteRepository) List(ctx context.Context, page, size int) ([]model.URL, int64, error) {
	const countQ = `SELECT COUNT(*) FROM urls`
	var total int64
	if err := r.db.QueryRowContext(ctx, countQ).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count urls: %w", err)
	}

	const q = `
	SELECT id, original_url, short_url, created_at, expiration_date, click_count
	FROM urls
	ORDER BY created_at DESC
	LIMIT ? OFFSET ?`

	offset := (page - 1) * size
	rows, err := r.db.QueryContext(ctx, q, size, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list urls: %w", err)
	}
	defer rows.Close()

	var urls []model.URL
	for rows.Next() {
		u, err := scanURLRow(rows)
		if err != nil {
			return nil, 0, err
		}
		urls = append(urls, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("rows error: %w", err)
	}

	return urls, total, nil
}

// ExistsID reports whether the given ID is already taken.
func (r *sqliteRepository) ExistsID(ctx context.Context, id string) (bool, error) {
	const q = `SELECT 1 FROM urls WHERE id = ? LIMIT 1`
	var dummy int
	err := r.db.QueryRowContext(ctx, q, id).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("exists id: %w", err)
	}
	return true, nil
}

// ---- helpers ----

type scanner interface {
	Scan(dest ...any) error
}

func scanURL(s scanner) (*model.URL, error) {
	var (
		u              model.URL
		createdAtStr   string
		expDateStr     sql.NullString
	)
	err := s.Scan(
		&u.ID,
		&u.OriginalURL,
		&u.ShortURL,
		&createdAtStr,
		&expDateStr,
		&u.ClickCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan url: %w", err)
	}
	return parseURLTimes(&u, createdAtStr, expDateStr)
}

func scanURLRow(rows *sql.Rows) (*model.URL, error) {
	var (
		u            model.URL
		createdAtStr string
		expDateStr   sql.NullString
	)
	err := rows.Scan(
		&u.ID,
		&u.OriginalURL,
		&u.ShortURL,
		&createdAtStr,
		&expDateStr,
		&u.ClickCount,
	)
	if err != nil {
		return nil, fmt.Errorf("scan url row: %w", err)
	}
	return parseURLTimes(&u, createdAtStr, expDateStr)
}

func parseURLTimes(u *model.URL, createdAtStr string, expDateStr sql.NullString) (*model.URL, error) {
	t, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		return nil, fmt.Errorf("parse created_at: %w", err)
	}
	u.CreatedAt = t

	if expDateStr.Valid && expDateStr.String != "" {
		exp, err := time.Parse(time.RFC3339, expDateStr.String)
		if err != nil {
			return nil, fmt.Errorf("parse expiration_date: %w", err)
		}
		u.ExpirationDate = &exp
	}
	return u, nil
}

func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "UNIQUE constraint failed")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
