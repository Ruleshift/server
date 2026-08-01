#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

CORE_NAMESPACE="${CORE_NAMESPACE:-ruleshift-core}"
GATEWAY_DEPLOYMENT="${GATEWAY_DEPLOYMENT:-ruleshift-gateway}"
POSTGRES_SERVICE="${POSTGRES_SERVICE:-postgres}"
LOCAL_PG_PORT="${LOCAL_PG_PORT:-15432}"
KUBE_TIMEOUT="${KUBE_TIMEOUT:-60s}"
EXPECTED_CONTEXT="${EXPECTED_CONTEXT:-}"
CONTROL_DATABASE_URL="${CONTROL_DATABASE_URL:-${RULESHIFT_DATABASE_URL:-}}"
USE_PORT_FORWARD="${USE_PORT_FORWARD:-1}"
DELETE_MODULE_RECORD_IF_EMPTY=0
DELETE_ROOMS=0
ALLOW_NON_FAILED=0
YES=0
ALL_FAILED=0
DEVELOPER_ID=""
MODULE_ID=""
VERSION=""
STATUS_FILTER="failed"
PF_PID=""
PF_LOG=""
CANDIDATES_FILE=""

log() {
  printf '[ruleshift-purge] %s\n' "$*" >&2
}

die() {
  log "ERROR: $*"
  exit 1
}

usage() {
  cat <<EOF
Usage:
  $0 --developer-id ID --module-id MODULE [--version SEMVER] [options]
  $0 --all-failed [options]

Deletes module-version runtime resources and database records.
Dry-run is the default. Add --yes to execute.

Selection:
  --developer-id ID       Developer id to purge.
  --module-id MODULE      Module id/key to purge.
  --version SEMVER        Optional exact module version.
  --status STATUS         Status filter when selecting versions. Default: failed.
                          Use --status any with --allow-non-failed for active/inactive.
  --all-failed            Select every failed version in the control DB.

Destructive options:
  --delete-rooms          Also delete rooms pinned to the selected version(s).
  --allow-non-failed      Permit deleting statuses other than failed.
  --delete-module-record-if-empty
                          Delete modules row when no module_versions remain.
  --yes                   Execute deletion. Without this, only prints the plan.

Connection options:
  CONTROL_DATABASE_URL    Control DB URL. If empty, read RULESHIFT_DATABASE_URL
                          from deployment/${GATEWAY_DEPLOYMENT}.
  CORE_NAMESPACE          Core namespace. Default: ${CORE_NAMESPACE}
  GATEWAY_DEPLOYMENT      Gateway Deployment. Default: ${GATEWAY_DEPLOYMENT}
  POSTGRES_SERVICE        PostgreSQL Service for port-forward. Default: ${POSTGRES_SERVICE}
  LOCAL_PG_PORT           Local forwarded port. Default: ${LOCAL_PG_PORT}
  USE_PORT_FORWARD=0      Do not start kubectl port-forward; use DB URL as-is.
  EXPECTED_CONTEXT        Refuse to run against another kubectl context.
  KUBE_TIMEOUT            kubectl delete timeout. Default: ${KUBE_TIMEOUT}

Examples:
  $0 --developer-id default --module-id leastpopular --version 1.0.3 --delete-rooms --yes
  $0 --developer-id default --module-id leastpopular --status failed --delete-rooms --yes
  $0 --all-failed --delete-rooms --yes

To delete active or inactive versions deliberately:
  $0 --developer-id default --module-id leastpopular --version 1.0.3 \\
    --status any --allow-non-failed --delete-rooms --yes
EOF
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n "$PF_PID" ]]; then
    kill "$PF_PID" 2>/dev/null || true
    wait "$PF_PID" 2>/dev/null || true
  fi
  [[ -z "$PF_LOG" ]] || rm -f "$PF_LOG"
  [[ -z "$CANDIDATES_FILE" ]] || rm -f "$CANDIDATES_FILE"
  exit "$status"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

while [[ $# -gt 0 ]]; do
  case "$1" in
    --developer-id)
      DEVELOPER_ID="${2:-}"
      shift 2
      ;;
    --module-id)
      MODULE_ID="${2:-}"
      shift 2
      ;;
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --status)
      STATUS_FILTER="${2:-}"
      shift 2
      ;;
    --all-failed)
      ALL_FAILED=1
      STATUS_FILTER="failed"
      shift
      ;;
    --delete-rooms)
      DELETE_ROOMS=1
      shift
      ;;
    --allow-non-failed)
      ALLOW_NON_FAILED=1
      shift
      ;;
    --delete-module-record-if-empty)
      DELETE_MODULE_RECORD_IF_EMPTY=1
      shift
      ;;
    --yes)
      YES=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      die "unknown argument: $1"
      ;;
  esac
done

[[ "$STATUS_FILTER" =~ ^(validating|active|inactive|failed|degraded|any)$ ]] \
  || die "--status must be validating, active, inactive, failed, degraded, or any"
if (( ALL_FAILED == 0 )); then
  [[ -n "$DEVELOPER_ID" ]] || die "--developer-id is required unless --all-failed is used"
  [[ -n "$MODULE_ID" ]] || die "--module-id is required unless --all-failed is used"
fi
if [[ "$STATUS_FILTER" != "failed" && "$ALLOW_NON_FAILED" != "1" ]]; then
  die "refusing to select non-failed versions without --allow-non-failed"
fi

require_command kubectl
require_command psql
require_command python3
require_command mktemp

context="$(kubectl config current-context)"
[[ -n "$context" ]] || die "kubectl has no current context"
if [[ -n "$EXPECTED_CONTEXT" && "$context" != "$EXPECTED_CONTEXT" ]]; then
  die "kubectl context is $context, expected $EXPECTED_CONTEXT"
fi
log "Kubernetes context: $context"

if [[ -z "$CONTROL_DATABASE_URL" ]]; then
  log "Reading RULESHIFT_DATABASE_URL from deployment/${GATEWAY_DEPLOYMENT}"
  CONTROL_DATABASE_URL="$(kubectl -n "$CORE_NAMESPACE" exec "deployment/${GATEWAY_DEPLOYMENT}" -- printenv RULESHIFT_DATABASE_URL)"
fi
[[ -n "$CONTROL_DATABASE_URL" ]] || die "CONTROL_DATABASE_URL/RULESHIFT_DATABASE_URL is empty"

rewrite_url_host_port() {
  python3 - "$1" "$2" <<'PY'
import sys
from urllib.parse import urlsplit, urlunsplit

url, port = sys.argv[1], sys.argv[2]
parts = urlsplit(url)
if not parts.scheme or not parts.netloc:
    raise SystemExit("database URL must be a PostgreSQL URI")
userinfo, sep, _hostport = parts.netloc.rpartition("@")
prefix = userinfo + sep if sep else ""
print(urlunsplit((parts.scheme, prefix + "127.0.0.1:" + port, parts.path, parts.query, parts.fragment)))
PY
}

database_url_for_name() {
  python3 - "$1" "$2" <<'PY'
import sys
from urllib.parse import quote, urlsplit, urlunsplit

url, db_name = sys.argv[1], sys.argv[2]
parts = urlsplit(url)
if not parts.scheme or not parts.netloc:
    raise SystemExit("database URL must be a PostgreSQL URI")
print(urlunsplit((parts.scheme, parts.netloc, "/" + quote(db_name), parts.query, parts.fragment)))
PY
}

namespace_for_developer() {
  python3 - "$1" <<'PY'
import hashlib
import sys

developer_id = sys.argv[1]
print("ruleshift-tenant-" + hashlib.sha256(developer_id.encode()).hexdigest()[:16])
PY
}

workload_for_version() {
  python3 - "$1" "$2" "$3" <<'PY'
import hashlib
import sys

module_id, version, image_digest = sys.argv[1], sys.argv[2], sys.argv[3]
raw = module_id.encode() + b"\0" + version.encode() + b"\0" + image_digest.encode()
print("module-" + hashlib.sha256(raw).hexdigest()[:20])
PY
}

LOCAL_CONTROL_DATABASE_URL="$CONTROL_DATABASE_URL"
if [[ "$USE_PORT_FORWARD" == "1" ]]; then
  LOCAL_CONTROL_DATABASE_URL="$(rewrite_url_host_port "$CONTROL_DATABASE_URL" "$LOCAL_PG_PORT")"
  PF_LOG="$(mktemp)"
  log "Starting port-forward svc/${POSTGRES_SERVICE} ${LOCAL_PG_PORT}:5432"
  kubectl -n "$CORE_NAMESPACE" port-forward "svc/${POSTGRES_SERVICE}" "${LOCAL_PG_PORT}:5432" >"$PF_LOG" 2>&1 &
  PF_PID=$!
  for _ in {1..30}; do
    if psql "$LOCAL_CONTROL_DATABASE_URL" -X -v ON_ERROR_STOP=1 -Atqc 'SELECT 1' >/dev/null 2>&1; then
      break
    fi
    if ! kill -0 "$PF_PID" 2>/dev/null; then
      die "port-forward exited early; log: $(cat "$PF_LOG")"
    fi
    sleep 1
  done
  psql "$LOCAL_CONTROL_DATABASE_URL" -X -v ON_ERROR_STOP=1 -Atqc 'SELECT 1' >/dev/null \
    || die "database is not reachable through port-forward; log: $(cat "$PF_LOG")"
fi

psql_control() {
  psql "$LOCAL_CONTROL_DATABASE_URL" -X -v ON_ERROR_STOP=1 "$@"
}

CANDIDATES_FILE="$(mktemp)"
psql_control -At -F $'\t' \
  -v all_failed="$ALL_FAILED" \
  -v developer_id="$DEVELOPER_ID" \
  -v module_id="$MODULE_ID" \
  -v version="$VERSION" \
  -v status_filter="$STATUS_FILTER" <<'SQL' >"$CANDIDATES_FILE"
SELECT
  mv.developer_id,
  mv.module_id,
  mv.version,
  mv.image_digest,
  mv.lifecycle_status,
  m.database_name,
  COUNT(rr.room_id)::text AS pinned_rooms
FROM module_versions mv
JOIN modules m
  ON m.developer_id = mv.developer_id
 AND m.module_key = mv.module_id
LEFT JOIN room_routes rr
  ON rr.developer_id = mv.developer_id
 AND rr.module_id = mv.module_id
 AND rr.module_version = mv.version
WHERE (:'all_failed' = '1' OR mv.developer_id = :'developer_id')
  AND (:'all_failed' = '1' OR mv.module_id = :'module_id')
  AND (:'version' = '' OR mv.version = :'version')
  AND (:'status_filter' = 'any' OR mv.lifecycle_status = :'status_filter')
GROUP BY
  mv.developer_id,
  mv.module_id,
  mv.version,
  mv.image_digest,
  mv.lifecycle_status,
  m.database_name
ORDER BY mv.developer_id, mv.module_id, mv.version;
SQL

if [[ ! -s "$CANDIDATES_FILE" ]]; then
  log "No matching module versions."
  exit 0
fi

log "Planned purge:"
while IFS=$'\t' read -r developer_id module_id version image_digest status database_name pinned_rooms; do
  namespace="$(namespace_for_developer "$developer_id")"
  workload="$(workload_for_version "$module_id" "$version" "$image_digest")"
  log "  ${developer_id}/${module_id}/${version} status=${status} rooms=${pinned_rooms} namespace=${namespace} workload=${workload} db=${database_name}"
  if [[ "$status" != "failed" && "$ALLOW_NON_FAILED" != "1" ]]; then
    die "refusing to delete ${developer_id}/${module_id}/${version}: status=${status}; add --allow-non-failed"
  fi
  if (( pinned_rooms > 0 && DELETE_ROOMS != 1 )); then
    die "refusing to delete ${developer_id}/${module_id}/${version}: ${pinned_rooms} room(s) are pinned; add --delete-rooms"
  fi
done <"$CANDIDATES_FILE"

if (( YES != 1 )); then
  log "Dry-run only. Re-run with --yes to execute."
  exit 0
fi

delete_kubernetes_resources() {
  local namespace="$1"
  local workload="$2"

  if ! kubectl get namespace "$namespace" >/dev/null 2>&1; then
    log "Namespace ${namespace} does not exist; skipping Kubernetes resources for ${workload}"
    return
  fi

  log "Deleting Kubernetes resources ${namespace}/${workload}"
  kubectl -n "$namespace" delete deployment "$workload" \
    --cascade=foreground --wait=true --timeout="$KUBE_TIMEOUT" --ignore-not-found
  kubectl -n "$namespace" delete service "$workload" --ignore-not-found
  kubectl -n "$namespace" delete secret "${workload}-rpc" --ignore-not-found
  kubectl -n "$namespace" delete rs,pod \
    -l "ruleshift.io/workload=${workload}" \
    --wait=true --timeout="$KUBE_TIMEOUT" --ignore-not-found
}

delete_module_rooms() {
  local developer_id="$1"
  local module_id="$2"
  local version="$3"
  local module_db_url="$4"
  local rooms_file

  rooms_file="$(mktemp)"
  psql_control -At \
    -v developer_id="$developer_id" \
    -v module_id="$module_id" \
    -v version="$version" <<'SQL' >"$rooms_file"
SELECT room_id
FROM room_routes
WHERE developer_id = :'developer_id'
  AND module_id = :'module_id'
  AND module_version = :'version'
ORDER BY room_id;
SQL

  if [[ ! -s "$rooms_file" ]]; then
    rm -f "$rooms_file"
    return
  fi

  log "Deleting module DB room rows for ${developer_id}/${module_id}/${version}"
  psql "$module_db_url" -X -v ON_ERROR_STOP=1 -v rooms_file="$rooms_file" <<'SQL'
BEGIN;
CREATE TEMP TABLE ruleshift_purge_rooms(id TEXT PRIMARY KEY) ON COMMIT DROP;
\copy ruleshift_purge_rooms(id) FROM :'rooms_file'
DELETE FROM rooms WHERE id IN (SELECT id FROM ruleshift_purge_rooms);
COMMIT;
SQL
  rm -f "$rooms_file"
}

delete_control_records() {
  local developer_id="$1"
  local module_id="$2"
  local version="$3"

  log "Deleting control DB records for ${developer_id}/${module_id}/${version}"
  psql_control \
    -v developer_id="$developer_id" \
    -v module_id="$module_id" \
    -v version="$version" \
    -v delete_module_record_if_empty="$DELETE_MODULE_RECORD_IF_EMPTY" <<'SQL'
BEGIN;
UPDATE modules
SET active_version = NULL, updated_at = NOW()
WHERE developer_id = :'developer_id'
  AND module_key = :'module_id'
  AND active_version = :'version';

DELETE FROM room_routes
WHERE developer_id = :'developer_id'
  AND module_id = :'module_id'
  AND module_version = :'version';

DELETE FROM module_validation_runs
WHERE developer_id = :'developer_id'
  AND module_id = :'module_id'
  AND version = :'version';

DELETE FROM module_versions
WHERE developer_id = :'developer_id'
  AND module_id = :'module_id'
  AND version = :'version';

DELETE FROM modules
WHERE :'delete_module_record_if_empty' = '1'
  AND developer_id = :'developer_id'
  AND module_key = :'module_id'
  AND NOT EXISTS (
    SELECT 1
    FROM module_versions
    WHERE developer_id = :'developer_id'
      AND module_id = :'module_id'
  );
COMMIT;
SQL
}

while IFS=$'\t' read -r developer_id module_id version image_digest status database_name pinned_rooms; do
  namespace="$(namespace_for_developer "$developer_id")"
  workload="$(workload_for_version "$module_id" "$version" "$image_digest")"
  module_db_url="$(database_url_for_name "$LOCAL_CONTROL_DATABASE_URL" "$database_name")"

  delete_kubernetes_resources "$namespace" "$workload"
  if (( pinned_rooms > 0 )); then
    delete_module_rooms "$developer_id" "$module_id" "$version" "$module_db_url"
  fi
  delete_control_records "$developer_id" "$module_id" "$version"
done <"$CANDIDATES_FILE"

log "Purge completed."
