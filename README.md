# tainodev.com.br

Portfólio + encurtador de links em Go ([Gin](https://gin-gonic.com) + SQLite). Um binário só.

- `tainodev.com.br/`: perfil, redes e cards dos projetos
- `tainodev.com.br/{slug}`: redireciona pro link cadastrado e conta o clique
- `tainodev.com.br/admin`: painel com login pra cadastrar, editar e apagar links
- `tainodev.com.br/privacidade`: política de privacidade e cookies (LGPD)

O SQLite usa [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite), que é Go puro
(sem CGO), então dá pra compilar pra Linux direto do Windows. Templates, CSS e favicon
ficam embutidos no binário (`embed`).

## Segurança

- Login do admin com cookie `__Host-` assinado (HMAC), `Secure`, `HttpOnly`, `SameSite=Strict`, com validade de 12h.
  Trocar a senha derruba todas as sessões abertas.
- Limite de 5 tentativas de senha por IP a cada 15 minutos.
- Proteção contra CSRF com `http.CrossOriginProtection`.
- Cabeçalhos: CSP restrita, HSTS, `Permissions-Policy`, `X-Frame-Options`, COOP/CORP e `nosniff`.
- Visitantes não recebem nenhum cookie, então não é preciso banner de consentimento.
- O IP real do visitante vem do header `CF-Connecting-IP`, que a Cloudflare envia.

## Acessibilidade

Link "Pular para o conteúdo", foco visível no teclado e marcação semântica (landmarks, títulos e
listas). O formulário usa `label`, `aria-invalid` e `aria-describedby` nos erros, e a tabela tem
`caption` e `scope`. As cores seguem o contraste AA no tema claro e no escuro, e as animações
respeitam `prefers-reduced-motion`.

## Estrutura

```
main.go                  config por env, servidor, desligamento limpo, importação (-seed)
internal/store/          SQLite: CRUD + contador de cliques
internal/web/            rotas Gin, handlers, login/sessão, middlewares de segurança
internal/web/templates/  HTML
internal/web/static/     CSS e favicon
deploy/                  systemd, Caddy, scripts de setup e deploy, projetos.json
```

## Rodar local

```bash
ADMIN_PASSWORD=dev go run . -seed deploy/projetos.json   # importa os projetos uma vez
ADMIN_PASSWORD=dev go run .
```

Depois abra http://127.0.0.1:8080. O painel fica em `/admin` (senha `dev`).

| Variável | Padrão | O que é |
|---|---|---|
| `ADDR` | `127.0.0.1:8080` | onde o servidor escuta |
| `DB_PATH` | `tainodev.db` | arquivo SQLite |
| `ADMIN_PASSWORD` | vazio | senha do `/admin`; vazio desliga o painel |
| `SESSION_SECRET` | aleatório | chave do cookie de sessão (mínimo de 32 caracteres) |
| `SITE_NAME`, `SITE_HEADLINE`, `SITE_BIO`, `SITE_EMAIL` | | textos da home |

## Testes

```bash
go test ./...
```

## Deploy

```
Visitante (IPv4/IPv6) → Cloudflare (TLS 1.2+, Full strict) → Caddy :443 (cert de origem) → tainodev 127.0.0.1:8080
```

1. Na VPS, como root: `bash setup-vps.sh`. O script instala o Caddy, cria o usuário e as pastas e gera a
   **chave privada e o CSR** (a chave nunca sai da VPS), além do `.env` com uma senha aleatória.
2. Na Cloudflare, abra **SSL/TLS → Origin Server → Create Certificate → Use my private key and CSR**, cole o CSR
   e salve o certificado em `/etc/caddy/certs/tainodev.pem`.
3. No seu PC: `bash deploy/deploy.sh`. Ele roda os testes, compila, envia, cria o banco com os projetos (só na
   primeira vez) e reinicia.

Logs: `journalctl -u tainodev -f`. O banco fica em `/var/lib/tainodev/tainodev.db`; faça backup dele.
