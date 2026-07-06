#!/usr/bin/env bash
set -Eeuo pipefail
IFS=$'\n\t'

NAMESPACE="${NAMESPACE:-ruleshift-core}"
DEPLOYMENT="${DEPLOYMENT:-ruleshift-gateway}"
POD_SELECTOR="${POD_SELECTOR:-app=ruleshift-gateway}"
GATEWAY_PORT="${GATEWAY_PORT:-8080}"
ROLLOUT_TIMEOUT="${ROLLOUT_TIMEOUT:-180s}"
PUBLIC_HEALTH_URL="${PUBLIC_HEALTH_URL:-https://api.ruleshift.ru/healthz}"
ALLOWED_IMAGE_REPOSITORY="${ALLOWED_IMAGE_REPOSITORY:-ghcr.io/ruleshift/server}"
EXPECTED_CONTEXT="${EXPECTED_CONTEXT:-}"
SKIP_PREFLIGHT_HEALTH="${SKIP_PREFLIGHT_HEALTH:-0}"
LOCK_DIR="${TMPDIR:-/tmp}/ruleshift-gateway-update.lock"

rollback_armed=0
lock_acquired=0
previous_image=""
container_name=""

log() {
  printf '[ruleshift-update] %s\n' "$*"
}

die() {
  log "ERROR: $*"
  exit 1
}

usage() {
  cat <<EOF
Usage: $0 IMAGE

IMAGE must use the configured repository and an immutable reference:
  ${ALLOWED_IMAGE_REPOSITORY}:<40-character-git-sha>
  ${ALLOWED_IMAGE_REPOSITORY}@sha256:<64-hex-digest>

Optional environment variables:
  NAMESPACE                 Kubernetes namespace (default: ${NAMESPACE})
  DEPLOYMENT                Deployment name (default: ${DEPLOYMENT})
  POD_SELECTOR              Gateway pod selector (default: ${POD_SELECTOR})
  ROLLOUT_TIMEOUT           kubectl timeout (default: ${ROLLOUT_TIMEOUT})
  PUBLIC_HEALTH_URL         HTTPS health URL; empty disables the check
  EXPECTED_CONTEXT          Refuse to run in another kubectl context
  SKIP_PREFLIGHT_HEALTH=1   Permit updating an already-unhealthy gateway
EOF
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

validate_image() {
  local image="$1"
  local reference

  if [[ "$image" == "${ALLOWED_IMAGE_REPOSITORY}:"* ]]; then
    reference="${image#${ALLOWED_IMAGE_REPOSITORY}:}"
    [[ "$reference" =~ ^[0-9a-f]{40}$ ]] || die "image tag must be a 40-character lowercase Git SHA"
    return
  fi
  if [[ "$image" == "${ALLOWED_IMAGE_REPOSITORY}@sha256:"* ]]; then
    reference="${image#${ALLOWED_IMAGE_REPOSITORY}@sha256:}"
    [[ "$reference" =~ ^[0-9a-f]{64}$ ]] || die "image digest must contain 64 lowercase hex characters"
    return
  fi
  die "image must use repository ${ALLOWED_IMAGE_REPOSITORY} and an immutable tag or digest"
}

latest_gateway_pod() {
  kubectl -n "$NAMESPACE" get pods \
    -l "$POD_SELECTOR" \
    --field-selector=status.phase=Running \
    --sort-by=.metadata.creationTimestamp \
    -o name | tail -n 1
}

check_internal_health() {
  local pod="$1"
  local response

  kubectl -n "$NAMESPACE" wait --for=condition=Ready "$pod" --timeout="$ROLLOUT_TIMEOUT" >/dev/null
  kubectl -n "$NAMESPACE" exec "$pod" -- sh -c \
    'test -n "$RULESHIFT_DATABASE_URL" && test -n "$RULESHIFT_DEVELOPER_API_KEY"' \
    || die "$pod is missing required database or Developer API configuration"
  response="$(kubectl -n "$NAMESPACE" exec "$pod" -- wget -qO- -T 5 "http://127.0.0.1:${GATEWAY_PORT}/healthz")"
  grep -Eq '"status"[[:space:]]*:[[:space:]]*"ok"' <<<"$response" \
    || die "$pod returned an invalid health response: $response"
}

check_public_health() {
  local response

  [[ -n "$PUBLIC_HEALTH_URL" ]] || return
  response="$(curl --fail --silent --show-error --max-time 10 "$PUBLIC_HEALTH_URL")"
  grep -Eq '"status"[[:space:]]*:[[:space:]]*"ok"' <<<"$response" \
    || die "public endpoint returned an invalid health response: $response"
}

print_diagnostics() {
  local pod

  log "Deployment diagnostics:"
  kubectl -n "$NAMESPACE" get deployment "$DEPLOYMENT" -o wide || true
  kubectl -n "$NAMESPACE" get pods -l "$POD_SELECTOR" -o wide || true
  pod="$(latest_gateway_pod 2>/dev/null || true)"
  if [[ -n "$pod" ]]; then
    kubectl -n "$NAMESPACE" logs "$pod" --tail=100 || true
  fi
}

rollback() {
  [[ -n "$previous_image" && -n "$container_name" ]] || return
  log "Rolling back to $previous_image"
  kubectl -n "$NAMESPACE" set image \
    "deployment/${DEPLOYMENT}" \
    "${container_name}=${previous_image}" || true
  kubectl -n "$NAMESPACE" rollout status \
    "deployment/${DEPLOYMENT}" \
    --timeout="$ROLLOUT_TIMEOUT" || true
}

on_exit() {
  local status=$?
  trap - EXIT
  if (( status != 0 && rollback_armed == 1 )); then
    print_diagnostics
    rollback_armed=0
    rollback
  fi
  if (( lock_acquired == 1 )); then
    rmdir "$LOCK_DIR" 2>/dev/null || true
  fi
  exit "$status"
}

trap on_exit EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi
[[ $# -eq 1 ]] || { usage >&2; exit 2; }

image="$1"
validate_image "$image"
require_command kubectl
require_command tail
require_command grep
if [[ -n "$PUBLIC_HEALTH_URL" ]]; then
  require_command curl
fi

mkdir "$LOCK_DIR" 2>/dev/null || die "another gateway update is running (lock: $LOCK_DIR)"
lock_acquired=1

context="$(kubectl config current-context)"
[[ -n "$context" ]] || die "kubectl has no current context"
if [[ -n "$EXPECTED_CONTEXT" && "$context" != "$EXPECTED_CONTEXT" ]]; then
  die "kubectl context is $context, expected $EXPECTED_CONTEXT"
fi
log "Context: $context"

kubectl get namespace "$NAMESPACE" >/dev/null
core_label="$(kubectl get namespace "$NAMESPACE" -o jsonpath='{.metadata.labels.ruleshift\.io/core}')"
[[ "$core_label" == "true" ]] || die "namespace $NAMESPACE must have label ruleshift.io/core=true"
kubectl -n "$NAMESPACE" get deployment "$DEPLOYMENT" >/dev/null

mapfile -t containers < <(kubectl -n "$NAMESPACE" get deployment "$DEPLOYMENT" \
  -o jsonpath='{range .spec.template.spec.containers[*]}{.name}{"\n"}{end}')
[[ ${#containers[@]} -eq 1 ]] || die "deployment must contain exactly one container; found ${#containers[@]}"
container_name="${containers[0]}"
previous_image="$(kubectl -n "$NAMESPACE" get deployment "$DEPLOYMENT" \
  -o jsonpath='{.spec.template.spec.containers[0].image}')"
[[ -n "$previous_image" ]] || die "could not determine the current gateway image"
log "Current image: $previous_image"
log "Target image:  $image"

kubectl -n "$NAMESPACE" rollout status "deployment/${DEPLOYMENT}" --timeout="$ROLLOUT_TIMEOUT" >/dev/null
current_pod="$(latest_gateway_pod)"
[[ -n "$current_pod" ]] || die "no running gateway pod matches $POD_SELECTOR"
if [[ "$SKIP_PREFLIGHT_HEALTH" != "1" ]]; then
  log "Checking current gateway before rollout"
  check_internal_health "$current_pod"
  check_public_health
fi

if [[ "$previous_image" == "$image" ]]; then
  log "Deployment already uses the requested image; no update needed"
  exit 0
fi

rollback_armed=1
log "Updating deployment"
kubectl -n "$NAMESPACE" set image \
  "deployment/${DEPLOYMENT}" \
  "${container_name}=${image}"
kubectl -n "$NAMESPACE" annotate deployment "$DEPLOYMENT" \
  kubernetes.io/change-cause="gateway update to ${image}" \
  --overwrite >/dev/null

log "Waiting for rollout"
kubectl -n "$NAMESPACE" rollout status \
  "deployment/${DEPLOYMENT}" \
  --timeout="$ROLLOUT_TIMEOUT"

deployed_image="$(kubectl -n "$NAMESPACE" get deployment "$DEPLOYMENT" \
  -o jsonpath='{.spec.template.spec.containers[0].image}')"
[[ "$deployed_image" == "$image" ]] || die "deployment reports unexpected image: $deployed_image"

new_pod="$(latest_gateway_pod)"
[[ -n "$new_pod" ]] || die "rollout completed without a running gateway pod"
restart_count="$(kubectl -n "$NAMESPACE" get "$new_pod" \
  -o jsonpath='{.status.containerStatuses[0].restartCount}')"
[[ "$restart_count" == "0" ]] || die "$new_pod restarted $restart_count time(s)"

log "Checking updated gateway"
check_internal_health "$new_pod"
check_public_health

rollback_armed=0
log "Gateway update completed successfully"
log "Pod:   $new_pod"
log "Image: $deployed_image"
