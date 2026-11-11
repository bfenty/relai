#!/usr/bin/env bash
# deploy.sh - Build Relai locally, push it to a Proxmox host, and run deploy-lxc.sh.
#
# Run this from the Relai repo on your workstation/build machine.
#
# Required:
#   PROXMOX_HOST       Proxmox host or SSH alias
#   RELAI_ADMIN_KEY    Admin key written into the LXC service env
#
# Common optional variables:
#   PROXMOX_USER       SSH user [root]
#   PROXMOX_DIR        Remote staging directory [/tmp/relai-deploy]
#   VMID               LXC ID passed through to deploy-lxc.sh [300]
#   LXC_HOSTNAME       LXC hostname passed through to deploy-lxc.sh [relai]
#   RELAI_PORT         Relai port inside the LXC [8080]
#   RELAI_LOG_DB_URL   PostgreSQL DSN for request logs
#   RELAI_REQUIRE_POSTGRES_LOGS Fail startup if RELAI_LOG_DB_URL is empty [auto]
#   RUN_TESTS          Run Go tests before deploy [true]

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FRONTEND_DIR="${ROOT_DIR}/frontend"
EMBED_DIR="${ROOT_DIR}/internal/server/frontend/dist"
BIN_DIR="${ROOT_DIR}/bin"
BIN_PATH="${BIN_DIR}/relai"

PROXMOX_HOST="${PROXMOX_HOST:-}"
PROXMOX_USER="${PROXMOX_USER:-root}"
PROXMOX_DIR="${PROXMOX_DIR:-/tmp/relai-deploy}"
RUN_TESTS="${RUN_TESTS:-true}"

usage() {
  cat <<'USAGE'
Usage: ./deploy.sh [command]

Commands:
  deploy       Build locally, push to Proxmox, and run deploy-lxc.sh (default)
  build        Build frontend assets and the relai Go binary only
  test         Run Go tests only
  help         Show this help

Required for deploy:
  PROXMOX_HOST       Proxmox host or SSH alias
  RELAI_ADMIN_KEY    Admin key for Relai

Examples:
  PROXMOX_HOST=pve1 RELAI_ADMIN_KEY=secret ./deploy.sh
  PROXMOX_HOST=10.0.0.5 VMID=300 RELAI_ADMIN_KEY=secret ./deploy.sh deploy
  RUN_TESTS=false PROXMOX_HOST=pve1 RELAI_ADMIN_KEY=secret ./deploy.sh

Pass-through variables for deploy-lxc.sh include:
  VMID, LXC_HOSTNAME, MEMORY, CORES, DISK, STORAGE, BRIDGE, RELAI_PORT,
  RELAI_LOG_DB_URL, RELAI_REQUIRE_POSTGRES_LOGS
USAGE
}

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: required command not found: $1" >&2
    exit 1
  fi
}

SSH_OPTS=()

remote() {
  ssh "${SSH_OPTS[@]+"${SSH_OPTS[@]}"}" "${PROXMOX_USER}@${PROXMOX_HOST}" "$@"
}

remote_copy() {
  scp "${SSH_OPTS[@]+"${SSH_OPTS[@]}"}" "$@" "${PROXMOX_USER}@${PROXMOX_HOST}:${PROXMOX_DIR}/"
}

shell_quote() {
  printf "%q" "$1"
}

build_frontend() {
  need_cmd npm

  echo "==> Building frontend"
  mkdir -p "${EMBED_DIR}"

  cd "${FRONTEND_DIR}"
  if [[ -f package-lock.json ]]; then
    npm ci
  else
    npm install
  fi
  npm run build
}

build_binary() {
  need_cmd go

  echo "==> Building relai binary"
  mkdir -p "${BIN_DIR}"
  cd "${ROOT_DIR}"
  go build -ldflags="-w -s" -o "${BIN_PATH}" ./cmd/relai
  echo "built ${BIN_PATH}"
}

run_tests() {
  need_cmd go

  echo "==> Running Go tests"
  cd "${ROOT_DIR}"
  go test ./...
}

build_all() {
  if [[ "${RUN_TESTS}" == "true" ]]; then
    run_tests
  fi
  build_frontend
  build_binary
}

require_deploy_env() {
  if [[ -z "${PROXMOX_HOST}" ]]; then
    echo "error: PROXMOX_HOST is required for deployment" >&2
    exit 1
  fi
  if [[ -z "${RELAI_ADMIN_KEY:-}" ]]; then
    echo "error: RELAI_ADMIN_KEY is required for deployment" >&2
    exit 1
  fi
}

deploy_to_proxmox() {
  need_cmd ssh
  need_cmd scp
  require_deploy_env
  build_all

  local _ctl_dir _ctl
  _ctl_dir="$(mktemp -d)"
  _ctl="${_ctl_dir}/ctl"
  SSH_OPTS=(-o "ControlMaster=auto" -o "ControlPath=${_ctl}" -o "ControlPersist=300")
  # shellcheck disable=SC2064
  trap "ssh -o 'ControlPath=${_ctl}' -O exit '${PROXMOX_USER}@${PROXMOX_HOST}' 2>/dev/null || true; rm -rf '${_ctl_dir}'" EXIT

  echo "==> Creating remote staging directory ${PROXMOX_USER}@${PROXMOX_HOST}:${PROXMOX_DIR}"
  remote "mkdir -p '${PROXMOX_DIR}'"

  echo "==> Pushing binary and LXC deploy script"
  remote_copy "${BIN_PATH}" "${ROOT_DIR}/deploy-lxc.sh"

  echo "==> Running deploy-lxc.sh on Proxmox"
  require_pg_logs="${RELAI_REQUIRE_POSTGRES_LOGS:-}"
  if [[ -z "${require_pg_logs}" ]]; then
    if [[ -n "${RELAI_LOG_DB_URL:-}" ]]; then
      require_pg_logs="true"
    else
      require_pg_logs="false"
    fi
  fi
  remote_env=(
    "RELAI_BINARY=$(shell_quote "${PROXMOX_DIR}/relai")"
    "RELAI_ADMIN_KEY=$(shell_quote "${RELAI_ADMIN_KEY}")"
    "RELAI_LOG_DB_URL=$(shell_quote "${RELAI_LOG_DB_URL:-}")"
    "RELAI_REQUIRE_POSTGRES_LOGS=$(shell_quote "${require_pg_logs}")"
    "RELAI_READY_REQUIRE_MODELS=$(shell_quote "${RELAI_READY_REQUIRE_MODELS:-false}")"
    "VMID=$(shell_quote "${VMID:-300}")"
    "LXC_HOSTNAME=$(shell_quote "${LXC_HOSTNAME:-relai}")"
    "MEMORY=$(shell_quote "${MEMORY:-1024}")"
    "CORES=$(shell_quote "${CORES:-2}")"
    "DISK=$(shell_quote "${DISK:-8}")"
    "STORAGE=$(shell_quote "${STORAGE:-local-lvm}")"
    "BRIDGE=$(shell_quote "${BRIDGE:-vmbr0}")"
    "RELAI_PORT=$(shell_quote "${RELAI_PORT:-8080}")"
  )

  remote "cd '${PROXMOX_DIR}' && chmod +x ./deploy-lxc.sh ./relai && ${remote_env[*]} ./deploy-lxc.sh"
}

main() {
  local command="${1:-deploy}"

  case "${command}" in
    deploy)
      deploy_to_proxmox
      ;;
    build)
      build_frontend
      build_binary
      ;;
    test)
      run_tests
      ;;
    help|-h|--help)
      usage
      ;;
    *)
      echo "error: unknown command: ${command}" >&2
      usage >&2
      exit 1
      ;;
  esac
}

main "$@"
