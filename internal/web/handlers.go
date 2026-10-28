package web

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"tainodev/internal/store"
)

// Slugs que não podem virar redirect porque colidem com rotas do site.
var reservedSlugs = map[string]bool{
	"admin": true, "static": true, "healthz": true, "privacidade": true,
	"favicon.ico": true, "robots.txt": true, "cdn-cgi": true,
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func (s *Server) home(c *gin.Context) {
	links, err := s.store.List(c.Request.Context(), "")
	if err != nil {
		s.serverError(c, err)
		return
	}
	var projects, socials []store.Link
	for _, l := range links {
		switch l.Kind {
		case store.KindProject:
			projects = append(projects, l)
		case store.KindSocial:
			socials = append(socials, l)
		}
	}
	c.HTML(http.StatusOK, "home.html", s.page(gin.H{"Projects": projects, "Socials": socials}))
}

func (s *Server) privacy(c *gin.Context) {
	c.HTML(http.StatusOK, "privacy.html", s.page(gin.H{}))
}

func (s *Server) redirect(c *gin.Context) {
	target, err := s.store.Resolve(c.Request.Context(), c.Param("slug"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(c)
		return
	}
	if err != nil {
		s.serverError(c, err)
		return
	}
	// 302 (e não 301) para o navegador não guardar em cache: assim dá pra
	// trocar o destino depois e todo clique continua sendo contado.
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, target)
}

// --- admin ---

type formData struct {
	Link   store.Link
	Errors map[string]string
}

func (s *Server) adminIndex(c *gin.Context) {
	s.renderAdmin(c, http.StatusOK, formData{Link: store.Link{Kind: store.KindProject}})
}

func (s *Server) renderAdmin(c *gin.Context, status int, form formData) {
	links, err := s.store.List(c.Request.Context(), "")
	if err != nil {
		s.serverError(c, err)
		return
	}
	c.HTML(status, "admin.html", s.page(gin.H{"Links": links, "Form": form, "Action": "/admin/links"}))
}

func (s *Server) renderEdit(c *gin.Context, status int, form formData) {
	c.HTML(status, "edit.html", s.page(gin.H{
		"Form":   form,
		"Action": "/admin/links/" + strconv.FormatInt(form.Link.ID, 10),
	}))
}

func (s *Server) adminCreate(c *gin.Context) {
	form := parseLinkForm(c)
	if len(form.Errors) > 0 {
		s.renderAdmin(c, http.StatusUnprocessableEntity, form)
		return
	}
	if _, err := s.store.Create(c.Request.Context(), form.Link); err != nil {
		if errors.Is(err, store.ErrSlugTaken) {
			form.Errors["slug"] = "esse slug já existe"
			s.renderAdmin(c, http.StatusUnprocessableEntity, form)
			return
		}
		s.serverError(c, err)
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin")
}

func linkID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil
}

func (s *Server) adminEdit(c *gin.Context) {
	id, ok := linkID(c)
	if !ok {
		s.notFound(c)
		return
	}
	l, err := s.store.Get(c.Request.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(c)
		return
	}
	if err != nil {
		s.serverError(c, err)
		return
	}
	s.renderEdit(c, http.StatusOK, formData{Link: l})
}

func (s *Server) adminUpdate(c *gin.Context) {
	id, ok := linkID(c)
	if !ok {
		s.notFound(c)
		return
	}
	form := parseLinkForm(c)
	form.Link.ID = id
	if len(form.Errors) > 0 {
		s.renderEdit(c, http.StatusUnprocessableEntity, form)
		return
	}
	switch err := s.store.Update(c.Request.Context(), form.Link); {
	case errors.Is(err, store.ErrSlugTaken):
		form.Errors["slug"] = "esse slug já existe"
		s.renderEdit(c, http.StatusUnprocessableEntity, form)
		return
	case errors.Is(err, store.ErrNotFound):
		s.notFound(c)
		return
	case err != nil:
		s.serverError(c, err)
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin")
}

func (s *Server) adminDelete(c *gin.Context) {
	id, ok := linkID(c)
	if !ok {
		s.notFound(c)
		return
	}
	if err := s.store.Delete(c.Request.Context(), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(c, err)
		return
	}
	c.Redirect(http.StatusSeeOther, "/admin")
}

// parseLinkForm lê e valida o formulário de link.
func parseLinkForm(c *gin.Context) formData {
	f := formData{Errors: map[string]string{}}
	l := &f.Link

	l.Slug = strings.ToLower(strings.TrimSpace(c.PostForm("slug")))
	l.Title = strings.TrimSpace(c.PostForm("title"))
	l.Description = strings.TrimSpace(c.PostForm("description"))
	l.URL = strings.TrimSpace(c.PostForm("url"))
	l.Kind = store.Kind(c.PostForm("kind"))
	l.Tags = strings.TrimSpace(c.PostForm("tags"))

	switch {
	case !slugPattern.MatchString(l.Slug):
		f.Errors["slug"] = "use só letras minúsculas, números e hífen (até 64)"
	case reservedSlugs[l.Slug]:
		f.Errors["slug"] = "esse slug é reservado pelo site"
	}
	if l.Title == "" {
		f.Errors["title"] = "obrigatório"
	}
	if u, err := url.Parse(l.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		f.Errors["url"] = "precisa ser uma URL completa, começando com https://"
	}
	if !l.Kind.Valid() {
		f.Errors["kind"] = "tipo inválido"
	}
	if p := strings.TrimSpace(c.PostForm("position")); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			f.Errors["position"] = "precisa ser um número"
		}
		l.Position = n
	}
	return f
}
