#!/usr/bin/env bash
# Prepara a VPS (Ubuntu) para rodar o tainodev atrás do Caddy.
# Rode na VPS como root: bash setup-vps.sh
# É seguro rodar de novo: só cria o que ainda não existe.
set -euo pipefail

echo "== Portas 80/443 em uso =="
ss -ltnp '( sport = :80 or sport = :443 )' || true
if ss -ltnp '( sport = :80 or sport = :443 )' | grep LISTEN | grep -vq caddy; then
  echo "ATENÇÃO: outro programa já usa a porta 80/443 (veja acima). Parando para não derrubar nada."
  exit 1
fi

echo "== Caddy =="
if ! command -v caddy >/dev/null; then
  apt-get update
  apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
  curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' > /etc/apt/sources.list.d/caddy-stable.list
  apt-get update
  apt-get install -y caddy
fi
caddy version

echo "== Firewall: 443 só para a Cloudflare =="
# Ninguém acessa a VPS direto: só a Cloudflare fala com o Caddy. Isso também
# impede que alguém forje o header CF-Connecting-IP.
CF_NETS="$(curl -fsS https://www.cloudflare.com/ips-v4) $(curl -fsS https://www.cloudflare.com/ips-v6)"
for net in $CF_NETS; do
  ufw allow proto tcp from "$net" to any port 443 comment 'cloudflare' >/dev/null
done
ufw status | grep -c cloudflare | xargs echo "regras da Cloudflare:"
install -d -m 755 /etc/caddy
echo "trusted_proxies static $(echo $CF_NETS)" > /etc/caddy/cloudflare-trusted.caddy

echo "== Usuário e pastas =="
id tainodev >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin tainodev
install -d -m 755 /opt/tainodev
install -d -m 750 -o tainodev -g tainodev /var/lib/tainodev
install -d -m 750 /etc/tainodev
install -d -m 750 -o root -g caddy /etc/caddy/certs

echo "== Chave privada e CSR do certificado de origem =="
# A chave é gerada aqui e nunca sai da VPS. Só o CSR (público) vai para a Cloudflare.
KEY=/etc/caddy/certs/tainodev.key
CSR=/etc/caddy/certs/tainodev.csr
if [ ! -f "$KEY" ]; then
  openssl ecparam -name prime256v1 -genkey -noout -out "$KEY"
  chown root:caddy "$KEY"
  chmod 640 "$KEY"
fi
if [ ! -f "$CSR" ]; then
  openssl req -new -key "$KEY" -out "$CSR" -subj "/CN=tainodev.com.br" \
    -addext "subjectAltName=DNS:tainodev.com.br,DNS:*.tainodev.com.br"
fi
echo "Cole este CSR na Cloudflare (SSL/TLS > Origin Server > Create Certificate > Use my private key and CSR):"
cat "$CSR"

echo "== .env =="
if [ ! -f /etc/tainodev/tainodev.env ]; then
  cat > /etc/tainodev/tainodev.env <<EOF
ADMIN_PASSWORD=$(openssl rand -base64 18)
SESSION_SECRET=$(openssl rand -base64 48)
SITE_NAME=Eduardo Taino
SITE_HEADLINE=Desenvolvedor de software
SITE_BIO=Construo sistemas web, backends e ferramentas. Aqui ficam os links pra tudo que eu fiz.
SITE_EMAIL=contato@tainodev.com.br
EOF
  chmod 600 /etc/tainodev/tainodev.env
  echo "Criado /etc/tainodev/tainodev.env com senha aleatória. Veja com: sudo cat /etc/tainodev/tainodev.env"
fi

echo "Pronto. Próximos passos: salvar o certificado em /etc/caddy/certs/tainodev.pem e rodar o deploy."
