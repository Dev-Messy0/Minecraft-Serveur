#!/usr/bin/env bash
# =============================================================================
#  build.sh — Build + packaging du panel Minecraft (Go) pour VPS + nginx
# =============================================================================
set -euo pipefail

# ----------------------------- Paramètres -----------------------------------
APP_NAME="mcpanel"
BIN_NAME="mcpanel"
INSTALL_DIR="/opt/mcpanel"
SERVICE_NAME="mcpanel"
SERVICE_USER="mcpanel"
PANEL_ADDR="127.0.0.1:8080"
MC_PORT="25565"
EMAIL="admin@example.com"
DOMAIN=""

# Identifiants admin — modifiables par prompt ou arguments
ADMIN_USER=""
ADMIN_PASSWORD=""

RED=$'\e[31m'; GRN=$'\e[32m'; YEL=$'\e[33m'; BLU=$'\e[34m'; RST=$'\e[0m'
ok()   { echo "${GRN}✓${RST} $*"; }
info() { echo "${BLU}ℹ${RST} $*"; }
warn() { echo "${YEL}⚠${RST} $*"; }
err()  { echo "${RED}✗${RST} $*" >&2; }
die()  { err "$*"; exit 1; }

# ----------------------------- Args parsing ---------------------------------
DO_INSTALL=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --install|-i)   DO_INSTALL=1; shift ;;
    --user|-u)      ADMIN_USER="$2"; shift 2 ;;
    --password|-p)  ADMIN_PASSWORD="$2"; shift 2 ;;
    --port)         MC_PORT="$2"; shift 2 ;;
    --email|-e)     EMAIL="$2"; shift 2 ;;
    --help|-h)
      cat <<EOF
Usage: $0 [options] [domaine]

Options :
  -u, --user USER        Nom d'utilisateur admin
  -p, --password PASS    Mot de passe admin
      --port PORT        Port Minecraft (défaut : 25565)
  -e, --email EMAIL      Email Let's Encrypt
  -i, --install          Installe automatiquement (root requis)

Sans -u/-p, les identifiants sont demandés de façon interactive.

Exemples :
  $0 panel.mondomaine.fr
  $0 -u admin -p 'MotDePasseFort!' panel.mondomaine.fr
  sudo $0 --install -u admin -p 'xxx' --email moi@domaine.fr panel.mondomaine.fr
EOF
      exit 0 ;;
    -*) die "Option inconnue : $1 (essaie --help)" ;;
    *)  DOMAIN="$1"; shift ;;
  esac
done

# ----------------------------- Pré-requis build -----------------------------
command -v go >/dev/null 2>&1 || die "Go n'est pas installé."
[[ -f go.mod ]]        || die "go.mod introuvable."
[[ -d templates ]]     || die "dossier 'templates' introuvable."
[[ -f main.go ]]       || die "main.go introuvable."
info "Go : $(go version | awk '{print $3}')"

ARCH_RAW=$(uname -m)
case "$ARCH_RAW" in
  x86_64|amd64)  GOARCH_TARGET="amd64" ;;
  aarch64|arm64) GOARCH_TARGET="arm64" ;;
  armv7l|armv7)  GOARCH_TARGET="arm" ;;
  *) die "Architecture non supportée : $ARCH_RAW" ;;
esac
info "Cible : linux/${GOARCH_TARGET}"

# ----------------------------- Build ----------------------------------------
info "Dépendances…"
go mod tidy

info "Compilation…"
CGO_ENABLED=0 GOOS=linux GOARCH="${GOARCH_TARGET}" \
  go build -trimpath -ldflags="-s -w -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o "${BIN_NAME}" .

[[ -f "${BIN_NAME}" ]] || die "build échoué"
ok "Binaire : ${BIN_NAME} ($(du -h "${BIN_NAME}" | cut -f1))"

# ----------------------------- Domaine --------------------------------------
if [[ -z "${DOMAIN}" ]]; then
  read -rp "Domaine du panel (ex: panel.mondomaine.fr) : " DOMAIN
fi
[[ -n "${DOMAIN}" ]] || die "Domaine requis."

if [[ -z "${EMAIL}" || "${EMAIL}" == "admin@example.com" ]]; then
  read -rp "Email Let's Encrypt [${EMAIL}] : " EMAIL_IN
  EMAIL="${EMAIL_IN:-$EMAIL}"
fi

# ----------------------------- Identifiants admin ---------------------------
echo
info "Configuration du compte administrateur"
echo

# Username
if [[ -z "${ADMIN_USER}" ]]; then
  read -rp "Nom d'utilisateur admin [admin] : " ADMIN_USER_IN
  ADMIN_USER="${ADMIN_USER_IN:-admin}"
fi
[[ "${ADMIN_USER}" =~ ^[a-zA-Z0-9_.-]{3,32}$ ]] \
  || die "Nom d'utilisateur invalide (3-32 caractères alphanumériques, _ . -)"

# Password — double saisie si interactif
if [[ -z "${ADMIN_PASSWORD}" ]]; then
  while true; do
    read -rsp "Mot de passe admin (min. 12 caractères) : " PWD1; echo
    read -rsp "Confirme le mot de passe : " PWD2; echo

    [[ "${PWD1}" == "${PWD2}" ]] || { warn "Les mots de passe ne correspondent pas."; continue; }
    [[ ${#PWD1} -ge 12 ]]        || { warn "Trop court (min. 12 caractères)."; continue; }
    [[ "${PWD1}" =~ [A-Z] ]]     || { warn "Ajoute au moins une majuscule."; continue; }
    [[ "${PWD1}" =~ [a-z] ]]     || { warn "Ajoute au moins une minuscule."; continue; }
    [[ "${PWD1}" =~ [0-9] ]]     || { warn "Ajoute au moins un chiffre."; continue; }
    ADMIN_PASSWORD="${PWD1}"
    break
  done
else
  [[ ${#ADMIN_PASSWORD} -ge 12 ]] \
    || die "Mot de passe trop court (min. 12 caractères)."
fi

ok "Utilisateur : ${ADMIN_USER}"
ok "Mot de passe : ${#ADMIN_PASSWORD} caractères (non affiché)"
ok "Port MC     : ${MC_PORT}"

# ----------------------------- Génération systemd ---------------------------
SYSTEMD_FILE="deploy/${SERVICE_NAME}.service"
mkdir -p deploy

# On génère un fichier .env séparé (root:mcpanel 0640)
# → évite d'exposer le mot de passe dans `systemctl status`
ENV_FILE="deploy/mcpanel.env"
cat > "${ENV_FILE}" <<EOF
# Identifiants initiaux du panel (lus uniquement au 1er lancement)
MCPANEL_ADMIN_USER=${ADMIN_USER}
MCPANEL_ADMIN_PASSWORD=${ADMIN_PASSWORD}
MCPANEL_MC_PORT=${MC_PORT}
EOF
chmod 600 "${ENV_FILE}"
ok "Généré : ${ENV_FILE} (chmod 600, à supprimer après le 1er démarrage)"

cat > "${SYSTEMD_FILE}" <<EOF
[Unit]
Description=Panel Minecraft (Go)
Documentation=https://${DOMAIN}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${SERVICE_USER}
Group=${SERVICE_USER}
WorkingDirectory=${INSTALL_DIR}
EnvironmentFile=-${INSTALL_DIR}/mcpanel.env
ExecStart=${INSTALL_DIR}/${BIN_NAME} \\
    -config ${INSTALL_DIR}/data/config.json \\
    -addr ${PANEL_ADDR} \\
    -templates ${INSTALL_DIR}/templates
Restart=on-failure
RestartSec=3

StandardOutput=journal
StandardError=journal
SyslogIdentifier=${SERVICE_NAME}

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=${INSTALL_DIR}/data
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
LockPersonality=true
MemoryMax=256M
CPUQuota=50%

[Install]
WantedBy=multi-user.target
EOF
ok "Généré : ${SYSTEMD_FILE}"

# ----------------------------- Génération nginx -----------------------------
NGINX_FILE="deploy/${DOMAIN}.conf"
cat > "${NGINX_FILE}" <<EOF
# =============================================================================
#  Panel Minecraft — ${DOMAIN}
#  /etc/nginx/sites-available/${DOMAIN}
# =============================================================================

server {
    listen 80;
    listen [::]:80;
    server_name ${DOMAIN};

    location /.well-known/acme-challenge/ {
        root /var/www/html;
    }

    location / {
        return 301 https://\$host\$request_uri;
    }
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    http2 on;
    server_name ${DOMAIN};

    ssl_certificate     /etc/letsencrypt/live/${DOMAIN}/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/${DOMAIN}/privkey.pem;
    ssl_trusted_certificate /etc/letsencrypt/live/${DOMAIN}/chain.pem;

    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 1d;
    ssl_session_tickets off;
    ssl_stapling on;
    ssl_stapling_verify on;

    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
    add_header Referrer-Policy "no-referrer" always;
    server_tokens off;

    client_max_body_size 1m;

    gzip on;
    gzip_types text/plain text/css application/json application/javascript text/xml application/xml;
    gzip_min_length 512;

    location / {
        proxy_pass http://${PANEL_ADDR};
        proxy_http_version 1.1;

        proxy_set_header Host              \$host;
        proxy_set_header X-Real-IP         \$remote_addr;
        proxy_set_header X-Forwarded-For   \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header X-Forwarded-Host  \$host;

        proxy_read_timeout  60s;
        proxy_send_timeout  60s;
        proxy_connect_timeout 5s;
        proxy_buffering off;
    }

    location ~ /\.(?!well-known) {
        deny all;
    }
}
EOF
ok "Généré : ${NGINX_FILE}"

# ----------------------------- Script d'installation ------------------------
INSTALL_SCRIPT="deploy/install.sh"
cat > "${INSTALL_SCRIPT}" <<'INSTALL_EOF'
#!/usr/bin/env bash
set -euo pipefail

DOMAIN="__DOMAIN__"
EMAIL="__EMAIL__"
ADMIN_USER="__ADMIN_USER__"
INSTALL_DIR="__INSTALL_DIR__"
SERVICE_NAME="__SERVICE_NAME__"
SERVICE_USER="__SERVICE_USER__"
PANEL_ADDR="__PANEL_ADDR__"
MC_PORT="__MC_PORT__"
BIN_NAME="__BIN_NAME__"

RED=$'\e[31m'; GRN=$'\e[32m'; BLU=$'\e[34m'; YEL=$'\e[33m'; RST=$'\e[0m'
ok(){ echo "${GRN}✓${RST} $*"; }
info(){ echo "${BLU}ℹ${RST} $*"; }
warn(){ echo "${YEL}⚠${RST} $*"; }
die(){ echo "${RED}✗${RST} $*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "Lance en root (sudo)."

info "Installation des paquets…"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq nginx certbot python3-certbot-nginx ufw curl

if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
  useradd -r -m -d "$INSTALL_DIR" -s /usr/sbin/nologin "$SERVICE_USER"
  ok "Utilisateur $SERVICE_USER créé"
fi

info "Copie des fichiers…"
mkdir -p "$INSTALL_DIR"/{data,templates}
install -m 0755 "$BIN_NAME" "$INSTALL_DIR/$BIN_NAME"
install -m 0644 templates/*.html "$INSTALL_DIR/templates/"
install -m 0600 mcpanel.env "$INSTALL_DIR/mcpanel.env"
chown -R "$SERVICE_USER:$SERVICE_USER" "$INSTALL_DIR"
chown root:"$SERVICE_USER" "$INSTALL_DIR/mcpanel.env"
chmod 0640 "$INSTALL_DIR/mcpanel.env"
chmod 700 "$INSTALL_DIR/data"
ok "Fichiers installés"

info "Service systemd…"
install -m 0644 "$SERVICE_NAME.service" /etc/systemd/system/"$SERVICE_NAME".service
systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl restart "$SERVICE_NAME"
sleep 2
systemctl is-active --quiet "$SERVICE_NAME" \
  || die "Service KO. Voir : journalctl -u $SERVICE_NAME -n 50"
ok "Service $SERVICE_NAME actif"

info "Vhost nginx…"
install -m 0644 "$DOMAIN.conf" /etc/nginx/sites-available/"$DOMAIN"
ln -sf /etc/nginx/sites-available/"$DOMAIN" /etc/nginx/sites-enabled/"$DOMAIN"
[[ -L /etc/nginx/sites-enabled/default ]] && rm -f /etc/nginx/sites-enabled/default
ok "Vhost installé"

info "Certificat Let's Encrypt…"
if [[ ! -d "/etc/letsencrypt/live/$DOMAIN" ]]; then
  systemctl stop nginx
  certbot certonly --standalone --non-interactive --agree-tos \
    --email "$EMAIL" -d "$DOMAIN" || die "certbot a échoué"
  systemctl start nginx
else
  certbot renew --quiet || true
fi
nginx -t || die "config nginx invalide"
systemctl reload nginx
ok "HTTPS opérationnel"

info "Firewall…"
ufw --force reset >/dev/null
ufw default deny incoming >/dev/null
ufw default allow outgoing >/dev/null
ufw allow 22/tcp comment 'SSH' >/dev/null
ufw allow 80,443/tcp comment 'Web' >/dev/null
ufw allow "$MC_PORT"/tcp comment 'Minecraft' >/dev/null
ufw --force enable >/dev/null
ok "ufw actif (22, 80, 443, $MC_PORT)"

# On peut supprimer le fichier .env contenant le mot de passe en clair
# puisque la config JSON (hash bcrypt) est maintenant écrite.
rm -f "$INSTALL_DIR/mcpanel.env"
systemctl restart "$SERVICE_NAME"
ok "Fichier .env supprimé (le mot de passe est hashé dans data/config.json)"

echo
echo "═══════════════════════════════════════════════════════════════"
echo "  ✅ Installation terminée"
echo "═══════════════════════════════════════════════════════════════"
echo
echo "  Panel  : https://$DOMAIN"
echo "  Login  : $ADMIN_USER"
echo "  MC     : $DOMAIN:$MC_PORT"
echo
echo "  Vérifs :"
echo "    systemctl status $SERVICE_NAME"
echo "    journalctl -u $SERVICE_NAME -f"
echo
INSTALL_EOF

# Substitution des placeholders
sed -i \
  -e "s|__DOMAIN__|${DOMAIN}|g" \
  -e "s|__EMAIL__|${EMAIL}|g" \
  -e "s|__ADMIN_USER__|${ADMIN_USER}|g" \
  -e "s|__INSTALL_DIR__|${INSTALL_DIR}|g" \
  -e "s|__SERVICE_NAME__|${SERVICE_NAME}|g" \
  -e "s|__SERVICE_USER__|${SERVICE_USER}|g" \
  -e "s|__PANEL_ADDR__|${PANEL_ADDR}|g" \
  -e "s|__MC_PORT__|${MC_PORT}|g" \
  -e "s|__BIN_NAME__|${BIN_NAME}|g" \
  "${INSTALL_SCRIPT}"

chmod +x "${INSTALL_SCRIPT}"
ok "Généré : ${INSTALL_SCRIPT}"

# ----------------------------- Récap ----------------------------------------
cat <<EOF

${GRN}═══════════════════════════════════════════════════════════════${RST}
${GRN}  ✅ Build terminé — artefacts dans ./deploy/${RST}
${GRN}═══════════════════════════════════════════════════════════════${RST}

  Domaine      : ${DOMAIN}
  Admin        : ${ADMIN_USER}
  Mot de passe : (défini, non affiché)
  Port MC      : ${MC_PORT}

  Fichiers :
    • ${SYSTEMD_FILE}
    • ${NGINX_FILE}
    • ${ENV_FILE}          ${YEL}(supprimé après install, contient le pwd en clair)${RST}
    • ${INSTALL_SCRIPT}

  ${BLU}Déploiement :${RST}
    rsync -avz deploy/ templates/ ${BIN_NAME} root@<IP>:/root/mcpanel-deploy/
    ssh root@<IP> 'cd /root/mcpanel-deploy && ./install.sh'

EOF

# ----------------------------- Install locale -------------------------------
if [[ "${DO_INSTALL}" -eq 1 ]]; then
  info "Lancement de l'installation locale…"
  [[ $EUID -eq 0 ]] || die "Pour --install, relance avec sudo."
  bash "${INSTALL_SCRIPT}"
fi