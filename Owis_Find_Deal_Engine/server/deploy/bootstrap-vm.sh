#!/usr/bin/env bash
# One-time setup of the production VM (Oracle, Ubuntu 24.04, Arm). Safe to
# run again: every part checks first and only changes what is missing.
#
#   curl -fsSL https://raw.githubusercontent.com/owisdev/claude-ai-clould-repo/main/Owis_Find_Deal_Engine/server/deploy/bootstrap-vm.sh -o bootstrap-vm.sh
#   less bootstrap-vm.sh          # read it first
#   sudo bash bootstrap-vm.sh
#
# What it does:
#   1. installs updates, git, Docker Engine + compose (Docker's own apt repo)
#   2. turns on automatic security updates (unattended-upgrades)
#   3. SSH: key login only, no root login, no passwords
#   4. Docker log rotation; lets the login user run docker
#   5. clones the repository to /opt/ofd/repo and makes the server's .env
#      (from .env.example, readable by the login user only) to fill in
#   6. shows the open ports (only SSH should listen to the outside)
set -euo pipefail

REPO_URL=${OFD_REPO_URL:-https://github.com/owisdev/claude-ai-clould-repo.git}
BASE=/opt/ofd
SERVER_DIR=$BASE/repo/Owis_Find_Deal_Engine/server

say() { printf '\n\033[1m== %s\033[0m\n' "$*"; }

[ "$(id -u)" -eq 0 ] || { echo "Run with sudo: sudo bash $0"; exit 1; }
OWNER=${SUDO_USER:-ubuntu}
id "$OWNER" >/dev/null 2>&1 || { echo "User $OWNER not found"; exit 1; }
. /etc/os-release
[ "$ID" = ubuntu ] || { echo "Made for Ubuntu (found $ID)"; exit 1; }
echo "Ubuntu $VERSION_ID on $(uname -m), login user: $OWNER"

say "1/6 Updates, git and Docker"
export DEBIAN_FRONTEND=noninteractive
apt-get update -q
apt-get -o Dpkg::Options::=--force-confold upgrade -y -q
apt-get install -y -q ca-certificates curl git unattended-upgrades
if ! command -v docker >/dev/null || ! docker compose version >/dev/null 2>&1; then
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu ${UBUNTU_CODENAME:-$VERSION_CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -q
  apt-get install -y -q docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
fi
docker --version
docker compose version

say "2/6 Automatic security updates"
cat > /etc/apt/apt.conf.d/20auto-upgrades <<'EOF'
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "1";
APT::Periodic::AutocleanInterval "7";
EOF
systemctl enable --now unattended-upgrades >/dev/null
echo "unattended-upgrades: $(systemctl is-active unattended-upgrades)"
echo "(A reboot is sometimes needed for kernel updates: see /var/run/reboot-required.)"

say "3/6 SSH: keys only"
cat > /etc/ssh/sshd_config.d/99-ofd.conf <<'EOF'
# owis_find_deal_engine (deploy/bootstrap-vm.sh): key login only.
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
EOF
if sshd -t; then
  # Ubuntu 24.04 starts sshd per connection (ssh.socket): new logins read
  # the new settings anyway; a running service is reloaded.
  systemctl try-reload-or-restart ssh.service 2>/dev/null || true
  sshd -T | grep -E '^(passwordauthentication|kbdinteractiveauthentication|permitrootlogin|pubkeyauthentication) '
else
  rm -f /etc/ssh/sshd_config.d/99-ofd.conf
  echo "SSH check failed: nothing changed"; exit 1
fi

say "4/6 Docker settings"
if [ ! -f /etc/docker/daemon.json ]; then
  cat > /etc/docker/daemon.json <<'EOF'
{
  "log-driver": "json-file",
  "log-opts": { "max-size": "10m", "max-file": "3" },
  "live-restore": true
}
EOF
  systemctl restart docker
fi
systemctl enable --now docker >/dev/null
if ! id -nG "$OWNER" | grep -qw docker; then
  usermod -aG docker "$OWNER"
  echo "$OWNER may now run docker: log out and in again (exit, then ssh again)."
fi

say "5/6 Repository and .env"
install -d -o "$OWNER" -g "$OWNER" "$BASE"
if [ ! -d "$BASE/repo/.git" ]; then
  sudo -u "$OWNER" git clone -q --branch main "$REPO_URL" "$BASE/repo"
fi
if [ ! -d "$SERVER_DIR" ]; then
  sudo -u "$OWNER" git -C "$BASE/repo" pull -q --ff-only origin main ||
    { echo "Cannot update $BASE/repo"; exit 1; }
fi
[ -d "$SERVER_DIR" ] || { echo "main has no Owis_Find_Deal_Engine/server yet: merge it into main first, then run this again"; exit 1; }
sudo -u "$OWNER" git -C "$BASE/repo" log --oneline -1
if [ ! -f "$SERVER_DIR/.env" ]; then
  install -m 600 -o "$OWNER" -g "$OWNER" "$SERVER_DIR/.env.example" "$SERVER_DIR/.env"
  echo "Made $SERVER_DIR/.env (mode 600): fill it in before the first deploy."
else
  chmod 600 "$SERVER_DIR/.env"
  echo "$SERVER_DIR/.env already there (kept)."
fi

say "6/6 Listening ports"
# Only sshd (22) should listen on all addresses; Docker publishes the API
# on 127.0.0.1:3002 only. Oracle's own firewall (security list) should
# also allow only port 22 in.
ss -tlnH | awk '{print $4}' | sort -u

say "Done"
echo "Next: log out and in again, fill in $SERVER_DIR/.env, then run"
echo "  $SERVER_DIR/deploy/deploy.sh"
