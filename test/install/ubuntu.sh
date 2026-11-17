#!/usr/bin/env bash
# A clean Ubuntu 24.04 server with its own Docker daemon (privileged container), as an entity's
# node server. Installs the node from a kit directory with install.sh, non-interactively.
#
#   test/install/ubuntu.sh <kit-dir>            # DEN_INSTALL_PORTAL and DEN_INSTALL_TOKEN set
#   test/install/ubuntu.sh --exec <command...>  # run a command in the test server
#
# The entity's own API is the portal's test provider (den/mock-providers:dev), published on the
# test server's LAN address (port 18000), as an internal API reachable by IP.
set -euo pipefail
NAME=${DEN_TEST_SERVER:-den-install-test}
export MSYS_NO_PATHCONV=1

if [[ ${1:-} == --exec ]]; then
  shift
  exec docker exec -i "$NAME" bash -lc "$*"
fi

KIT=${1:?usage: ubuntu.sh <kit-dir>}
: "${DEN_INSTALL_PORTAL:?}" "${DEN_INSTALL_TOKEN:?}"

docker rm -f -v "$NAME" >/dev/null 2>&1 || true
if [[ ${DEN_TEST_DOCKER:-preinstalled} == install ]]; then
  # systemd as PID 1 and no Docker: install.sh installs Docker Engine itself.
  docker build -q -f "$(dirname "$0")/systemd.Dockerfile" -t den-test-ubuntu-systemd:24.04 "$(dirname "$0")" >/dev/null
  docker run -d --privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock     -v /var/lib/docker -v /var/lib/containerd --name "$NAME" den-test-ubuntu-systemd:24.04 >/dev/null
  docker exec "$NAME" bash -c 'for i in $(seq 60); do s=$(systemctl is-system-running 2>/dev/null); [[ $s == running || $s == degraded ]] && exit 0; sleep 1; done; exit 1' ||
    { echo "systemd did not boot"; exit 1; }
  echo "test server ready (systemd, no Docker)"
else
  # /var/lib/docker on a volume: the inner dockerd cannot stack overlayfs on the container root.
  docker run -d --privileged --name "$NAME" -v /var/lib/docker ubuntu:24.04 sleep infinity >/dev/null
  docker exec "$NAME" bash -c '
    set -e
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq docker.io docker-compose-v2 ca-certificates curl >/dev/null
    # cgroup v2 delegation, as docker:dind does, so the node'"'"'s memory and pids limits apply.
    if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
      mkdir -p /sys/fs/cgroup/init
      xargs -rn1 </sys/fs/cgroup/cgroup.procs >/sys/fs/cgroup/init/cgroup.procs 2>/dev/null || true
      sed -e "s/ / +/g" -e "s/^/+/" </sys/fs/cgroup/cgroup.controllers >/sys/fs/cgroup/cgroup.subtree_control
    fi
    (dockerd >/var/log/dockerd.log 2>&1 &)
    for i in $(seq 60); do docker info >/dev/null 2>&1 && exit 0; sleep 1; done
    echo "dockerd did not start"; tail -20 /var/log/dockerd.log; exit 1'
  echo "test server ready (Docker preinstalled)"
fi
docker cp "$KIT" "$NAME:/root/kit"

status=0
docker exec -e DEN_INSTALL_YES=1 -e DEN_INSTALL_MODE="${DEN_INSTALL_MODE:-canal}"   -e DEN_INSTALL_PORTAL -e DEN_INSTALL_TOKEN "$NAME" bash /root/kit/install.sh || status=$?

# The entity's own API, published on the test server's LAN address once Docker is there.
if docker exec "$NAME" docker info >/dev/null 2>&1; then
  docker save den/mock-providers:dev | docker exec -i "$NAME" docker load -q >/dev/null
  docker exec "$NAME" docker run -d --name api-propia --restart unless-stopped -p 18000:8000     -e PROVIDER=salud -e PORT=8000 den/mock-providers:dev >/dev/null
  LAN=$(docker exec "$NAME" bash -c "hostname -I | awk '{print \$1}'")
  echo "the entity's API is at http://$LAN:18000"
  echo "$LAN" >"$(dirname "$0")/.lan-address"
fi
exit "$status"
