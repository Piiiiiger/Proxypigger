#!/bin/sh
# Install only. Does not enable a timer, change proxy logs, or install/authenticate Codex.
set -eu
if [ "${1:-}" = "--help" ]; then
    echo 'Usage: sudo sh install.sh'
    echo 'Installs the opt-in package and a disabled systemd timer on a selected machine.'
    exit 0
fi
if [ "$(id -u)" != 0 ]; then
    echo 'Run the installer as root. The patrol itself runs as an unprivileged account.' >&2
    exit 1
fi
command -v python3 >/dev/null
python3 -c 'import sys; assert sys.version_info >= (3, 10), "Python 3.10+ required"'
command -v systemctl >/dev/null || {
    echo 'For OpenRC/cron deployment, follow ai-abuse-patrol/references/deployment.md.' >&2
    exit 1
}
command -v useradd >/dev/null
patrol_source=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if ! id ai-abuse-patrol >/dev/null 2>&1; then
    useradd --system --user-group --create-home --home-dir /var/lib/ai-abuse-patrol --shell /usr/sbin/nologin ai-abuse-patrol
fi
# Refuse to adopt an unrelated account with different state/auth locations.
patrol_home=$(getent passwd ai-abuse-patrol | cut -d: -f6)
[ "$patrol_home" = /var/lib/ai-abuse-patrol ] || {
    echo 'Existing ai-abuse-patrol user has a different home; inspect it before installing.' >&2
    exit 1
}
install -d -m 0755 /opt/ai-abuse-patrol /opt/ai-abuse-patrol/ai-abuse-patrol
for dir in agents references scripts; do
    install -d -m 0755 "/opt/ai-abuse-patrol/ai-abuse-patrol/$dir"
done
install -m 0644 "$patrol_source/ai-abuse-patrol/SKILL.md" /opt/ai-abuse-patrol/ai-abuse-patrol/SKILL.md
install -m 0644 "$patrol_source/ai-abuse-patrol/agents/openai.yaml" /opt/ai-abuse-patrol/ai-abuse-patrol/agents/openai.yaml
install -m 0644 "$patrol_source/ai-abuse-patrol/references/deployment.md" /opt/ai-abuse-patrol/ai-abuse-patrol/references/deployment.md
install -m 0755 "$patrol_source/ai-abuse-patrol/scripts/patrol.py" /opt/ai-abuse-patrol/ai-abuse-patrol/scripts/patrol.py
install -m 0755 "$patrol_source/ai-abuse-patrol/scripts/export.py" /opt/ai-abuse-patrol/ai-abuse-patrol/scripts/export.py
install -d -m 0750 -o root -g ai-abuse-patrol /etc/ai-abuse-patrol
if [ ! -e /etc/ai-abuse-patrol/config.json ]; then
    install -m 0640 -o root -g ai-abuse-patrol "$patrol_source/config.example.json" /etc/ai-abuse-patrol/config.json
fi
install -d -m 0700 -o ai-abuse-patrol -g ai-abuse-patrol /var/lib/ai-abuse-patrol
install -d -m 0755 -o root -g root /var/lib/ai-abuse-patrol/.agents /var/lib/ai-abuse-patrol/.agents/skills
ln -sfn /opt/ai-abuse-patrol/ai-abuse-patrol /var/lib/ai-abuse-patrol/.agents/skills/ai-abuse-patrol
install -m 0644 "$patrol_source/systemd/ai-abuse-patrol.service" /etc/systemd/system/ai-abuse-patrol.service
install -m 0644 "$patrol_source/systemd/ai-abuse-patrol.timer" /etc/systemd/system/ai-abuse-patrol.timer
systemctl daemon-reload
printf '%s\n' 'Installed. No new timer was enabled; existing timers keep their prior state.' \
  'Next: configure sources/model, grant read-only log access, authenticate Codex as ai-abuse-patrol, and run --collect-only.' \
  'See /opt/ai-abuse-patrol/ai-abuse-patrol/references/deployment.md'
