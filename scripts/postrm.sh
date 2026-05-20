#!/bin/sh
set -e

# postrm — runs after files are removed. We only do destructive cleanup
# on `purge`, never on plain `remove` or `upgrade`. On purge:
#   1. Delete the rrsh user (without --remove: /usr/lib/rrsh/home is
#      package-owned and dpkg handles it; an operator-added
#      ~/.ssh/authorized_keys is left in place rather than silently
#      destroyed).
#   2. Drop our /etc/shells entry.
#   3. Defensively remove the sudoers file (dpkg handles conffile
#      removal on purge already, but RPM doesn't have the same machinery).
#   4. Remove the shipped example config and, if /etc/rrsh is otherwise
#      empty (no operator-authored rrsh.json), drop the directory too.

if [ "$1" = "purge" ] || [ "$1" = "0" ]; then
  if id rrsh >/dev/null 2>&1; then
    userdel rrsh 2>/dev/null || true
  fi
  if [ -f /etc/shells ]; then
    sed -i '\|^/usr/bin/rrsh$|d' /etc/shells || true
  fi
  rm -f /etc/sudoers.d/rrsh
  rm -f /etc/rrsh/rrsh.json.example
  rmdir /etc/rrsh 2>/dev/null || true
fi
