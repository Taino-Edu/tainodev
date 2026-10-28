package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCRUDAndResolve(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.Create(ctx, Link{Slug: "github", Title: "GitHub", URL: "https://github.com", Kind: KindSocial})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create(ctx, Link{Slug: "github", Title: "dup", URL: "https://x.com", Kind: KindSocial}); !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("slug duplicado: esperava ErrSlugTaken, veio %v", err)
	}

	for range 3 {
		url, err := s.Resolve(ctx, "github")
		if err != nil || url != "https://github.com" {
			t.Fatalf("Resolve = %q, %v", url, err)
		}
	}
	if _, err := s.Resolve(ctx, "nao-existe"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("esperava ErrNotFound, veio %v", err)
	}

	l, err := s.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if l.Clicks != 3 {
		t.Fatalf("clicks = %d, esperava 3", l.Clicks)
	}

	l.URL = "https://github.com/tainodev"
	if err := s.Update(ctx, l); err != nil {
		t.Fatal(err)
	}
	if url, _ := s.Resolve(ctx, "github"); url != "https://github.com/tainodev" {
		t.Fatalf("depois do update, url = %q", url)
	}

	if err := s.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("depois do delete: esperava ErrNotFound, veio %v", err)
	}
}

func TestListFiltersAndOrders(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	s.Create(ctx, Link{Slug: "b", Title: "B", URL: "https://b", Kind: KindProject, Position: 2})
	s.Create(ctx, Link{Slug: "a", Title: "A", URL: "https://a", Kind: KindProject, Position: 1})
	s.Create(ctx, Link{Slug: "gh", Title: "GH", URL: "https://gh", Kind: KindSocial})

	projects, err := s.List(ctx, KindProject)
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || projects[0].Slug != "a" || projects[1].Slug != "b" {
		t.Fatalf("projetos fora de ordem: %+v", projects)
	}

	all, _ := s.List(ctx, "")
	if len(all) != 3 {
		t.Fatalf("List(\"\") = %d links, esperava 3", len(all))
	}
}

func TestTagList(t *testing.T) {
	got := Link{Tags: " go, sqlite ,,htmx "}.TagList()
	want := []string{"go", "sqlite", "htmx"}
	if len(got) != len(want) {
		t.Fatalf("TagList = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("TagList = %v", got)
		}
	}
}
