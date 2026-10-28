#!/usr/bin/env bash
# Compila e publica o tainodev na VPS. Rode do Windows (Git Bash) na raiz do projeto:
#   bash deploy/deploy.sh
# Variáveis opcionais: VPS (padrão root@38.226.23.24), PORT (padrão 6985), SSH_KEY (chave a usar).
set -euo pipefail

VPS="${VPS:-root@38.226.23.24}"
PORT="${PORT:-6985}"
KEY_OPTS=()
[ -n "${SSH_KEY:-}" ] && KEY_OPTS+=(-i "$SSH_KEY" -o IdentitiesOnly=yes)

echo "== testes =="
go test ./...

echo "== build linux/amd64 =="
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o tainodev-linux .

echo "== enviando =="
scp "${KEY_OPTS[@]}" -P "$PORT" tainodev-linux "$VPS:/opt/tainodev/tainodev.new"
scp "${KEY_OPTS[@]}" -P "$PORT" deploy/tainodev.service "$VPS:/etc/systemd/system/tainodev.service"
scp "${KEY_OPTS[@]}" -P "$PORT" deploy/Caddyfile "$VPS:/etc/caddy/Caddyfile"
scp "${KEY_OPTS[@]}" -P "$PORT" deploy/projetos.json "$VPS:/opt/tainodev/projetos.json"

echo "== reiniciando =="
ssh "${KEY_OPTS[@]}" -p "$PORT" "$VPS" bash -s <<'EOF'
set -euo pipefail
chmod +x /opt/tainodev/tainodev.new
mv /opt/tainodev/tainodev.new /opt/tainodev/tainodev
# Na primeira vez, cria o banco já com os projetos. Depois disso, o conteúdo
# é editado pelo /admin e o JSON não é mais aplicado.
if [ ! -f /var/lib/tainodev/tainodev.db ]; then
  sudo -u tainodev env DB_PATH=/var/lib/tainodev/tainodev.db /opt/tainodev/tainodev -seed /opt/tainodev/projetos.json
fi
systemctl daemon-reload
systemctl enable tainodev >/dev/null
systemctl restart tainodev
caddy validate --config /etc/caddy/Caddyfile >/dev/null && systemctl reload caddy
sleep 1
systemctl is-active tainodev
curl -fsS http://127.0.0.1:8090/healthz && echo " <- healthz"
EOF
rm -f tainodev-linux
echo "Deploy ok: https://tainodev.com.br"
