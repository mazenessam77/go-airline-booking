#!/usr/bin/env bash
set -Eeuo pipefail

readonly script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
readonly app_dir=/opt/airline

if [[ ${EUID} -ne 0 ]]; then
    echo "Run this script with sudo" >&2
    exit 1
fi
if [[ -n $(podman ps -aq 2>/dev/null || true) ]]; then
    echo "Refusing to remove Podman while containers exist" >&2
    exit 1
fi

dnf install -y dnf-plugins-core openssl awscli2 "kernel-modules-extra-$(uname -r)"
dnf remove -y podman
dnf config-manager --add-repo https://download.docker.com/linux/rhel/docker-ce.repo
dnf install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
dnf install -y https://s3.us-east-1.amazonaws.com/amazon-ssm-us-east-1/latest/linux_amd64/amazon-ssm-agent.rpm

install -d -o root -g root -m 0700 "$app_dir" "$app_dir/releases"
if [[ ! -f $app_dir/runtime.env ]]; then
    umask 077
    printf 'POSTGRES_PASSWORD=%s\n' "$(openssl rand -hex 32)" > "$app_dir/runtime.env"
fi
install -o root -g root -m 0755 "$script_dir/image/host-deploy.sh" "$app_dir/deploy.sh"

modprobe overlay
modprobe br_netfilter
modprobe xt_addrtype
systemctl enable --now docker
systemctl enable --now amazon-ssm-agent
docker compose version
systemctl is-active --quiet amazon-ssm-agent
echo "EC2 bootstrap completed"
