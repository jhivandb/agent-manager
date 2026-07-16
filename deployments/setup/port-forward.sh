#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/env.sh"

# Parse flags
PLATFORM=false
BACKGROUND=false

for arg in "$@"; do
    case "$arg" in
        --platform)   PLATFORM=true ;;
        --background) BACKGROUND=true ;;
    esac
done

# Default: all services if no group specified
if ! $PLATFORM; then
    PLATFORM=true
fi

# Check prerequisites
if ! command -v kubectl &> /dev/null; then
    echo "❌ kubectl is not installed"
    exit 1
fi

if ! kubectl cluster-info --context "$CLUSTER_CONTEXT" &> /dev/null; then
    echo "❌ k3d cluster '$CLUSTER_NAME' is not running"
    exit 1
fi

kubectl config use-context "$CLUSTER_CONTEXT"

# Stop any existing port-forwards before starting fresh
bash "$SCRIPT_DIR/stop-port-forward.sh" 2>/dev/null || true

echo ""
echo "=== Starting Port Forwarding ==="

# In interactive mode, kill background jobs on exit
cleanup() {
    echo ""
    echo "🛑 Stopping all port forwarding..."
    jobs -p | xargs kill 2>/dev/null || true
    exit 0
}
if ! $BACKGROUND; then
    trap cleanup EXIT INT TERM
fi

# PORT_FORWARD_ADDRESS optionally binds the forwards to a specific address.
# Unset = kubectl default (127.0.0.1). CI sets 0.0.0.0 so the agent-manager
# container (docker-compose) can reach them via the host/bridge gateway —
# a 127.0.0.1-only forward is unreachable from inside the container on Linux.
PF_ADDRESS="${PORT_FORWARD_ADDRESS:-}"

start_forward() {
    local desc="$1"; shift
    echo "   $desc"
    if [ -n "$PF_ADDRESS" ]; then
        kubectl port-forward --address "$PF_ADDRESS" "$@" > /dev/null 2>&1 &
    else
        kubectl port-forward "$@" > /dev/null 2>&1 &
    fi
}

if $PLATFORM; then
    echo "🔧 Platform services:"
    start_forward "Thunder IDP (8090)"               -n amp-thunder svc/amp-thunder-extension-service 8090:8090
    start_forward "OpenSearch (9200)"                -n openchoreo-observability-plane svc/opensearch 9200:9200
    start_forward "OpenTelemetry Collector (21893)"  -n openchoreo-observability-plane svc/opentelemetry-collector 21893:4318
    start_forward "Agent Manager Observer (9098)"    -n openchoreo-observability-plane svc/amp-observer 9098:9098
    start_forward "Observer API (8085)"              -n openchoreo-observability-plane svc/observer 8085:8080
    start_forward "OpenBao Workflow Plane (8200)"    -n openbao svc/openbao 8200:8200
    start_forward "OpenChoreo API (8195)"            -n openchoreo-control-plane svc/openchoreo-api 8195:8080
fi

echo ""
echo "✅ Active endpoints:"
if $PLATFORM; then
    echo "   Thunder IDP:              http://localhost:8090"
    echo "   Observer API:             http://localhost:8085"
    echo "   OpenSearch:               http://localhost:9200"
    echo "   Agent Manager Observer:   http://localhost:9098"
    echo "   OpenBao:     http://localhost:8200"
fi

echo ""
if $BACKGROUND; then
    echo "💡 Running in background — use 'make stop-port-forward' to stop"
else
    echo "💡 Keep this terminal open. Press Ctrl+C to stop."
    wait
fi
