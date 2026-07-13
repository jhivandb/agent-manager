<!--
  CONTRACT: Published agent skills (wso2/agent-skills) fetch this file at release
  tags via raw.githubusercontent.com/wso2/agent-manager/<tag>/deployments/AGENT_INSTALL.md.
  Do NOT move or rename it. Keep it version-agnostic: never write a literal release
  version; use v<X.Y.Z> placeholders. Update this file in the same PR as any change
  to deployments/quick-start/install.sh or deployments/vm/install-vm.sh.
-->

# Installing WSO2 Agent Manager (agent-facing guide)

Audience: a coding agent (Claude Code, Codex, …) installing the **released** version
`amp/v<X.Y.Z>` whose tag this file was fetched at. Substitute `<X.Y.Z>` everywhere below
with that version. Human-facing docs: https://wso2.github.io/agent-manager/docs/

## Choose an install path

- **Local machine** (laptop, workstation, private VM, no public hostname needed) →
  [Quick-start container](#path-a-local-machine-quick-start-container).
- **Linux VM with a static public IP** (needs real TLS and public URLs) →
  [VM installer](#path-b-linux-vm-with-public-ip). Linux-only.

Either path takes **15–25 minutes** of mostly unattended waiting. Run the installer in the
background or with a generous timeout — never a default 2-minute command timeout.

## Path A: local machine (quick-start container)

### Prerequisites

- Docker Engine 26.0+ running, with **at least 4 CPUs and 8 GB RAM** allocated.
- **macOS**: use Colima, not Docker Desktop. Start a dedicated profile:

  ```bash
  colima start --profile agent-manager \
    --vm-type=vz --vz-rosetta --network-address \
    --cpu 4 --memory 8
  ```

  Verify `docker ps` works afterwards (Colima sets the docker context).
- Several host ports must be free — the console and API use `8080`/`8443`, and the
  installer also needs a range of gateway, registry, and OpenSearch ports. You do not need
  to pre-check them all: the installer checks required ports early and **aborts listing any
  that are busy**. If it aborts on a port, free the conflicting process
  (`lsof -i :<port>`) and re-run.

### Install (non-interactive)

The documented human flow is an interactive shell; as an agent, run the container detached
and exec the installer instead. The image already targets its own release — do not set
`VERSION`.

```bash
docker run -d -t --name amp-quick-start \
  -v /var/run/docker.sock:/var/run/docker.sock \
  --network=host \
  ghcr.io/wso2/amp-quick-start:v<X.Y.Z>

docker exec -u wso2-amp -w /home/wso2-amp amp-quick-start \
  bash -lc './install.sh' > /tmp/amp-install.log 2>&1
```

The `-t` keeps the container's login shell alive; `bash -lc` makes the exec'd shell source
the kubeconfig setup in `.bashrc`. Tail `/tmp/amp-install.log` to monitor progress. The
installer is idempotent about transient races (it retries the control-plane webhook once
and pre-waits the data-plane certificate); if it exits non-zero, read the last ~50 log
lines before doing anything else.

### Verify health

All `kubectl` state lives inside the container:

```bash
docker exec -u wso2-amp amp-quick-start bash -lc '
  kubectl wait --for=jsonpath="{.status.readyReplicas}"=1 statefulset/amp-postgresql -n wso2-amp --timeout=120s &&
  kubectl wait --for=condition=Available deployment/amp-api -n wso2-amp --timeout=120s &&
  kubectl wait --for=condition=Available deployment/amp-console -n wso2-amp --timeout=120s &&
  if kubectl get deployment amp-traces-observer -n openchoreo-observability-plane >/dev/null 2>&1; then
    kubectl wait --for=condition=Available deployment/amp-traces-observer -n openchoreo-observability-plane --timeout=120s
  fi'
```

The traces-observer check is guarded because the installer treats the observability
extension as non-fatal — it may be absent on an otherwise healthy install, and an
unconditional wait would hang until timeout.

Then probe the endpoints from the host (the quick-start container runs with
`--network=host`, so the platform is reachable on plain `localhost`):

```bash
curl -fsS http://localhost:9000/api/v1/healthz
curl -fsS -o /dev/null -w "%{http_code}\n" http://localhost:3000
```

Success: the required `kubectl wait` calls pass, healthz returns success, console returns 200.

### Report to the user

- Console: `http://localhost:3000` — login `admin` / `admin`
- API: `http://localhost:9000`
- Traces (OTLP ingest): `http://localhost:22893/otel`

## Path B: Linux VM with public IP

### Prerequisites

- Linux VM: ≥4 vCPUs, 8 GB RAM, 50 GB disk, sudo/root access.
- A **static (reserved) public IPv4**. All hostnames, TLS certs, and OAuth issuers derive
  from it (`*.amp.<IP>.sslip.io`); a changed IP means reinstalling.
- Inbound **443** reachable as raw TCP (TLS certs use Let's Encrypt TLS-ALPN-01).

The installer bootstraps Docker, k3d, kubectl, and helm itself if missing.

### Install

```bash
curl -fsSL -o /tmp/install-vm.sh \
  "https://raw.githubusercontent.com/wso2/agent-manager/amp/v<X.Y.Z>/deployments/vm/install-vm.sh"
chmod +x /tmp/install-vm.sh
sudo /tmp/install-vm.sh --host <PUBLIC_IP> --version <X.Y.Z>
```

Optional flags: `--email <addr>` (ACME notifications), `--no-external-gateways`.

### Verify health

As root on the VM, run the same `kubectl wait` commands as Path A (without the
`docker exec` wrapper), then:

```bash
curl -fsS https://api.amp.<PUBLIC_IP>.sslip.io/api/v1/healthz
```

### Report to the user

- Console: `https://console.amp.<PUBLIC_IP>.sslip.io` — login `admin` / `admin`
- API: `https://api.amp.<PUBLIC_IP>.sslip.io`

## Install and configure amctl

```bash
curl -fsSL https://wso2.github.io/agent-manager/install.sh | sh
amctl version
```

Login is an **interactive browser flow — do not run it yourself**. Tell the user to run,
in their own terminal (new terminal if PATH just changed):

```bash
# Path A:
amctl login --url http://localhost:9000 --name local
# Path B:
amctl login --url https://api.amp.<PUBLIC_IP>.sslip.io --name <a-name>
```

Credentials: `admin` / `admin`. After the user confirms, verify auth with
`amctl project list --json` (NOT `amctl context show`, which looks identical
whether or not login succeeded).

## Known failure modes

| Symptom | Cause | Fix |
| --- | --- | --- |
| Installer aborts listing a port | Host port already bound | Free the port (`lsof -i :<port>`) and re-run `./install.sh` |
| `docker ps` permission denied | docker.sock perms | Installer attempts `sudo chmod 666 /var/run/docker.sock`; if sudo unavailable, have the user run that |
| Pods stuck `Pending`, installs time out | Docker VM under-resourced | Increase Colima/Docker resources to ≥4 CPU / 8 GB, re-run |
| "Data plane agent certificate not ready" | cert-manager slow | Re-run `./install.sh` — the wait guards a re-issuance race |
| "Control Plane webhook was not ready" then failure | Webhook race persisted past the built-in retry | Re-run `./install.sh` |
| Helm/registry fetch fails with 429 | GitHub rate limiting | No automatic retry — wait a few minutes and re-run the installer |
| VM URLs unreachable but pods healthy | :443 blocked or non-static IP | Open 443 as raw TCP; if the IP changed, reinstall |

Re-running `./install.sh` (or `install-vm.sh` with the same flags) is safe — the installer
skips already-installed components and re-applies the rest.

## Teardown

Path A: `docker exec -u wso2-amp amp-quick-start bash -lc './uninstall.sh'`, then
`docker rm -f amp-quick-start` (and `colima delete --profile agent-manager` on macOS).

## Next steps

Hand off to the `manage-agent` skill (same plugin) to create projects and deploy agents.
