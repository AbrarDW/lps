#!/bin/sh
# lpsd copies /etc/lps/init.env into the container before its first start.
# It holds the SSH user, public key and (optional) password, base64-encoded.
set -eu

INIT=/etc/lps/init.env
if [ -f "$INIT" ]; then
  # shellcheck disable=SC1090
  . "$INIT"
  user="${LPS_USER:?LPS_USER missing}"
  if ! id "$user" >/dev/null 2>&1; then
    # Home lives on a persistent volume; reuse it if it already exists.
    if [ -d "/home/$user" ]; then
      useradd -d "/home/$user" -s /bin/bash "$user"
      chown "$user:$user" "/home/$user"
    else
      useradd -m -s /bin/bash "$user"
    fi
    usermod -aG sudo "$user"
  fi

  if [ -n "${LPS_PUBKEY_B64:-}" ]; then
    install -d -m 0700 -o "$user" -g "$user" "/home/$user/.ssh"
    echo "$LPS_PUBKEY_B64" | base64 -d > "/home/$user/.ssh/authorized_keys"
    chown "$user:$user" "/home/$user/.ssh/authorized_keys"
    chmod 0600 "/home/$user/.ssh/authorized_keys"
  fi

  pwauth=no
  if [ -n "${LPS_PASSWORD_B64:-}" ]; then
    printf '%s:%s\n' "$user" "$(echo "$LPS_PASSWORD_B64" | base64 -d)" | chpasswd
    pwauth=yes
    rm -f /etc/sudoers.d/lps-nopasswd
  else
    # Key-only account: no password exists, so allow passwordless sudo.
    passwd -d "$user" >/dev/null
    echo "$user ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/lps-nopasswd
    chmod 0440 /etc/sudoers.d/lps-nopasswd
  fi

  cat > /etc/ssh/sshd_config.d/lps.conf <<CONF
PermitRootLogin no
PasswordAuthentication $pwauth
KbdInteractiveAuthentication no
PubkeyAuthentication yes
AllowUsers $user
CONF
  # Never leave the plaintext password on disk.
  rm -f "$INIT"
fi

[ -f /etc/ssh/ssh_host_ed25519_key ] || ssh-keygen -A >/dev/null
mkdir -p /run/sshd
exec /usr/sbin/sshd -D -e
