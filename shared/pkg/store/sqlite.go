package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

// IncidentNote stores user annotations and postmortem findings tied to an outage.
type IncidentNote struct {
	ID         int64  `json:"id"`
	IncidentID string `json:"incident_id"`
	Title      string `json:"title"`
	Author     string `json:"author"`
	Content    string `json:"content"`
	CreatedAt  int64  `json:"created_at"`
}

// SavedBookmark stores saved time-travel scrubber positions for team sharing.
type SavedBookmark struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	TimestampUnixNano int64  `json:"timestamp_unix_nano"`
	WindowSeconds     int    `json:"window_seconds"`
	Description       string `json:"description"`
	CreatedAt         int64  `json:"created_at"`
}

// MetadataStore abstracts local-first incident metadata storage.
type MetadataStore interface {
	SaveIncidentNote(ctx context.Context, note *IncidentNote) error
	GetIncidentNotes(ctx context.Context, incidentID string) ([]*IncidentNote, error)
	SaveBookmark(ctx context.Context, bookmark *SavedBookmark) error
	GetBookmarks(ctx context.Context) ([]*SavedBookmark, error)
	Close() error
}

// SQLiteMetadataStore implements MetadataStore with modernc.org/sqlite (pure Go, zero CGO).
type SQLiteMetadataStore struct {
	db *sql.DB
	mu sync.RWMutex
}

// NewSQLiteMetadataStore initializes the local SQLite database and migrates tables.
func NewSQLiteMetadataStore(dbPath string) (*SQLiteMetadataStore, error) {
	if dbPath == "" {
		dbPath = "data/metadata.db"
	}
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sqlite directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Pragmas for local performance and crash resilience
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA foreign_keys = ON;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to exec pragma %s: %w", p, err)
		}
	}

	schema := `
	CREATE TABLE IF NOT EXISTS incident_notes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		incident_id TEXT NOT NULL,
		title TEXT NOT NULL,
		author TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_incident_notes_id ON incident_notes(incident_id);

	CREATE TABLE IF NOT EXISTS saved_bookmarks (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		timestamp_unix_nano INTEGER NOT NULL,
		window_seconds INTEGER NOT NULL,
		description TEXT,
		created_at INTEGER NOT NULL
	);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to migrate sqlite schema: %w", err)
	}

	return &SQLiteMetadataStore{db: db}, nil
}

// SaveIncidentNote inserts an incident note.
func (s *SQLiteMetadataStore) SaveIncidentNote(ctx context.Context, note *IncidentNote) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT INTO incident_notes (incident_id, title, author, content, created_at) VALUES (?, ?, ?, ?, ?)`
	res, err := s.db.ExecContext(ctx, query, note.IncidentID, note.Title, note.Author, note.Content, note.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert incident note: %w", err)
	}
	id, err := res.LastInsertId()
	if err == nil {
		note.ID = id
	}
	return nil
}

// GetIncidentNotes retrieves notes for a given incident.
func (s *SQLiteMetadataStore) GetIncidentNotes(ctx context.Context, incidentID string) ([]*IncidentNote, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, incident_id, title, author, content, created_at FROM incident_notes WHERE incident_id = ? ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, query, incidentID)
	if err != nil {
		return nil, fmt.Errorf("failed to query incident notes: %w", err)
	}
	defer rows.Close()

	var notes []*IncidentNote
	for rows.Next() {
		var n IncidentNote
		if err := rows.Scan(&n.ID, &n.IncidentID, &n.Title, &n.Author, &n.Content, &n.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan incident note: %w", err)
		}
		notes = append(notes, &n)
	}
	return notes, rows.Err()
}

// SaveBookmark stores a new saved timeline position bookmark.
func (s *SQLiteMetadataStore) SaveBookmark(ctx context.Context, bookmark *SavedBookmark) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `INSERT OR REPLACE INTO saved_bookmarks (id, name, timestamp_unix_nano, window_seconds, description, created_at) VALUES (?, ?, ?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query, bookmark.ID, bookmark.Name, bookmark.TimestampUnixNano, bookmark.WindowSeconds, bookmark.Description, bookmark.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to insert bookmark: %w", err)
	}
	return nil
}

// GetBookmarks retrieves all saved bookmarks.
func (s *SQLiteMetadataStore) GetBookmarks(ctx context.Context) ([]*SavedBookmark, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query := `SELECT id, name, timestamp_unix_nano, window_seconds, description, created_at FROM saved_bookmarks ORDER BY created_at DESC`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query bookmarks: %w", err)
	}
	defer rows.Close()

	var bookmarks []*SavedBookmark
	for rows.Next() {
		var b SavedBookmark
		if err := rows.Scan(&b.ID, &b.Name, &b.TimestampUnixNano, &b.WindowSeconds, &b.Description, &b.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan bookmark: %w", err)
		}
		bookmarks = append(bookmarks, &b)
	}
	return bookmarks, rows.Err()
}

// Close closes the underlying SQLite database.
func (s *SQLiteMetadataStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}
