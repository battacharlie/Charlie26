#!/usr/bin/env bash
# CHARLIE26 – installazione su un server pubblico (VPS) con Rocky Linux / AlmaLinux 9.
#
# Cosa fa:
#   1. installa certbot e ottiene il certificato HTTPS gratuito (Let's Encrypt)
#      per il nome del server, con rinnovo automatico;
#   2. scarica il programma (versione Linux) e ne controlla l'impronta;
#   3. crea il servizio "charlie26" che parte da solo all'accensione;
#   4. apre nel firewall SOLO le porte necessarie (22, 80, 443 TCP e 443 UDP).
#
# Uso (da root, sul server):
#   bash installa-rocky.sh intercom.charlie26inter.com tua@email.it
#   bash installa-rocky.sh intercom.charlie26inter.com tua@email.it /percorso/charlie26-linux-amd64
#
# Prima di lanciarlo: il record DNS "A" del nome deve già puntare all'IP del server.
# La password della regia la scegli quando lo script la chiede (non resta nella
# cronologia dei comandi).

set -euo pipefail

DOMINIO="${1:-}"
EMAIL="${2:-}"
BINARIO_LOCALE="${3:-}"
REPO="battacharlie/Charlie26"
BIN=/usr/local/bin/charlie26
DATI=/var/lib/charlie26
CONF=/etc/charlie26

die() { echo "ERRORE: $*" >&2; exit 1; }
ok() { echo "  ✔ $*"; }

[ "$(id -u)" -eq 0 ] || die "lancialo da root (o con sudo)."
[ -n "$DOMINIO" ] && [ -n "$EMAIL" ] || die "uso: bash installa-rocky.sh NOME-DOMINIO EMAIL [percorso-programma]"
[[ "$DOMINIO" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$ ]] || die "nome di dominio non valido: $DOMINIO"
[[ "$DOMINIO" == *.* ]] || die "serve un nome completo, per esempio intercom.esempio.it"
[[ "$EMAIL" =~ ^[^[:space:]@]+@[^[:space:]@]+\.[^[:space:]@]+$ ]] || die "email non valida: $EMAIL"
command -v dnf >/dev/null || die "questo script è per Rocky/AlmaLinux (serve dnf)."

echo
echo "CHARLIE26 – installazione server per $DOMINIO"
echo

# --- 1. Il nome deve puntare a questo server -----------------------------------
echo "1/6 Controllo il DNS"
IP_DNS="$(getent ahostsv4 "$DOMINIO" | awk 'NR==1{print $1}' || true)"
[ -n "$IP_DNS" ] || die "il nome $DOMINIO non risolve ancora: crea il record DNS A e riprova tra qualche minuto."
if ! ip -4 -o addr show | awk '{print $4}' | cut -d/ -f1 | grep -qx "$IP_DNS"; then
  echo "  ⚠ $DOMINIO punta a $IP_DNS, che non è un indirizzo di questo server."
  echo "    Se il server sta dietro un NAT va bene; altrimenti correggi il record DNS."
  read -r -p "    Continuo comunque? [s/N] " r
  [[ "$r" =~ ^[sS]$ ]] || exit 1
fi
ok "$DOMINIO → $IP_DNS"

# --- 2. Password della regia ----------------------------------------------------
echo "2/6 Password della regia (con questa entri dalla console, da internet)"
if [ -r "$CONF/charlie26.env" ] && grep -q '^CHARLIE26_PASSWORD_REGIA=.\{12,\}' "$CONF/charlie26.env"; then
  ok "password già impostata (la tengo). Per cambiarla modifica $CONF/charlie26.env"
else
  while true; do
    read -r -s -p "    Scegli la password (almeno 12 caratteri): " P1; echo
    read -r -s -p "    Ripetila: " P2; echo
    [ "$P1" = "$P2" ] || { echo "    Le due password sono diverse."; continue; }
    [ "${#P1}" -ge 12 ] || { echo "    Troppo corta."; continue; }
    case "$P1" in *\'*|*\"*|*\\*|*\$*|*\`*|*' '*) echo "    Evita spazi e i simboli ' \" \\ \$ \`"; continue;; esac
    break
  done
  install -d -m 0750 "$CONF"
  ( umask 077; printf 'CHARLIE26_PASSWORD_REGIA=%s\n' "$P1" > "$CONF/charlie26.env" )
  chmod 600 "$CONF/charlie26.env"
  unset P1 P2
  ok "password salvata in $CONF/charlie26.env (leggibile solo da root)"
fi

# --- 3. Firewall -------------------------------------------------------------------
echo "3/6 Firewall"
dnf install -y -q firewalld >/dev/null
systemctl enable --now firewalld >/dev/null
for p in ssh; do firewall-cmd --permanent --add-service="$p" >/dev/null; done
for p in 80/tcp 443/tcp 443/udp; do firewall-cmd --permanent --add-port="$p" >/dev/null; done
firewall-cmd --reload >/dev/null
ok "aperte: SSH, 80/tcp (certificato), 443/tcp (pagine), 443/udp (audio e video)"

# --- 4. Programma ------------------------------------------------------------------
echo "4/6 Programma"
id charlie26 >/dev/null 2>&1 || useradd --system --home-dir "$DATI" --shell /sbin/nologin charlie26
install -d -m 0750 -o charlie26 -g charlie26 "$DATI" "$DATI/certificati"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
if [ -n "$BINARIO_LOCALE" ]; then
  [ -f "$BINARIO_LOCALE" ] || die "file non trovato: $BINARIO_LOCALE"
  cp "$BINARIO_LOCALE" "$TMP/charlie26-linux-amd64"
  ok "uso il file indicato"
else
  BASE="https://github.com/$REPO/releases/latest/download"
  curl -fsSL -o "$TMP/charlie26-linux-amd64" "$BASE/charlie26-linux-amd64" \
    || die "scarico non riuscito: la versione per Linux non è ancora pubblicata su GitHub. Passa il file come terzo argomento."
  curl -fsSL -o "$TMP/SHA256SUMS.txt" "$BASE/SHA256SUMS.txt" || die "manca SHA256SUMS.txt nella release."
  ( cd "$TMP" && grep ' charlie26-linux-amd64$' SHA256SUMS.txt | sha256sum -c - >/dev/null ) \
    || die "l'impronta del programma scaricato non coincide: non lo installo."
  ok "scaricato e verificato (SHA-256)"
fi
install -m 0755 -o root -g root "$TMP/charlie26-linux-amd64" "$BIN"
restorecon "$BIN" 2>/dev/null || true
"$BIN" -h >/dev/null 2>&1 || true

# --- 5. Certificato HTTPS ----------------------------------------------------------
echo "5/6 Certificato HTTPS (Let's Encrypt)"
dnf install -y -q epel-release >/dev/null
dnf install -y -q certbot >/dev/null
HOOK=/etc/letsencrypt/renewal-hooks/deploy/charlie26.sh
install -d /etc/letsencrypt/renewal-hooks/deploy
cat > "$HOOK" <<EOF
#!/usr/bin/env bash
# Dopo ogni rinnovo copia il certificato dove lo legge CHARLIE26 (che lo ricarica da solo).
set -e
L=/etc/letsencrypt/live/$DOMINIO
install -m 0644 -o charlie26 -g charlie26 "\$L/fullchain.pem" $DATI/certificati/dominio.crt
install -m 0600 -o charlie26 -g charlie26 "\$L/privkey.pem" $DATI/certificati/dominio.key
EOF
chmod 755 "$HOOK"
if [ ! -d "/etc/letsencrypt/live/$DOMINIO" ]; then
  certbot certonly --standalone -d "$DOMINIO" -m "$EMAIL" --agree-tos --no-eff-email -n \
    || die "certbot non è riuscito: controlla che il DNS punti qui e che la porta 80 sia raggiungibile dall'esterno."
fi
bash "$HOOK"
if systemctl list-unit-files 2>/dev/null | grep -q '^certbot-renew.timer'; then
  systemctl enable --now certbot-renew.timer >/dev/null
else
  echo '17 3,15 * * * root certbot renew -q' > /etc/cron.d/certbot-charlie26
fi
ok "certificato installato, rinnovo automatico attivo"

# --- 6. Servizio -------------------------------------------------------------------
echo "6/6 Servizio"
cat > /etc/systemd/system/charlie26.service <<EOF
[Unit]
Description=CHARLIE26 – Intercom Evoluto (server)
After=network-online.target
Wants=network-online.target

[Service]
User=charlie26
Group=charlie26
EnvironmentFile=$CONF/charlie26.env
ExecStart=$BIN -server -host-pubblico $DOMINIO -porta 443 -porta-udp 443 -dati $DATI -cert $DATI/certificati/dominio.crt -key $DATI/certificati/dominio.key
Restart=always
RestartSec=3
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=$DATI
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable charlie26 >/dev/null
systemctl restart charlie26
sleep 3
if systemctl is-active --quiet charlie26; then
  ok "servizio avviato"
else
  journalctl -u charlie26 -n 30 --no-pager || true
  die "il servizio non è partito: leggi le righe qui sopra."
fi

echo
echo "FATTO. Apri  https://$DOMINIO/regia  ed entra con la password della regia."
echo "I tecnici entrano da  https://$DOMINIO/  (o con i QR code della console)."
echo "Messaggi del programma:   journalctl -u charlie26 -f"
