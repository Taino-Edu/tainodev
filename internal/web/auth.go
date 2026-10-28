package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// O prefixo __Host- obriga o navegador a só aceitar o cookie se ele for
// Secure, com Path=/ e sem Domain, ou seja: preso a tainodev.com.br via HTTPS.
const sessionCookie = "__Host-tainodev_session"

// sessions emite e valida cookies de sessão assinados com HMAC.
// Não há estado no servidor: o cookie carrega a validade e a assinatura.
type sessions struct {
	key []byte
	ttl time.Duration
}

// A chave mistura o segredo com a senha do admin: trocar a senha
// invalida na hora todas as sessões abertas.
func newSessions(secret []byte, adminPassword string, ttl time.Duration) *sessions {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(adminPassword))
	return &sessions{key: mac.Sum(nil), ttl: ttl}
}

func (s *sessions) sign(payload string) string {
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *sessions) issue(now time.Time) string {
	exp := strconv.FormatInt(now.Add(s.ttl).Unix(), 10)
	return exp + "." + s.sign(exp)
}

func (s *sessions) valid(value string, now time.Time) bool {
	exp, sig, ok := strings.Cut(value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.sign(exp))) {
		return false
	}
	unix, err := strconv.ParseInt(exp, 10, 64)
	return err == nil && now.Unix() < unix
}

func (s *Server) setSessionCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   true,
		HttpOnly: true,                    // JavaScript não lê o cookie
		SameSite: http.SameSiteStrictMode, // nunca é enviado em requisições de outros sites
	})
}

func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.cfg.AdminPassword == "" {
			s.notFound(c)
			c.Abort()
			return
		}
		cookie, err := c.Cookie(sessionCookie)
		if err != nil || !s.sessions.valid(cookie, time.Now()) {
			c.Redirect(http.StatusSeeOther, "/admin/login")
			c.Abort()
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

func (s *Server) loginPage(c *gin.Context) {
	if s.cfg.AdminPassword == "" {
		s.notFound(c)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.HTML(http.StatusOK, "login.html", s.page(gin.H{}))
}

func (s *Server) login(c *gin.Context) {
	if s.cfg.AdminPassword == "" {
		s.notFound(c)
		return
	}
	ip := c.ClientIP()
	// Além do limite por IP, um teto global segura ataques vindos de muitos IPs.
	if !s.limiter.allow(ip, time.Now()) || !s.globalLimiter.allow("*", time.Now()) {
		c.HTML(http.StatusTooManyRequests, "login.html", s.page(gin.H{
			"Error": "Muitas tentativas. Espere alguns minutos e tente de novo.",
		}))
		return
	}

	got := sha256.Sum256([]byte(c.PostForm("password")))
	want := sha256.Sum256([]byte(s.cfg.AdminPassword))
	// Comparação em tempo constante para não vazar a senha por timing.
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		s.limiter.fail(ip, time.Now())
		s.globalLimiter.fail("*", time.Now())
		s.log.Warn("login falhou", "ip", ip)
		c.HTML(http.StatusUnauthorized, "login.html", s.page(gin.H{"Error": "Senha incorreta."}))
		return
	}

	s.limiter.reset(ip)
	s.setSessionCookie(c, s.sessions.issue(time.Now()), int(s.sessions.ttl.Seconds()))
	c.Redirect(http.StatusSeeOther, "/admin")
}

func (s *Server) logout(c *gin.Context) {
	s.setSessionCookie(c, "", -1)
	c.Redirect(http.StatusSeeOther, "/")
}

// loginLimiter limita tentativas de senha erradas por IP (contra força bruta).
type loginLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: window, fails: map[string][]time.Time{}}
}

// recent descarta falhas antigas; precisa ser chamado com o lock.
func (l *loginLimiter) recent(ip string, now time.Time) []time.Time {
	kept := l.fails[ip][:0]
	for _, t := range l.fails[ip] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, ip)
		return nil
	}
	l.fails[ip] = kept
	return kept
}

func (l *loginLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip, now)) < l.max
}

func (l *loginLimiter) fail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[ip] = append(l.recent(ip, now), now)
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}
