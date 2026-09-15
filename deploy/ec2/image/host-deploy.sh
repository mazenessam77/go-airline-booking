#!/usr/bin/env bash
set -Eeuo pipefail

readonly image_pattern='^740089361110\.dkr\.ecr\.us-east-1\.amazonaws\.com/go-airline-booking@sha256:[a-f0-9]{64}$'
readonly app_dir=/opt/airline

if [[ ${EUID} -ne 0 || $# -ne 1 || ! $1 =~ $image_pattern ]]; then
    echo "Usage: sudo /opt/airline/deploy.sh ECR_IMAGE@sha256:DIGEST" >&2
    exit 1
fi

exec 9>/run/airline-booking-deploy.lock
flock -n 9 || { echo "Another deployment is running" >&2; exit 1; }

image=$1
digest=${image##*@sha256:}
release_dir=$app_dir/releases/$digest
registry=${image%%/*}
docker_config=$(mktemp -d /run/airline-docker.XXXXXX)
container_id=""
cleanup() {
    [[ -z $container_id ]] || docker rm -f "$container_id" >/dev/null 2>&1 || true
    rm -rf -- "$docker_config"
}
trap cleanup EXIT
export DOCKER_CONFIG=$docker_config

aws ecr get-login-password --region us-east-1 \
    | docker login --username AWS --password-stdin "$registry" >/dev/null
docker pull "$image"

if [[ ! -d $release_dir ]]; then
    mkdir -p "$release_dir"
    container_id=$(docker create "$image")
    docker cp "${container_id}:/app/deploy/." "$release_dir"
    docker rm "$container_id" >/dev/null
    container_id=""
fi
printf 'AIRLINE_IMAGE=%s\n' "$image" > "$release_dir/release.env"
chmod 0600 "$release_dir/release.env"

compose=(docker compose --project-name airline-booking
    --env-file "$app_dir/runtime.env"
    --env-file "$release_dir/release.env"
    --file "$release_dir/compose.yml")

"${compose[@]}" config --quiet
"${compose[@]}" up --detach postgres
"${compose[@]}" run --rm migrate
"${compose[@]}" run --rm seed-demo
"${compose[@]}" up --detach --remove-orphans postgres api worker

for _ in $(seq 1 60); do
    if curl --fail --silent http://127.0.0.1:8080/readyz >/dev/null; then
        ln -sfn "$release_dir" "$app_dir/current"
        install -o root -g root -m 0755 "$release_dir/host-deploy.sh" "$app_dir/deploy.sh"
        echo "Deployment succeeded for sha256:${digest}"
        exit 0
    fi
    sleep 2
done

"${compose[@]}" ps
"${compose[@]}" logs --tail 100 api worker
echo "Deployment failed readiness checks" >&2
exit 1

