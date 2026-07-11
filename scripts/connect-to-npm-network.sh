#!/usr/bin/env bash
set -euo pipefail

TARGET_NETWORK="${1:-npm_network}"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$PROJECT_DIR"

if ! command -v docker >/dev/null 2>&1; then
  echo "[error] docker not found"
  exit 1
fi

if ! docker network inspect "$TARGET_NETWORK" >/dev/null 2>&1; then
  echo "[error] docker network '$TARGET_NETWORK' not found"
  echo "        create it first: docker network create $TARGET_NETWORK"
  exit 1
fi

mapfile -t CONTAINERS < <(docker compose ps -q)

if [[ ${#CONTAINERS[@]} -eq 0 ]]; then
  echo "[error] no running compose containers found in $PROJECT_DIR"
  echo "        start them first: docker compose up -d"
  exit 1
fi

echo "[info] target network: $TARGET_NETWORK"

for container_id in "${CONTAINERS[@]}"; do
  container_name="$(docker inspect -f '{{.Name}}' "$container_id" | sed 's#^/##')"

  if docker inspect -f '{{json .NetworkSettings.Networks}}' "$container_id" | grep -q "\"$TARGET_NETWORK\""; then
    echo "[skip] $container_name already connected to $TARGET_NETWORK"
    continue
  fi

  echo "[connect] $container_name -> $TARGET_NETWORK"
  docker network connect "$TARGET_NETWORK" "$container_id"
done

echo "[done] compose containers connected to $TARGET_NETWORK"
