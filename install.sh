#!/bin/sh
# OSS Railway installer. Usage: curl -fsSL <url> | sh
# Re-running upgrades the panel and keeps data.
set -eu

IMAGE="${OSSR_IMAGE:-ghcr.io/kumarroushan9234/oss-railway:latest}"

say() { printf '==> %s\n' "$1"; }
warn() { printf 'warning: %s\n' "$1" >&2; }
die() { printf 'error: %s\n' "$1" >&2; exit 1; }
has() { command -v "$1" >/dev/null 2>&1; }

# --- Preflight -------------------------------------------------------------
say "Checking system"
[ "$(uname -s)" = "Linux" ] || die "Linux is required"
[ "$(id -u)" = "0" ] || die "run as root (try: curl -fsSL <url> | sudo sh)"

case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "unsupported architecture: $(uname -m) (need amd64 or arm64)" ;;
esac

OS_ID=unknown
OS_LIKE=""
if [ -r /etc/os-release ]; then
  # shellcheck disable=SC1091
  OS_ID=$(. /etc/os-release && printf '%s' "${ID:-unknown}")
  # shellcheck disable=SC1091
  OS_LIKE=$(. /etc/os-release && printf '%s' "${ID_LIKE:-}")
fi
case "$OS_ID $OS_LIKE" in
  *ubuntu* | *debian* | *rhel* | *centos* | *fedora* | *rocky* | *almalinux*) ;;
  *) warn "untested distribution '$OS_ID', continuing" ;;
esac
say "System: $OS_ID $ARCH"

if ! has curl; then die "curl is required"; fi

MEM_KB=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
if [ "$MEM_KB" -lt 1000000 ]; then
  warn "less than 1 GB RAM; builds may be slow or fail"
fi

DISK_KB=$(df -Pk / | awk 'NR==2 {print $4}')
if [ "$DISK_KB" -lt 10485760 ]; then
  die "at least 10 GB free disk on / is required"
fi

# --- Docker ----------------------------------------------------------------
if has docker; then
  say "Docker already installed"
else
  say "Installing Docker"
  curl -fsSL https://get.docker.com | sh
  systemctl enable --now docker
fi
docker info >/dev/null 2>&1 || die "Docker is installed but not running"
docker buildx version >/dev/null 2>&1 || die "docker buildx is missing (install the docker-buildx-plugin package)"

# --- Swap ------------------------------------------------------------------
SWAP_KB=$(awk '/^SwapTotal:/ {print $2}' /proc/meminfo)
if [ "$MEM_KB" -lt 2000000 ] && [ "$SWAP_KB" -eq 0 ]; then
  say "Adding 2 GB swap at /swapfile"
  if [ ! -f /swapfile ]; then
    fallocate -l 2G /swapfile || dd if=/dev/zero of=/swapfile bs=1M count=2048
    chmod 600 /swapfile
    mkswap /swapfile >/dev/null
  fi
  swapon /swapfile
  if ! grep -q '^/swapfile[[:space:]]' /etc/fstab; then
    printf '/swapfile none swap sw 0 0\n' >>/etc/fstab
  fi
fi

# --- Firewall --------------------------------------------------------------
# Only add rules to a firewall that is already on. Turning one on here could block
# services already running on this server (an API on 8710, a database, ...).
if has ufw && ufw status 2>/dev/null | grep -q '^Status: active'; then
  say "Adding ufw rules (SSH, 80, 443)"
  SSH_PORTS="22"
  if [ -n "${SSH_CONNECTION:-}" ]; then
    SSH_PORTS="$SSH_PORTS $(printf '%s' "$SSH_CONNECTION" | awk '{print $4}')"
  fi
  if has sshd; then
    SSH_PORTS="$SSH_PORTS $(sshd -T 2>/dev/null | awk '$1 == "port" {print $2}' || true)"
  fi
  for p in $SSH_PORTS; do
    case "$p" in
      '' | *[!0-9]*) ;;
      *) ufw allow "$p/tcp" >/dev/null ;;
    esac
  done
  ufw allow 80/tcp >/dev/null
  ufw allow 443/tcp >/dev/null
elif has firewall-cmd && firewall-cmd --state >/dev/null 2>&1; then
  say "Configuring firewalld (http, https)"
  firewall-cmd --permanent --add-service=ssh >/dev/null
  firewall-cmd --permanent --add-service=http >/dev/null
  firewall-cmd --permanent --add-service=https >/dev/null
  firewall-cmd --reload >/dev/null
else
  say "No active firewall found, leaving the firewall alone (open ports 80 and 443 yourself if your provider blocks them)"
fi

# --- Public IP -------------------------------------------------------------
IP=$(curl -fsS4 --max-time 10 https://api.ipify.org 2>/dev/null || true)
if [ -z "$IP" ]; then
  IP=$(hostname -I 2>/dev/null | awk '{print $1}')
fi
[ -n "$IP" ] || die "could not detect the public IP"
say "Public IP: $IP"

# --- Panel -----------------------------------------------------------------
say "Preparing network and volume"
docker network inspect ossr >/dev/null 2>&1 || docker network create ossr >/dev/null
docker volume inspect ossr-data >/dev/null 2>&1 || docker volume create ossr-data >/dev/null

say "Pulling $IMAGE"
if ! docker pull "$IMAGE" >/dev/null 2>&1; then
  docker image inspect "$IMAGE" >/dev/null 2>&1 || die "could not pull $IMAGE"
  say "Using the local image $IMAGE"
fi

say "Starting panel"
docker rm -f ossr-panel >/dev/null 2>&1 || true
docker run -d --name ossr-panel --restart unless-stopped --network ossr \
  -v /var/run/docker.sock:/var/run/docker.sock -v ossr-data:/data \
  -v /proc:/host/proc:ro -v /:/host/root:ro --cap-add SYS_PTRACE --add-host host.docker.internal:host-gateway \
  -e OSSR_PUBLIC_IP="$IP" "$IMAGE" >/dev/null

say "Waiting for panel"
CODE=""
CLAIMED=""
i=0
while [ "$i" -lt 60 ]; do
  if CODE=$(docker exec ossr-panel cat /data/setup-code 2>/dev/null) && [ -n "$CODE" ]; then
    break
  fi
  CODE=""
  # Re-running the installer to update: the account already exists, so there is no code.
  if docker exec ossr-panel wget -qO- http://localhost:3000/api/me 2>/dev/null | grep -q '"needs_setup":false'; then
    CLAIMED=1
    break
  fi
  i=$((i + 1))
  sleep 1
done

if [ -z "$CODE" ] && [ -z "$CLAIMED" ]; then
  if [ "$(docker inspect -f '{{.State.Running}}' ossr-panel 2>/dev/null)" != "true" ]; then
    die "panel did not start; see: docker logs ossr-panel"
  fi
  die "the panel started but did not report ready; see: docker logs ossr-panel"
fi

HOST="$(printf '%s' "$IP" | tr '.' '-').sslip.io"
if [ -n "$CLAIMED" ]; then
  printf '\nOSS Railway was updated and is running. Your account, services and data are unchanged.\n\n  Open:  https://%s\n         (or http://%s if HTTPS is not ready yet)\n\n' "$HOST" "$IP"
else
  printf '\nOSS Railway is running.\n\n  Open:        https://%s\n               (or http://%s if HTTPS is not ready yet)\n  Setup code:  %s\n\n' "$HOST" "$IP" "$CODE"
fi
