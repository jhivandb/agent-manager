#!/bin/bash
# shellcheck source-path=SCRIPTDIR
set -euo pipefail

# Adds a "prod" environment to a local k3d + docker-compose install and points the
# default deployment pipeline at it, so agents promote "default → prod".
#
# Run after `make setup` (agent-manager-service must answer on :9000 and the
# port-forwards must be up). Re-running is idempotent: add-environment.sh upserts
# the environment, its Thunder instance and its gateway release, and the pipeline
# update below is a declarative PUT.
#
# Optional overrides: ENV_NAME (prod), DISPLAY_NAME (Production), ORG_NAME
# (default), PIPELINE_NAME (default), SOURCE_ENV_NAME (default),
# IS_PRODUCTION (true), AGENT_MANAGER_URL (http://localhost:9000).

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

ENV_NAME="${ENV_NAME:-prod}"
DISPLAY_NAME="${DISPLAY_NAME:-Production}"
ORG_NAME="${ORG_NAME:-default}"
PIPELINE_NAME="${PIPELINE_NAME:-default}"
SOURCE_ENV_NAME="${SOURCE_ENV_NAME:-default}"
IS_PRODUCTION="${IS_PRODUCTION:-true}"
AGENT_MANAGER_URL="${AGENT_MANAGER_URL:-http://localhost:9000}"
AGENT_MANAGER_API_URL="${AGENT_MANAGER_API_URL:-${AGENT_MANAGER_URL}/api/v1}"

# shellcheck source=../scripts/ams-auth.sh
source "${SCRIPT_DIR}/../scripts/ams-auth.sh"

# One token has to cover all three AMS calls this run makes: creating the
# environment, storing its Thunder system client (add-environment-thunder.sh's
# store_via_ams, which reuses the token exported here), and rewriting the
# pipeline. Thunder issues requested ∩ allowed and amp-api-client holds the admin
# role, so requesting the whole set up front costs nothing.
mint_ams_token() {
  AMS_TOKEN_SCOPES="amp:environment:create amp:deployment-pipeline:read amp:deployment-pipeline:update amp:org:manage-service-account"
  export AMS_TOKEN_SCOPES

  local token
  if ! token="$(get_ams_token)"; then
    echo "❌ Could not mint an access token for agent-manager-service." >&2
    echo "   Check platform Thunder is reachable at ${IDP_TOKEN_URL:-http://thunder.amp.localhost:8080/oauth2/token}" >&2
    echo "   (run 'make port-forward'), or export AGENT_MANAGER_TOKEN yourself." >&2
    return 1
  fi
  AGENT_MANAGER_TOKEN="$token"
  export AGENT_MANAGER_TOKEN
}

# Everything add-environment.sh defaults to already describes this install
# (host.docker.internal:9000 for in-cluster callers, am-gateway.localhost:19080
# for agent ingress, gateway.localhost:19080 for the published vhost), and its
# --set flags fully supersede the gateway chart's values-dev.yaml — so only the
# two things that differ from a released install are passed here:
#
#   GATEWAY_CHART    the chart from this working tree, so no CHART_VERSION and no OCI pull
#   SCRIPT_BASE_URL  a file:// base, so the chained add-environment-thunder.sh and the
#                    libraries it loads also come from this working tree. Without it that
#                    script is fetched from GitHub main (it runs from a mktemp path, so its
#                    own sibling-file lookup can never find the local copy) and a dev-loop
#                    run would silently exercise main's provisioning rather than the branch's.
provision_environment() {
  ENV_NAME="${ENV_NAME}" \
  DISPLAY_NAME="${DISPLAY_NAME}" \
  ORG_NAME="${ORG_NAME}" \
  IS_PRODUCTION="${IS_PRODUCTION}" \
  AGENT_MANAGER_URL="${AGENT_MANAGER_URL}" \
  GATEWAY_CHART="${REPO_ROOT}/deployments/helm-charts/wso2-amp-api-platform-gateway-extension" \
  SCRIPT_BASE_URL="file://${REPO_ROOT}/deployments/scripts" \
  bash "${SCRIPT_DIR}/../scripts/add-environment.sh"
}

# Declarative, not additive: the chain is always rewritten to
# "<source> → <env>", the same body the console's chain editor sends for a
# two-environment pipeline (chainToPromotionPaths). That keeps re-runs
# idempotent, at the cost of replacing any other chain already on this pipeline.
#
# displayName and description are omitted on purpose — agent-manager-service GETs
# the pipeline and patches only the fields it is sent, so leaving them out
# preserves both.
set_pipeline_chain() {
  local body
  body="$(printf '{"promotionPaths":[{"sourceEnvironmentRef":"%s","targetEnvironmentRefs":[{"name":"%s"}]}]}' \
    "$(json_escape "${SOURCE_ENV_NAME}")" "$(json_escape "${ENV_NAME}")")"

  local response http_code resp_body
  response="$(curl -s -w '\n%{http_code}' --max-time 30 --retry 5 --retry-delay 5 \
    -X PUT "${AGENT_MANAGER_API_URL}/orgs/${ORG_NAME}/deployment-pipelines/${PIPELINE_NAME}" \
    -H "Authorization: Bearer ${AGENT_MANAGER_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "${body}")"
  http_code="$(printf '%s' "${response}" | tail -n1)"
  resp_body="$(printf '%s' "${response}" | sed '$d')"

  if [ "${http_code}" != "200" ]; then
    echo "❌ Could not update deployment pipeline '${PIPELINE_NAME}' (HTTP ${http_code}): ${resp_body}" >&2
    echo "   Environment '${ENV_NAME}' itself is provisioned. Retry just this step with:" >&2
    echo "     curl -X PUT ${AGENT_MANAGER_API_URL}/orgs/${ORG_NAME}/deployment-pipelines/${PIPELINE_NAME} \\" >&2
    echo "       -H \"Authorization: Bearer \${AGENT_MANAGER_TOKEN}\" -H 'Content-Type: application/json' \\" >&2
    echo "       -d '${body}'" >&2
    return 1
  fi
}

echo "=== Adding environment '${ENV_NAME}' ==="
mint_ams_token
provision_environment

echo ""
echo "🔗 Pointing deployment pipeline '${PIPELINE_NAME}' at ${SOURCE_ENV_NAME} → ${ENV_NAME}..."
set_pipeline_chain
echo "✅ Deployment pipeline '${PIPELINE_NAME}': ${SOURCE_ENV_NAME} → ${ENV_NAME}"

echo ""
echo "✅ Environment '${ENV_NAME}' is ready"
echo "   Promote an agent along the pipeline: ${SOURCE_ENV_NAME} → ${ENV_NAME}"
if [ "${IS_PRODUCTION}" = "true" ]; then
  echo "   Flagged as production — deploy/promote/suspend there needs amp:agent:env-production"
fi
