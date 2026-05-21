#!/bin/sh
set -e

# postinst - runs after files are extracted on .deb/.rpm install or
# upgrade. We:
#   1. Add /usr/bin/rrsh to /etc/shells so chsh accepts it.
#   2. Create the 'rrsh' system-style user with rrsh as its login shell
#      and /var/lib/rrsh as its home directory. The home dir is
#      package-owned (ships with an empty .hushlogin so sshd doesn't
#      prepend its motd/last-login banner to the JSON-RPC stdout).
#      The operator drops their authorized_keys under it.
#   3. Lock the password (this account is SSH-key-only).
#   4. Validate the freshly-installed sudoers snippet with visudo.

RRSH_HOME=/var/lib/rrsh

if [ "$1" = "configure" ] || [ "$1" -ge 1 ]; then
  # 1. Register the shell.
  if [ -f /etc/shells ] && ! grep -qxF /usr/bin/rrsh /etc/shells; then
    echo /usr/bin/rrsh >> /etc/shells
  fi

  # 2./3. Create the user, or repair its shell/home if it already exists.
  # --no-create-home: the package shipped $RRSH_HOME already, owned by
  # root. We don't want useradd to copy /etc/skel into it.
  if ! id rrsh >/dev/null 2>&1; then
    useradd --system --home-dir "$RRSH_HOME" --no-create-home \
            --shell /usr/bin/rrsh \
            --comment "rrsh restricted shell user" rrsh
    passwd --lock rrsh >/dev/null 2>&1 || true
  else
    usermod --home "$RRSH_HOME" --shell /usr/bin/rrsh rrsh || true
  fi

  # 4. Validate the sudoers snippet. If visudo rejects it, remove it
  # rather than leaving a broken sudoers state on the host.
  if [ -f /etc/sudoers.d/rrsh ]; then
    chmod 0440 /etc/sudoers.d/rrsh || true
    chown root:root /etc/sudoers.d/rrsh || true
    if command -v visudo >/dev/null 2>&1; then
      if ! visudo -cf /etc/sudoers.d/rrsh >/dev/null 2>&1; then
        echo "rrsh: warning: /etc/sudoers.d/rrsh failed visudo validation; removing it" >&2
        rm -f /etc/sudoers.d/rrsh
      fi
    fi
  fi
fi
