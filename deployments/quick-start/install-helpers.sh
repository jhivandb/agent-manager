#!/bin/bash
# Helper functions for Agent Management Platform installation
# This file provides functions to install AMP helm charts from public registry

set -euo pipefail

# ============================================================================
# CONFIGURATION
# ============================================================================

# Version
VERSION="${VERSION:-0.0.0-dev}"

# Helm chart registry and versions
HELM_CHART_REGISTRY="${HELM_CHART_REGISTRY:-ghcr.io/wso2}"

# Chart names
AMP_CHART_NAME="wso2-agent-manager"
OBSERVABILITY_CHART_NAME="wso2-amp-observability-extension"
PLATFORM_RESOURCES_CHART_NAME="wso2-amp-platform-resources-extension"
THUNDER_EXTENSION_CHART_NAME="wso2-amp-thunder-extension"
EVALUATION_CHART_NAME="wso2-amp-evaluation-extension"
GATEWAY_EXTENSION_CHART_NAME="wso2-amp-api-platform-gateway-extension"

# Namespace definitions
AMP_NS="${AMP_NS:-wso2-amp}"
OBSERVABILITY_NS="${OBSERVABILITY_NS:-openchoreo-observability-plane}"
DEFAULT_NS="${DEFAULT_NS:-default}"
DATA_PLANE_NS="${DATA_PLANE_NS:-openchoreo-data-plane}"
THUNDER_NS="${THUNDER_NS:-amp-thunder}"
EVALUATION_NS="${EVALUATION_NS:-openchoreo-workflow-plane}"
BUILD_CI_NS="${BUILD_CI_NS:-openchoreo-workflow-plane}"

# Helm arguments arrays (initialize if not set)
if [[ -z "${AMP_HELM_ARGS+x}" ]]; then
    AMP_HELM_ARGS=()
fi
if [[ -z "${OBSERVABILITY_HELM_ARGS+x}" ]]; then
    OBSERVABILITY_HELM_ARGS=()
fi
if [[ -z "${PLATFORM_RESOURCES_HELM_ARGS+x}" ]]; then
    PLATFORM_RESOURCES_HELM_ARGS=()
fi
if [[ -z "${THUNDER_HELM_ARGS+x}" ]]; then
    THUNDER_HELM_ARGS=()
fi
if [[ -z "${EVALUATION_HELM_ARGS+x}" ]]; then
    EVALUATION_HELM_ARGS=()
fi
if [[ -z "${GATEWAY_HELM_ARGS+x}" ]]; then
    GATEWAY_HELM_ARGS=()
fi
if [[ -z "${CP_HELM_ARGS+x}" ]]; then
    CP_HELM_ARGS=()
fi

# Timeouts (in seconds)
TIMEOUT_AMP_INSTALL=1800
TIMEOUT_DEPLOYMENT=600

# ============================================================================
# HELPER FUNCTIONS
# ============================================================================

# Fallback logging functions (can be overridden by sourcing script)
if ! declare -f log_error >/dev/null 2>&1; then
    log_error() {
        echo "ERROR: $1" >&2
    }
fi

if ! declare -f log_warning >/dev/null 2>&1; then
    log_warning() {
        echo "WARNING: $1" >&2
    }
fi

# Check if helm release exists
helm_release_exists() {
    local release="$1"
    local namespace="$2"
    helm status "${release}" -n "${namespace}" &>/dev/null
}

# Wait for a deployment to be available
wait_for_deployment() {
    local deployment="$1"
    local namespace="$2"
    local timeout="${3:-600}"

    if kubectl wait --for=condition=Available deployment/"${deployment}" -n "${namespace}" --timeout="${timeout}s" &>/dev/null; then
        return 0
    else
        return 1
    fi
}

# Wait for statefulset to be ready
wait_for_statefulset() {
    local statefulset="$1"
    local namespace="$2"
    local timeout="${3:-600}"

    # Get the desired replica count
    local replicas
    replicas=$(kubectl get statefulset/"${statefulset}" -n "${namespace}" -o jsonpath='{.spec.replicas}' 2>/dev/null || echo "1")
 
    if kubectl wait --for=jsonpath="{.status.readyReplicas}"="${replicas}" statefulset/"${statefulset}" -n "${namespace}" --timeout="${timeout}s" &>/dev/null; then
        return 0
    else
        return 1
    fi
}

# Install helm chart with idempotency check
install_amp_helm_chart() {
    local release_name="$1"
    local chart_ref="$2"
    local namespace="$3"
    local timeout="${4:-1800}"
    shift 4
    local extra_args=("$@")

    # Check if release already exists
    if helm_release_exists "${release_name}" "${namespace}"; then
        return 0
    fi

    # Install the chart
    if helm install "${release_name}" "${chart_ref}" \
        --namespace "${namespace}" \
        --create-namespace \
        --timeout "${timeout}s" \
        "${extra_args[@]}"; then
        return 0
    else
        return 1
    fi
}

# ============================================================================
# INSTALLATION FUNCTIONS
# ============================================================================

# Install Agent Management Platform
install_agent_management_platform() {
    local chart_ref="oci://${HELM_CHART_REGISTRY}/${AMP_CHART_NAME}"
    local chart_version="${VERSION}"
    local release_name="amp"
    local helm_log="/tmp/helm-amp-install.log"

    # Install Helm chart
    if ! install_amp_helm_chart "${release_name}" "${chart_ref}" "${AMP_NS}" "${TIMEOUT_AMP_INSTALL}" \
        --version "${chart_version}" \
        --set console.config.instrumentationUrl="http://default-default.gateway.localhost:19080/otel" \
        "${AMP_HELM_ARGS[@]}" >"${helm_log}" 2>&1; then
        echo "Helm installation log (last 50 lines):"
        tail -50 "${helm_log}" 2>/dev/null || cat "${helm_log}" 2>/dev/null || echo "Log file not available"
        echo ""
        echo "Helm release status:"
        helm status "${release_name}" -n "${AMP_NS}" 2>&1 || echo "Release not found"
        echo ""
        echo "Pods in namespace ${AMP_NS}:"
        kubectl get pods -n "${AMP_NS}" 2>&1 || echo "No pods found"
        echo ""
        echo "Events in namespace ${AMP_NS}:"
        kubectl get events -n "${AMP_NS}" --sort-by='.lastTimestamp' | tail -20 2>&1 || true
        return 1
    fi

    # Wait for PostgreSQL StatefulSet (Bitnami subchart uses release-name-postgresql)
    if ! wait_for_statefulset "${release_name}-postgresql" "${AMP_NS}" "${TIMEOUT_DEPLOYMENT}"; then
        echo "PostgreSQL StatefulSet failed to become ready"
        echo ""
        echo "PostgreSQL pod status:"
        kubectl get pods -n "${AMP_NS}" -l app.kubernetes.io/name=postgresql 2>&1 || true
        echo ""
        echo "PostgreSQL StatefulSet status:"
        kubectl get statefulset "${release_name}-postgresql" -n "${AMP_NS}" 2>&1 || true
        echo ""
        echo "PostgreSQL pod logs (if available):"
        kubectl logs -n "${AMP_NS}" -l app.kubernetes.io/name=postgresql --tail=30 2>&1 || true
        return 1
    fi

    # Wait for agent manager service (amp-api)
    if ! wait_for_deployment "amp-api" "${AMP_NS}" "${TIMEOUT_DEPLOYMENT}"; then
        echo "Agent Manager Service deployment failed to become ready"
        echo ""
        echo "Agent Manager Service pod status:"
        kubectl get pods -n "${AMP_NS}" -l app.kubernetes.io/component=agent-manager-service 2>&1 || true
        echo ""
        echo "Agent Manager Service pod logs:"
        kubectl logs -n "${AMP_NS}" -l app.kubernetes.io/component=agent-manager-service --tail=50 2>&1 || true
        return 1
    fi

    # Wait for console (amp-console)
    if ! wait_for_deployment "amp-console" "${AMP_NS}" "${TIMEOUT_DEPLOYMENT}"; then
        echo "Console deployment failed to become ready"
        echo ""
        echo "Console pod status:"
        kubectl get pods -n "${AMP_NS}" -l app.kubernetes.io/component=console 2>&1 || true
        echo ""
        echo "Console pod logs:"
        kubectl logs -n "${AMP_NS}" -l app.kubernetes.io/component=console --tail=50 2>&1 || true
        return 1
    fi

    return 0
}

# Install Observability Extension
install_observability_extension() {
    local chart_ref="oci://${HELM_CHART_REGISTRY}/${OBSERVABILITY_CHART_NAME}"
    local chart_version="${VERSION}"
    local release_name="amp-observability-traces"

    # Install Helm chart
    if ! install_amp_helm_chart "${release_name}" "${chart_ref}" "${OBSERVABILITY_NS}" "${TIMEOUT_AMP_INSTALL}" \
        --version "${chart_version}" \
        "${OBSERVABILITY_HELM_ARGS[@]}"; then
        return 1
    fi

    # Wait for amp-observer if enabled
    if kubectl get deployment amp-observer -n "${OBSERVABILITY_NS}" &>/dev/null; then
        if ! wait_for_deployment "amp-observer" "${OBSERVABILITY_NS}" "${TIMEOUT_DEPLOYMENT}"; then
            echo "Agent Manager Observer deployment failed to become ready"
            echo ""
            echo "Agent Manager Observer pod status:"
            kubectl get pods -n "${OBSERVABILITY_NS}" -l app.kubernetes.io/component=observer 2>&1 || true
            return 1
        fi
    fi

    return 0
}

# Provision a per-environment Thunder instance for the default environment.
# Downloads add-environment-thunder.sh from the published release and runs it using
# ITS OWN default chart: the upstream ThunderID release chart
# (oci://ghcr.io/thunder-id/helm-charts/thunderid), NOT the agent-manager
# wso2-amp-thunder-extension chart. CHART_VERSION is intentionally left unset here
# so the script pins its own validated ThunderID version — the agent-manager
# release VERSION has no bearing on which ThunderID release env-Thunder runs.
install_default_env_thunder() {
    # Prefer the copy bundled next to the installer (DEPLOYMENTS_DIR is set by
    # install.sh). Fetching it from raw.githubusercontent.com is a fallback for
    # standalone use; GitHub rate-limits unauthenticated per-IP requests (429),
    # so avoid the network whenever the bundled script is present.
    local bundled_script="${DEPLOYMENTS_DIR:-}/scripts/add-environment-thunder.sh"
    local script_path tmp_script=""

    if [[ -n "${DEPLOYMENTS_DIR:-}" && -f "${bundled_script}" ]]; then
        script_path="${bundled_script}"
    else
        local script_url="https://raw.githubusercontent.com/wso2/agent-manager/amp/v${VERSION}/deployments/scripts/add-environment-thunder.sh"
        tmp_script="$(mktemp)"
        if ! curl -fsSL --connect-timeout 30 "${script_url}" -o "${tmp_script}" 2>/dev/null; then
            echo "Failed to download add-environment-thunder.sh from ${script_url}"
            rm -f "${tmp_script}"
            return 1
        fi
        script_path="${tmp_script}"
    fi

    ENV_NAME=default \
        DISPLAY_NAME="Default" \
        ORG_NAME=default \
        bash "${script_path}"
    local status=$?
    [[ -n "${tmp_script}" ]] && rm -f "${tmp_script}"
    return $status
}

# Install AMP Thunder Extension
install_amp_thunder_extension() {
    local chart_ref="oci://${HELM_CHART_REGISTRY}/${THUNDER_EXTENSION_CHART_NAME}"
    local chart_version="${VERSION}"
    local release_name="amp-thunder-extension"

    # Install Helm chart
    if ! install_amp_helm_chart "${release_name}" "${chart_ref}" "${THUNDER_NS}" "${TIMEOUT_AMP_INSTALL}" \
        --version "${chart_version}" \
        "${THUNDER_HELM_ARGS[@]}"; then
        return 1
    fi

    return 0
}

# Install Evaluation Extension
install_evaluation_extension() {
    local chart_ref="oci://${HELM_CHART_REGISTRY}/${EVALUATION_CHART_NAME}"
    local chart_version="${VERSION}"
    local release_name="amp-evaluation-extension"

    # Install Helm chart
    if ! install_amp_helm_chart "${release_name}" "${chart_ref}" "${EVALUATION_NS}" "${TIMEOUT_AMP_INSTALL}" \
        --version "${chart_version}" \
        "${EVALUATION_HELM_ARGS[@]}"; then
        return 1
    fi

    return 0
}

# Install Platform Resources Extension
install_platform_resources_extension() {
    local chart_ref="oci://${HELM_CHART_REGISTRY}/${PLATFORM_RESOURCES_CHART_NAME}"
    local chart_version="${VERSION}"
    local release_name="amp-platform-resources"

    # Install Helm chart
    if ! install_amp_helm_chart "${release_name}" "${chart_ref}" "${DEFAULT_NS}" "${TIMEOUT_AMP_INSTALL}" \
        --version "${chart_version}" \
        "${PLATFORM_RESOURCES_HELM_ARGS[@]}"; then
        return 1
    fi

    return 0
}

# Verify prerequisites for AMP installation
# Note: This function uses logging functions from the main script (log_error, log_warning)
verify_amp_prerequisites() {
    # Check if OpenChoreo Observability Plane is available
    if ! kubectl get namespace "${OBSERVABILITY_NS}" &>/dev/null; then
        log_error "OpenChoreo Observability Plane not found"
        echo ""
        echo "The Agent Management Platform requires OpenChoreo Observability Plane."
        echo "Please install it first."
        return 1
    fi

    # Check if OpenChoreo Workflow Plane is available
    if ! kubectl get namespace "${BUILD_CI_NS}" &>/dev/null; then
        log_error "OpenChoreo Workflow Plane not found"
        echo ""
        echo "The Agent Management Platform requires OpenChoreo Workflow Plane."
        echo "Please install it first."
        return 1
    fi

    # Verify OpenSearch is accessible
    if ! kubectl get pods -n "${OBSERVABILITY_NS}" -l app=opensearch &>/dev/null; then
        log_warning "OpenSearch pods not found in observability plane"
        log_warning "Installation may fail without OpenSearch"
    fi

    return 0
}

# Install API Platform Gateway Extension
# Installs wso2-amp-api-platform-gateway-extension which:
#   1. Runs a bootstrap Job to register the gateway in Agent Manager and generate a token
#   2. Deploys an APIGateway CR consumed by the gateway-operator to spin up the full stack
install_gateway_extension() {
    local chart_ref="oci://${HELM_CHART_REGISTRY}/${GATEWAY_EXTENSION_CHART_NAME}"
    local chart_version="${VERSION}"
    local release_name="api-platform-default-default"
    local gateway_vhost="http://default-default.gateway.localhost:19080"
    local idp_skip_tls_verify="${IDP_SKIP_TLS_VERIFY:-true}"
    # Per-org-env namespace isolation: the default env's gateway stack lives in
    # its own "<org>-<env>" namespace, mirroring add-environment.sh.
    local gateway_namespace="default-default"

    # Wire the gateway's ThunderKeyManager to the default environment's own Thunder
    # instance when it exists, mirroring the THUNDER_PROVISIONED logic in
    # add-environment.sh. keymanagers[0] is re-asserted alongside keymanagers[1]
    # because this install uses no -f values file, so --set on keymanagers[1]
    # alone would otherwise drop keymanagers[0] (verified via `helm template`).
    local thunder_args=()
    local thunder_release
    thunder_release="$(thunder_release_name default default)"
    if helm status "${thunder_release}" --namespace "${thunder_release}" &>/dev/null; then
        local thunder_issuer_url thunder_jwks
        thunder_issuer_url="$(thunder_issuer default default)"
        thunder_jwks="http://${thunder_release}-service.${thunder_release}.svc.cluster.local:8090/oauth2/jwks"
        thunder_args=(
            --set "apiGateway.config.policyConfigurations.jwtauth_v1.keymanagers[0].name=agent-manager-service"
            --set "apiGateway.config.policyConfigurations.jwtauth_v1.keymanagers[0].issuer=agent-manager-service"
            --set "apiGateway.config.policyConfigurations.jwtauth_v1.keymanagers[0].jwks.remote.uri=http://amp-api.wso2-amp.svc.cluster.local:9000/auth/external/jwks.json"
            --set "apiGateway.config.policyConfigurations.jwtauth_v1.keymanagers[0].jwks.remote.skipTlsVerify=true"
            --set "apiGateway.config.policyConfigurations.jwtauth_v1.keymanagers[1].name=ThunderKeyManager"
            --set "apiGateway.config.policyConfigurations.jwtauth_v1.keymanagers[1].issuer=${thunder_issuer_url}"
            --set "apiGateway.config.policyConfigurations.jwtauth_v1.keymanagers[1].jwks.remote.uri=${thunder_jwks}"
            --set "apiGateway.config.policyConfigurations.jwtauth_v1.keymanagers[1].jwks.remote.skipTlsVerify=${idp_skip_tls_verify}"
            # Name must match keymanagers[].name, which is always "ThunderKeyManager" (set above).
            --set "bootstrap.identityProviders[0].name=ThunderKeyManager"
            --set "bootstrap.identityProviders[0].issuer=${thunder_issuer_url}"
            --set "bootstrap.identityProviders[0].jwksUri=${thunder_jwks}"
            --set "bootstrap.identityProviders[0].skipTlsVerify=${idp_skip_tls_verify}"
        )
    fi

    # Install Helm chart. apiGateway.namespace drives where the chart renders
    # the APIGateway CR, config, RestApis, kgateway backendRef and token secret
    # — it must match the release namespace.
    if ! install_amp_helm_chart "${release_name}" "${chart_ref}" "${gateway_namespace}" "${TIMEOUT_AMP_INSTALL}" \
        --version "${chart_version}" \
        --set apiGateway.namespace="${gateway_namespace}" \
        --set agentManager.orgName=default \
        --set gateway.environment=default \
        --set gateway.vhost="${gateway_vhost}" \
        "${thunder_args[@]}" \
        "${GATEWAY_HELM_ARGS[@]}"; then
        return 1
    fi

    # Wait for the bootstrap job to complete (the Helm hook runs asynchronously)
    log_info "Waiting for gateway bootstrap job to complete..."
    if ! kubectl wait --for=condition=complete "job/${release_name}-bootstrap" \
        -n "${gateway_namespace}" --timeout=300s 2>/dev/null; then
        log_error "Gateway bootstrap job did not complete within 300s"
        return 1
    fi

    return 0
}

# ---------------------------------------------------------------------------
# Load the shared Thunder naming helpers (thunder_release_name/etc.) — the
# single source of truth for this derivation, see
# deployments/scripts/thunder-naming.sh. Always run from a checked-out repo
# (install.sh sources this file locally, never via curl | bash), so a plain
# relative source is enough — no network-fetch fallback needed here.
# ---------------------------------------------------------------------------
# shellcheck source=../scripts/thunder-naming.sh
source "$(dirname "${BASH_SOURCE[0]}")/../scripts/thunder-naming.sh"

