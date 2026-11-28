package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func NewStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS history (
 id TEXT PRIMARY KEY,
 created_at INTEGER NOT NULL,
 source TEXT NOT NULL,
 prompt TEXT NOT NULL,
 file TEXT NOT NULL,
 mime TEXT,
 width INTEGER DEFAULT 0,
 height INTEGER DEFAULT 0,
 img_key TEXT,
 group_id TEXT,
 size TEXT,
 starred INTEGER DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_history_created ON history(created_at DESC);
`); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Add(e HistoryEntry) error {
	star := 0
	if e.Starred {
		star = 1
	}
	_, err := s.db.Exec(`INSERT INTO history
(id, created_at, source, prompt, file, mime, width, height, img_key, group_id, size, starred)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		e.ID, e.CreatedAt, e.Source, e.Prompt, e.File, e.Mime, e.Width, e.Height, e.ImgKey, e.GroupID, e.Size, star)
	return err
}

func (s *Store) Get(id string) (*HistoryEntry, error) {
	row := s.db.QueryRow(`SELECT id,created_at,source,prompt,file,mime,width,height,img_key,group_id,size,starred
FROM history WHERE id=?`, id)
	return scanEntry(row)
}

type HistoryFilter struct {
	Query   string
	Source  string
	Starred bool
	Limit   int
}

func (s *Store) List(f HistoryFilter) ([]HistoryEntry, error) {
	where := []string{"1=1"}
	args := []any{}
	if q := strings.TrimSpace(f.Query); q != "" {
		where = append(where, "prompt LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escapeLike(q)+"%")
	}
	if f.Source == "gen" || f.Source == "faceswap" {
		where = append(where, "source=?")
		args = append(args, f.Source)
	}
	if f.Starred {
		where = append(where, "starred=1")
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	q := `SELECT id,created_at,source,prompt,file,mime,width,height,img_key,group_id,size,starred
FROM history WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	for rows.Next() {
		var e HistoryEntry
		var star int
		if err := rows.Scan(&e.ID, &e.CreatedAt, &e.Source, &e.Prompt, &e.File, &e.Mime,
			&e.Width, &e.Height, &e.ImgKey, &e.GroupID, &e.Size, &star); err != nil {
			return nil, err
		}
		e.Starred = star == 1
		out = append(out, e)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func (s *Store) ToggleStar(id string) (bool, error) {
	var star int
	if err := s.db.QueryRow(`SELECT starred FROM history WHERE id=?`, id).Scan(&star); err != nil {
		return false, err
	}
	next := 1 - star
	if _, err := s.db.Exec(`UPDATE history SET starred=? WHERE id=?`, next, id); err != nil {
		return false, err
	}
	return next == 1, nil
}

func (s *Store) Delete(id string) (string, error) {
	var file string
	if err := s.db.QueryRow(`SELECT file FROM history WHERE id=?`, id).Scan(&file); err != nil {
		return "", err
	}
	if _, err := s.db.Exec(`DELETE FROM history WHERE id=?`, id); err != nil {
		return "", err
	}
	return file, nil
}

func (s *Store) Count() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM history`).Scan(&n)
	return n, err
}

func scanEntry(row *sql.Row) (*HistoryEntry, error) {
	var e HistoryEntry
	var star int
	err := row.Scan(&e.ID, &e.CreatedAt, &e.Source, &e.Prompt, &e.File, &e.Mime,
		&e.Width, &e.Height, &e.ImgKey, &e.GroupID, &e.Size, &star)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	e.Starred = star == 1
	return &e, nil
}

func (s *Store) Export(dir string, entries []HistoryEntry) (string, error) {
	dest := filepath.Join(dir, "002m-history-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(filepath.Join(dest, "images"), 0o755); err != nil {
		return "", err
	}
	type exportEntry struct {
		HistoryEntry
		ExportFile string `json:"export_file"`
	}
	out := make([]exportEntry, 0, len(entries))
	for _, e := range entries {
		name := e.ID + filepath.Ext(e.File)
		dst := filepath.Join(dest, "images", name)
		if b, err := os.ReadFile(e.File); err == nil {
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				return "", err
			}
			e2 := exportEntry{HistoryEntry: e, ExportFile: filepath.ToSlash(filepath.Join("images", name))}
			out = append(out, e2)
		}
	}
	payload := map[string]any{
		"exported_at": time.Now().Format(time.RFC3339),
		"count":       len(out),
		"items":       out,
		"note":        "图片仅保存在本机；gen.002m.com 上的在线副本约 30 天后清理。",
	}
	b, _ := jsonMarshalIndent(payload)
	if err := os.WriteFile(filepath.Join(dest, "history.json"), b, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

func jsonMarshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
