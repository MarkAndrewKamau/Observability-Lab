#!/usr/bin/env bash
# Tune host fs.inotify limits for the kind cluster.
#
# Root cause: kube-proxy on the kind nodes crash-looped with
# "too many open files" (EMFILE). It was NOT fd exhaustion -- the pod's
# RLIMIT_NOFILE was already ~2B. kube-proxy creates an inotify instance at
# startup, and inotify_init(1) failed with EMFILE because the host's root user
# had exhausted fs.inotify.max_user_instances (default 128; this box uses 145+
# across the kind nodes' kubelet/containerd/systemd plus the app stack).
# With kube-proxy down, ClusterIP/DNS broke on that node, which wedged the
# wazuh dashboard ("getaddrinfo EBUSY wazuh-indexer") and made other pods
# crash-loop -- easily mistaken for an OOM.
#
# Run with sudo after a fresh host boot, or rely on the persisted
# /etc/sysctl.d/90-obs-lab-inotify.conf that this script installs.
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "need root: run with sudo" >&2
  exit 1
fi

cat > /etc/sysctl.d/90-obs-lab-inotify.conf <<'EOF'
fs.inotify.max_user_instances = 1024
fs.inotify.max_user_watches = 1048576
EOF

sysctl -p /etc/sysctl.d/90-obs-lab-inotify.conf

echo "inotify limits:"
cat /proc/sys/fs/inotify/max_user_instances
cat /proc/sys/fs/inotify/max_user_watches