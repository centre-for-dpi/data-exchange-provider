# A minimal Ubuntu Server 24.04 for tests: systemd as PID 1, curl, no Docker. install.sh then
# walks its real Docker Engine path (download.docker.com, apt, systemctl enable --now docker).
FROM ubuntu:24.04
RUN apt-get update -qq \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends \
      systemd systemd-sysv dbus curl ca-certificates iproute2 sudo gnupg >/dev/null \
 && apt-get clean && rm -rf /var/lib/apt/lists/* \
 && systemctl mask systemd-logind.service getty.target console-getty.service
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
