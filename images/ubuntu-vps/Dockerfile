# LPS Linux VPS image: an Ubuntu userland with SSH, managed by lpsd.
# On Windows hosts this runs as a Linux container inside Docker Desktop's WSL2 VM.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      openssh-server sudo tini ca-certificates curl wget git nano vim-tiny less \
      iproute2 iputils-ping procps htop unzip locales tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && (userdel -r ubuntu 2>/dev/null || true) \
 && mkdir -p /run/sshd /etc/lps \
 && rm -f /etc/ssh/ssh_host_*

COPY entrypoint.sh /usr/local/sbin/lps-entrypoint
RUN chmod 0755 /usr/local/sbin/lps-entrypoint

LABEL org.opencontainers.image.title="lps-ubuntu-vps" \
      org.opencontainers.image.source="https://github.com/abrardw/lps"

EXPOSE 22
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/sbin/lps-entrypoint"]
