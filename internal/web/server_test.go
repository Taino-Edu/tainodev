package web

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"tainodev/internal/store"
)

const testPassword = "senha-de-teste"

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	srv, err := New(st, Config{
		Profile:       Profile{Name: "Teste", Headline: "dev", Email: "a@b.c"},
		AdminPassword: testPassword,
		SessionSecret: []byte("segredo-de-teste-com-mais-de-32-bytes!!"),
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	return srv, st
}

// do executa uma requisição direto no handler (sem rede).
func do(srv *Server, method, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func login(t *testing.T, srv *Server) *http.Cookie {
	t.Helper()
	rec := do(srv, http.MethodPost, "/admin/login", url.Values{"password": {testPassword}}, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login: status %d, esperava 303", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatalf("cookie sem as flags de segurança: %+v", c)
			}
			return c
		}
	}
	t.Fatal("login não devolveu cookie de sessão")
	return nil
}

func TestAdminRequiresLogin(t *testing.T) {
	srv, _ := newTestServer(t)

	rec := do(srv, http.MethodGet, "/admin", nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
		t.Fatalf("sem login: status %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}

	forged := &http.Cookie{Name: sessionCookie, Value: "9999999999.assinatura-falsa"}
	if rec := do(srv, http.MethodGet, "/admin", nil, forged); rec.Code != http.StatusSeeOther {
		t.Fatalf("cookie forjado: status %d, esperava 303", rec.Code)
	}

	if rec := do(srv, http.MethodGet, "/admin", nil, login(t, srv)); rec.Code != http.StatusOK {
		t.Fatalf("logado: status %d, esperava 200", rec.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	srv, _ := newTestServer(t)
	wrong := url.Values{"password": {"errada"}}
	for i := range 5 {
		if rec := do(srv, http.MethodPost, "/admin/login", wrong, nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("tentativa %d: status %d, esperava 401", i+1, rec.Code)
		}
	}
	// Mesmo com a senha certa, o IP fica bloqueado até a janela passar.
	right := url.Values{"password": {testPassword}}
	if rec := do(srv, http.MethodPost, "/admin/login", right, nil); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("depois de 5 erros: status %d, esperava 429", rec.Code)
	}
}

func TestSessionExpires(t *testing.T) {
	s := newSessions([]byte("x"), "senha", time.Hour)
	now := time.Now()
	v := s.issue(now)
	if !s.valid(v, now) {
		t.Fatal("sessão recém-criada deveria ser válida")
	}
	if s.valid(v, now.Add(2*time.Hour)) {
		t.Fatal("sessão expirada não deveria ser válida")
	}
	if newSessions([]byte("x"), "outra-senha", time.Hour).valid(v, now) {
		t.Fatal("trocar a senha deveria invalidar a sessão")
	}
}

func TestCreateLinkThenRedirectAndHome(t *testing.T) {
	srv, st := newTestServer(t)
	cookie := login(t, srv)

	form := url.Values{
		"slug": {"meu-projeto"}, "title": {"Meu Projeto"}, "url": {"https://github.com/x/meu-projeto"},
		"kind": {"project"}, "tags": {"go, sqlite"}, "description": {"um projeto legal"},
	}
	if rec := do(srv, http.MethodPost, "/admin/links", form, cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("criar link: status %d, esperava 303", rec.Code)
	}

	rec := do(srv, http.MethodGet, "/meu-projeto", nil, nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://github.com/x/meu-projeto" {
		t.Fatalf("redirect: status %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}

	links, _ := st.List(t.Context(), "")
	if len(links) != 1 || links[0].Clicks != 1 {
		t.Fatalf("esperava 1 link com 1 clique, veio %+v", links)
	}

	home := do(srv, http.MethodGet, "/", nil, nil)
	for _, want := range []string{"Meu Projeto", "um projeto legal", "sqlite", `href="/meu-projeto"`, `href="#conteudo"`} {
		if !strings.Contains(home.Body.String(), want) {
			t.Errorf("home não contém %q", want)
		}
	}
}

func TestCreateLinkValidation(t *testing.T) {
	srv, st := newTestServer(t)
	cookie := login(t, srv)

	cases := map[string]url.Values{
		"slug reservado":     {"slug": {"privacidade"}, "title": {"X"}, "url": {"https://x.com"}, "kind": {"project"}},
		"slug com espaço":    {"slug": {"a b"}, "title": {"X"}, "url": {"https://x.com"}, "kind": {"project"}},
		"url javascript":     {"slug": {"x"}, "title": {"X"}, "url": {"javascript:alert(1)"}, "kind": {"project"}},
		"sem título":         {"slug": {"x"}, "title": {""}, "url": {"https://x.com"}, "kind": {"project"}},
		"tipo inválido":      {"slug": {"x"}, "title": {"X"}, "url": {"https://x.com"}, "kind": {"banana"}},
		"posição não numero": {"slug": {"x"}, "title": {"X"}, "url": {"https://x.com"}, "kind": {"project"}, "position": {"a"}},
	}
	for name, form := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(srv, http.MethodPost, "/admin/links", form, cookie)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, esperava 422", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), `aria-invalid="true"`) {
				t.Fatal("campo com erro deveria ter aria-invalid")
			}
		})
	}
	if links, _ := st.List(t.Context(), ""); len(links) != 0 {
		t.Fatalf("nenhum link inválido deveria ter sido salvo, veio %d", len(links))
	}
}

func TestCrossOriginPostBlocked(t *testing.T) {
	srv, st := newTestServer(t)
	cookie := login(t, srv)

	form := url.Values{"slug": {"x"}, "title": {"X"}, "url": {"https://x.com"}, "kind": {"project"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/links", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site") // simula um site malicioso postando aqui
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST cross-site: status %d, esperava 403", rec.Code)
	}
	if links, _ := st.List(t.Context(), ""); len(links) != 0 {
		t.Fatal("link não deveria ter sido criado")
	}
}

func TestSecurityHeadersAndPages(t *testing.T) {
	srv, _ := newTestServer(t)

	rec := do(srv, http.MethodGet, "/privacidade", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "LGPD") {
		t.Fatalf("privacidade: status %d", rec.Code)
	}
	for _, h := range []string{"Content-Security-Policy", "Strict-Transport-Security", "Permissions-Policy", "X-Content-Type-Options"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("faltando header %s", h)
		}
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("visitante não deveria receber cookies")
	}

	if rec := do(srv, http.MethodGet, "/nao-existe", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("slug inexistente: status %d, esperava 404", rec.Code)
	}
	if rec := do(srv, http.MethodGet, "/static/style.css", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("css: status %d", rec.Code)
	}
}
