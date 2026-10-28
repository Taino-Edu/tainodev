// Package store guarda os links do portfólio num banco SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // driver SQLite em Go puro (sem CGO)
)

// Kind diz onde o link aparece na página inicial.
type Kind string

const (
	KindProject Kind = "project" // card na lista de projetos
	KindSocial  Kind = "social"  // ícone/link no topo (GitHub, LinkedIn...)
	KindHidden  Kind = "hidden"  // só funciona como redirect, não aparece na home
)

func (k Kind) Valid() bool {
	switch k {
	case KindProject, KindSocial, KindHidden:
		return true
	}
	return false
}

// Link é um atalho tainodev.com.br/{slug} que redireciona para URL.
type Link struct {
	ID          int64
	Slug        string
	Title       string
	Description string
	URL         string
	Kind        Kind
	Tags        string // separadas por vírgula, ex: "go,sqlite"
	Position    int    // ordem de exibição (menor primeiro)
	Clicks      int64
	CreatedAt   time.Time
}

// TagList devolve as tags já separadas e sem espaços.
func (l Link) TagList() []string {
	var out []string
	for t := range strings.SplitSeq(l.Tags, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

var (
	ErrNotFound  = errors.New("link não encontrado")
	ErrSlugTaken = errors.New("slug já está em uso")
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS links (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	slug        TEXT    NOT NULL UNIQUE,
	title       TEXT    NOT NULL,
	description TEXT    NOT NULL DEFAULT '',
	url         TEXT    NOT NULL,
	kind        TEXT    NOT NULL DEFAULT 'project',
	tags        TEXT    NOT NULL DEFAULT '',
	position    INTEGER NOT NULL DEFAULT 0,
	clicks      INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL
);`

// Open abre (ou cria) o banco no caminho indicado e aplica o schema.
func Open(path string) (*Store, error) {
	// WAL deixa leituras e escritas acontecerem ao mesmo tempo;
	// busy_timeout evita erro "database is locked" sob concorrência.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite só aceita um escritor por vez; uma conexão simplifica tudo
	// e é mais que suficiente para um portfólio.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("aplicando schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

const linkColumns = `id, slug, title, description, url, kind, tags, position, clicks, created_at`

func scanLink(row interface{ Scan(...any) error }) (Link, error) {
	var l Link
	var created int64
	err := row.Scan(&l.ID, &l.Slug, &l.Title, &l.Description, &l.URL, &l.Kind, &l.Tags, &l.Position, &l.Clicks, &created)
	l.CreatedAt = time.Unix(created, 0)
	return l, err
}

// List devolve todos os links ordenados. Se kind for vazio, traz todos os tipos.
func (s *Store) List(ctx context.Context, kind Kind) ([]Link, error) {
	query := `SELECT ` + linkColumns + ` FROM links`
	var args []any
	if kind != "" {
		query += ` WHERE kind = ?`
		args = append(args, kind)
	}
	query += ` ORDER BY position, id`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

func (s *Store) Get(ctx context.Context, id int64) (Link, error) {
	l, err := scanLink(s.db.QueryRowContext(ctx, `SELECT `+linkColumns+` FROM links WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Link{}, ErrNotFound
	}
	return l, err
}

// Resolve busca a URL de destino de um slug e conta o clique.
func (s *Store) Resolve(ctx context.Context, slug string) (string, error) {
	var url string
	err := s.db.QueryRowContext(ctx,
		`UPDATE links SET clicks = clicks + 1 WHERE slug = ? RETURNING url`, slug,
	).Scan(&url)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return url, err
}

func (s *Store) Create(ctx context.Context, l Link) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO links (slug, title, description, url, kind, tags, position, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		l.Slug, l.Title, l.Description, l.URL, l.Kind, l.Tags, l.Position, time.Now().Unix(),
	)
	if err != nil {
		return 0, translate(err)
	}
	return res.LastInsertId()
}

func (s *Store) Update(ctx context.Context, l Link) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE links SET slug = ?, title = ?, description = ?, url = ?, kind = ?, tags = ?, position = ?
		 WHERE id = ?`,
		l.Slug, l.Title, l.Description, l.URL, l.Kind, l.Tags, l.Position, l.ID,
	)
	if err != nil {
		return translate(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM links WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// translate converte erros do SQLite em erros do pacote.
func translate(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: links.slug") {
		return ErrSlugTaken
	}
	return err
}
