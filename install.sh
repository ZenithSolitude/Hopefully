#!/bin/bash
# Hopefully — установщик для Ubuntu 22.04
# curl -fsSL https://github.com/ZenithSolitude/Hopefully/releases/latest/download/install.sh | sudo bash
set -euo pipefail

# ── Цвета ─────────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; NC='\033[0m'

ok()   { echo -e "${GREEN}[OK]${NC} $*"; }
info() { echo -e "${CYAN}[i]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
die()  { echo -e "${RED}[ERR]${NC} $*"; exit 1; }
step() { echo -e "\n${BOLD}${CYAN}=== $* ===${NC}"; }

cat << 'BANNER'

  🌱 Hopefully — установщик
     https://github.com/ZenithSolitude/Hopefully

BANNER

# ── Проверки ──────────────────────────────────────────────────────────────────
[[ $EUID -ne 0 ]] && die "Запустите от root: sudo bash install.sh"

ARCH=$(uname -m)
case "$ARCH" in
  x86_64)  GOARCH=amd64 ;;
  aarch64) GOARCH=arm64 ;;
  *) die "Архитектура $ARCH не поддерживается" ;;
esac
ok "Архитектура: $ARCH"

# Проверка ОС
if [[ -f /etc/os-release ]]; then
  . /etc/os-release
  info "ОС: $PRETTY_NAME"
fi

RAM_MB=$(awk '/MemTotal/{print int($2/1024)}' /proc/meminfo)
DISK_GB=$(df / --output=avail -BG | tail -1 | tr -d 'G ')
ok "RAM: ${RAM_MB}MB | Диск: ${DISK_GB}GB свободно"
[[ $RAM_MB -lt 256 ]] && warn "Мало RAM — рекомендуется минимум 256MB"

# ── Переменные ────────────────────────────────────────────────────────────────
BIN="/usr/local/bin/hopefully"
DATA_DIR="/var/lib/hopefully"
SERVICE="/etc/systemd/system/hopefully.service"
REPO="ZenithSolitude/Hopefully"
GO_VERSION="1.22.4"

# Определяем порт (80 если свободен, иначе 8080)
HTTP_PORT=8080
if ! ss -tlnp 2>/dev/null | grep -q ':80 '; then
  HTTP_PORT=80
fi
info "HTTP порт: $HTTP_PORT"

# Внешний IP
EXT_IP=$(curl -4 -fsSL --connect-timeout 5 https://ifconfig.me 2>/dev/null || \
         curl -4 -fsSL --connect-timeout 5 https://api.ipify.org 2>/dev/null || echo "")

# SECRET_KEY — сохраняем если уже есть
ENV_FILE="${DATA_DIR}/.env"
if [[ -f "$ENV_FILE" ]] && grep -q "^SECRET_KEY=" "$ENV_FILE"; then
  SECRET_KEY=$(grep "^SECRET_KEY=" "$ENV_FILE" | cut -d= -f2-)
  info "Существующий SECRET_KEY сохранён"
else
  SECRET_KEY=$(openssl rand -hex 32)
  info "Сгенерирован новый SECRET_KEY"
fi

# ── Зависимости ───────────────────────────────────────────────────────────────
step "Системные зависимости"
apt-get update -qq
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
  curl wget git openssl ufw ca-certificates \
  build-essential gcc
ok "Зависимости установлены"

# ── Бинарник ──────────────────────────────────────────────────────────────────
step "Загрузка бинарника Hopefully"

BIN_URL="https://github.com/${REPO}/releases/latest/download/hopefully-linux-${GOARCH}"
info "Пробуем: $BIN_URL"
TMP_BIN=$(mktemp /tmp/hopefully_XXXXXX)

if curl -fsSL --connect-timeout 15 --retry 2 "$BIN_URL" -o "$TMP_BIN" 2>/dev/null \
   && [[ -s "$TMP_BIN" ]] \
   && file "$TMP_BIN" 2>/dev/null | grep -q "ELF"; then
  ok "Бинарник скачан из GitHub Releases"
else
  # ── Fallback: собираем из исходников ──────────────────────────────────────
  rm -f "$TMP_BIN"
  warn "Готовый бинарник недоступен. Собираем из исходников..."
  warn "Это займёт 3-7 минут при первом запуске"

  # Устанавливаем Go
  step "Установка Go ${GO_VERSION}"
  GO_TAR="go${GO_VERSION}.linux-${GOARCH}.tar.gz"
  GO_URL="https://go.dev/dl/${GO_TAR}"

  if command -v go &>/dev/null && go version | grep -q "go${GO_VERSION}"; then
    ok "Go ${GO_VERSION} уже установлен"
    export PATH=$PATH:/usr/local/go/bin
  else
    info "Загружаем Go ${GO_VERSION}..."
    curl -fsSL --progress-bar "$GO_URL" -o "/tmp/${GO_TAR}" \
      || die "Не удалось скачать Go с $GO_URL"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "/tmp/${GO_TAR}"
    rm "/tmp/${GO_TAR}"
    export PATH=$PATH:/usr/local/go/bin
    ok "Go $(go version) установлен"
  fi

  # Клонируем и собираем
  step "Сборка Hopefully"
  BUILD_DIR=$(mktemp -d /tmp/hopefully_build_XXXXXX)
  info "Клонирование репозитория..."
  git clone --depth=1 "https://github.com/${REPO}.git" "$BUILD_DIR" \
    || die "Не удалось клонировать репозиторий"

  cd "$BUILD_DIR"
  info "Загрузка зависимостей Go (go mod download)..."
  CGO_ENABLED=1 /usr/local/go/bin/go mod download \
    || die "go mod download завершился с ошибкой"

  info "Компиляция..."
  TMP_BIN=$(mktemp /tmp/hopefully_XXXXXX)
  CGO_ENABLED=1 /usr/local/go/bin/go build \
    -ldflags="-s -w" \
    -o "$TMP_BIN" \
    ./cmd/server \
    || die "Компиляция завершилась с ошибкой"

  cd /
  rm -rf "$BUILD_DIR"
  ok "Бинарник собран из исходников"
fi

# Проверяем бинарник
chmod +x "$TMP_BIN"
if ! "$TMP_BIN" --help &>/dev/null && ! file "$TMP_BIN" | grep -q "ELF"; then
  die "Бинарник повреждён или не является ELF-файлом"
fi
mv "$TMP_BIN" "$BIN"
ok "Установлен: $BIN ($(du -sh $BIN | cut -f1))"

# ── Директории и конфигурация ─────────────────────────────────────────────────
step "Конфигурация"
mkdir -p "$DATA_DIR" "${DATA_DIR}/logs" "${DATA_DIR}/modules" "${DATA_DIR}/module_data"

if [[ ! -f "$ENV_FILE" ]]; then
  cat > "$ENV_FILE" << ENVEOF
# Hopefully — конфигурация
# Создан: $(date '+%Y-%m-%d %H:%M:%S')

SECRET_KEY=${SECRET_KEY}
PORT=${HTTP_PORT}
DATA_DIR=${DATA_DIR}
ENVEOF
  chmod 600 "$ENV_FILE"
  ok ".env создан: $ENV_FILE"
else
  # Обновляем PORT если изменился
  sed -i "s|^PORT=.*|PORT=${HTTP_PORT}|" "$ENV_FILE" 2>/dev/null || true
  warn ".env уже существует — сохраняем настройки"
fi

# ── Systemd ───────────────────────────────────────────────────────────────────
step "Systemd сервис"

# Если сервис запущен — останавливаем для обновления
systemctl stop hopefully 2>/dev/null || true

cat > "$SERVICE" << SVCEOF
[Unit]
Description=Hopefully Modular Portal
Documentation=https://github.com/${REPO}
After=network.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=${DATA_DIR}/.env
ExecStart=${BIN} -port \${PORT} -data \${DATA_DIR} -secret \${SECRET_KEY}
Restart=on-failure
RestartSec=5s
TimeoutStopSec=15s

# Логи пишутся самим приложением в DATA_DIR/logs/
# Для просмотра через journalctl:
StandardOutput=journal
StandardError=journal
SyslogIdentifier=hopefully

[Install]
WantedBy=multi-user.target
SVCEOF

systemctl daemon-reload
systemctl enable hopefully
systemctl start hopefully

# Ждём запуска
for i in $(seq 1 10); do
  sleep 1
  if systemctl is-active --quiet hopefully; then
    ok "Сервис запущен (${i}с)"
    break
  fi
  [[ $i -eq 10 ]] && {
    warn "Сервис не запустился за 10с. Диагностика:"
    journalctl -u hopefully -n 20 --no-pager 2>/dev/null || true
  }
done

# ── Firewall ──────────────────────────────────────────────────────────────────
step "Firewall (UFW)"

ufw --force reset &>/dev/null
ufw default deny incoming  &>/dev/null
ufw default allow outgoing &>/dev/null
ufw allow 22/tcp   comment "SSH"    &>/dev/null
ufw allow "${HTTP_PORT}/tcp" comment "Hopefully HTTP" &>/dev/null
[[ "$HTTP_PORT" != "443" ]] && ufw allow 443/tcp comment "HTTPS" &>/dev/null || true
ufw --force enable &>/dev/null
ok "UFW активен. Правила:"
ufw status numbered 2>/dev/null | grep -E "ALLOW|DENY" | head -10 || true

# ── Утилита hf ────────────────────────────────────────────────────────────────
step "Утилита hf"

cat > /usr/local/bin/hf << 'HFEOF'
#!/bin/bash
# hf — управление Hopefully
REPO="ZenithSolitude/Hopefully"

case "${1:-help}" in
  start)
    systemctl start hopefully && echo "Started" ;;
  stop)
    systemctl stop hopefully && echo "Stopped" ;;
  restart)
    systemctl restart hopefully && echo "Restarted" ;;
  status)
    systemctl status hopefully ;;
  logs)
    journalctl -u hopefully -f --output=short-iso ;;
  update)
    echo "Обновление Hopefully..."
    ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
    URL="https://github.com/${REPO}/releases/latest/download/hopefully-linux-${ARCH}"
    TMP=$(mktemp /tmp/hopefully_update_XXXXXX)
    if curl -fsSL "$URL" -o "$TMP" && [[ -s "$TMP" ]]; then
      chmod +x "$TMP"
      systemctl stop hopefully
      cp /usr/local/bin/hopefully /usr/local/bin/hopefully.bak
      mv "$TMP" /usr/local/bin/hopefully
      systemctl start hopefully
      echo "Обновлено! Предыдущая версия: /usr/local/bin/hopefully.bak"
    else
      echo "Не удалось скачать обновление"
      rm -f "$TMP"
      exit 1
    fi
    ;;
  version)
    /usr/local/bin/hopefully -version 2>/dev/null || echo "Hopefully (version unknown)" ;;
  help|*)
    echo ""
    echo "  hf — управление Hopefully"
    echo ""
    echo "  Использование: hf <команда>"
    echo ""
    echo "  Команды:"
    echo "    start    — запустить"
    echo "    stop     — остановить"
    echo "    restart  — перезапустить"
    echo "    status   — статус сервиса"
    echo "    logs     — логи в реальном времени"
    echo "    update   — обновить до последней версии"
    echo "    version  — версия"
    echo ""
    ;;
esac
HFEOF

chmod +x /usr/local/bin/hf
ok "Команда 'hf' доступна"

# ── Итоговая информация ───────────────────────────────────────────────────────
DISPLAY_HOST="${EXT_IP:-localhost}"
[[ "$HTTP_PORT" == "80" ]] && URL="http://${DISPLAY_HOST}" || URL="http://${DISPLAY_HOST}:${HTTP_PORT}"

echo ""
echo -e "${BOLD}${GREEN}╔══════════════════════════════════════════════╗${NC}"
echo -e "${BOLD}${GREEN}║   🌱 Hopefully успешно установлен!           ║${NC}"
echo -e "${BOLD}${GREEN}╚══════════════════════════════════════════════╝${NC}"
echo ""
echo -e "  ${BOLD}Веб-интерфейс:${NC}  ${CYAN}${URL}${NC}"
echo -e "  ${BOLD}Логин:${NC}          admin"
echo -e "  ${BOLD}Пароль:${NC}         admin  ${RED}← СМЕНИТЕ НЕМЕДЛЕННО!${NC}"
echo ""
echo -e "  ${BOLD}Управление:${NC}"
echo -e "    ${CYAN}hf start${NC}    — запустить"
echo -e "    ${CYAN}hf stop${NC}     — остановить"
echo -e "    ${CYAN}hf restart${NC}  — перезапустить"
echo -e "    ${CYAN}hf logs${NC}     — логи в реальном времени"
echo -e "    ${CYAN}hf update${NC}   — обновить до последней версии"
echo ""
echo -e "  ${BOLD}Конфигурация:${NC}  ${DATA_DIR}/.env"
echo -e "  ${BOLD}Логи:${NC}          journalctl -u hopefully -f"
echo ""
