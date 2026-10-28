// tainodev é o portfólio + encurtador de links de tainodev.com.br.
//
// Configuração por variáveis de ambiente:
//
//	ADDR            endereço de escuta (padrão 127.0.0.1:8080)
//	DB_PATH         arquivo SQLite (padrão tainodev.db)
//	ADMIN_PASSWORD  senha do /admin; vazio desliga o painel
//	SESSION_SECRET  chave do cookie de sessão (mín. 32 caracteres)
//	SITE_NAME, SITE_HEADLINE, SITE_BIO  textos da home
//	SITE_EMAIL                           opcional; vazio esconde o email
//
// Para importar links de um JSON (pula slugs que já existem):
//
//	tainodev -seed projetos.json
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"tainodev/internal/store"
	"tainodev/internal/web"
)

func main() {
	seedFile := flag.String("seed", "", "importa links de um arquivo JSON e sai")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	st, err := store.Open(env("DB_PATH", "tainodev.db"))
	if err != nil {
		log.Error("abrindo banco", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	if *seedFile != "" {
		err = seed(st, *seedFile, log)
	} else {
		err = serve(st, log)
	}
	if err != nil {
		log.Error("encerrando", "err", err)
		os.Exit(1)
	}
}

func serve(st *store.Store, log *slog.Logger) error {
	if os.Getenv(gin.EnvGinMode) == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	adminPassword := os.Getenv("ADMIN_PASSWORD")
	if adminPassword == "" {
		log.Warn("ADMIN_PASSWORD vazio: painel /admin desligado")
	}
	secret := []byte(os.Getenv("SESSION_SECRET"))
	if len(secret) < 32 {
		// Sem segredo fixo, as sessões só valem até o próximo restart.
		log.Warn("SESSION_SECRET ausente ou curto: usando um aleatório")
		secret = []byte(rand.Text() + rand.Text())
	}

	srv, err := web.New(st, web.Config{
		Profile: web.Profile{
			Name:     env("SITE_NAME", "Taino Dev"),
			Headline: env("SITE_HEADLINE", "Desenvolvedor de software"),
			Bio:      os.Getenv("SITE_BIO"),
			Email:    os.Getenv("SITE_EMAIL"),
		},
		AdminPassword: adminPassword,
		SessionSecret: secret,
	}, log)
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              env("ADDR", "127.0.0.1:8080"),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	// Ctrl+C ou `systemctl stop` disparam um desligamento limpo.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("servidor no ar", "addr", httpSrv.Addr)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("desligando...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
	return nil
}

// seedLink é o formato de cada item do JSON de importação.
type seedLink struct {
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Kind        string   `json:"kind"`
	Tags        []string `json:"tags"`
	Position    int      `json:"position"`
}

func seed(st *store.Store, path string, log *slog.Logger) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var items []seedLink
	if err := json.Unmarshal(data, &items); err != nil {
		return fmt.Errorf("lendo %s: %w", path, err)
	}

	ctx := context.Background()
	for _, it := range items {
		kind := store.Kind(it.Kind)
		if !kind.Valid() {
			return fmt.Errorf("link %q: tipo inválido %q", it.Slug, it.Kind)
		}
		_, err := st.Create(ctx, store.Link{
			Slug: it.Slug, Title: it.Title, Description: it.Description,
			URL: it.URL, Kind: kind, Tags: strings.Join(it.Tags, ","), Position: it.Position,
		})
		switch {
		case errors.Is(err, store.ErrSlugTaken):
			log.Info("já existe, pulando", "slug", it.Slug)
		case err != nil:
			return fmt.Errorf("link %q: %w", it.Slug, err)
		default:
			log.Info("importado", "slug", it.Slug)
		}
	}
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
