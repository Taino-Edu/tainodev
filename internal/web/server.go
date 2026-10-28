// Package web tem as rotas HTTP do portfólio: home, redirects e painel admin.
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"

	"tainodev/internal/store"
)

//go:embed templates static
var assets embed.FS

// Profile são os dados fixos que aparecem no topo da home.
type Profile struct {
	Name     string
	Headline string
	Bio      string
	Email    string
}

type Config struct {
	Profile       Profile
	AdminPassword string // vazio desliga o painel
	SessionSecret []byte // chave HMAC do cookie de sessão
}

type Server struct {
	store    *store.Store
	cfg      Config
	sessions *sessions
	limiter  *loginLimiter
	log      *slog.Logger
	engine   *gin.Engine
}

func New(st *store.Store, cfg Config, log *slog.Logger) (*Server, error) {
	pages, err := parsePages()
	if err != nil {
		return nil, err
	}
	s := &Server{
		store:    st,
		cfg:      cfg,
		sessions: newSessions(cfg.SessionSecret, cfg.AdminPassword, 12*time.Hour),
		limiter:  newLoginLimiter(5, 15*time.Minute),
		log:      log,
	}
	s.engine = s.routes(pages)
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.engine }

func (s *Server) routes(pages pagesRender) *gin.Engine {
	r := gin.New()
	r.HTMLRender = pages
	// O tráfego chega pela Cloudflare → Caddy (local). Com isso, c.ClientIP()
	// devolve o IP real do visitante (header CF-Connecting-IP).
	r.TrustedPlatform = gin.PlatformCloudflare
	r.SetTrustedProxies([]string{"127.0.0.1", "::1"})

	r.Use(gin.Recovery(), s.logRequests(), securityHeaders(), crossOriginProtection())

	static, _ := fs.Sub(assets, "static")
	r.StaticFS("/static", http.FS(static))
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	r.GET("/", s.home)
	r.GET("/privacidade", s.privacy)
	r.GET("/:slug", s.redirect)

	r.GET("/admin/login", s.loginPage)
	r.POST("/admin/login", s.login)

	admin := r.Group("/admin", s.requireAdmin())
	admin.GET("", s.adminIndex)
	admin.POST("/logout", s.logout)
	admin.POST("/links", s.adminCreate)
	admin.GET("/links/:id", s.adminEdit)
	admin.POST("/links/:id", s.adminUpdate)
	admin.POST("/links/:id/delete", s.adminDelete)

	r.NoRoute(s.notFound)
	return r
}

// pagesRender implementa o render HTML do Gin: um template por página,
// cada um montado com o layout base.
type pagesRender map[string]*template.Template

func (p pagesRender) Instance(name string, data any) render.Render {
	return render.HTML{Template: p[name], Name: "base", Data: data}
}

func parsePages() (pagesRender, error) {
	pages := pagesRender{}
	for _, name := range []string{"home.html", "admin.html", "edit.html", "login.html", "privacy.html", "notfound.html"} {
		t, err := template.New(name).ParseFS(assets, "templates/base.html", "templates/form.html", "templates/"+name)
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", name, err)
		}
		pages[name] = t
	}
	return pages, nil
}

// page monta os dados comuns a todas as páginas.
func (s *Server) page(data gin.H) gin.H {
	data["Profile"] = s.cfg.Profile
	return data
}

func (s *Server) notFound(c *gin.Context) {
	c.HTML(http.StatusNotFound, "notfound.html", s.page(gin.H{}))
}

func (s *Server) serverError(c *gin.Context, err error) {
	s.log.Error("erro interno", "path", c.Request.URL.Path, "err", err)
	c.String(http.StatusInternalServerError, "erro interno")
}

// securityHeaders aplica as políticas de segurança do navegador em toda resposta.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		// Só carrega recursos do próprio domínio. 'self' em script cobre os
		// scripts que a Cloudflare serve em /cdn-cgi/.
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
			"object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("Strict-Transport-Security", "max-age=31536000")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), browsing-topics=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		c.Next()
	}
}

// crossOriginProtection bloqueia POSTs vindos de outros sites (CSRF),
// usando os headers Sec-Fetch-Site/Origin que o navegador envia.
func crossOriginProtection() gin.HandlerFunc {
	cop := http.NewCrossOriginProtection()
	return func(c *gin.Context) {
		if err := cop.Check(c.Request); err != nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}

func (s *Server) logRequests() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		s.log.Info("req",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"dur", time.Since(start).Round(time.Microsecond),
			"ip", c.ClientIP(),
		)
	}
}
