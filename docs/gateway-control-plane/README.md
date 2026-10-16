# Gateway control plane: onboarding and alignment

agent-manager-service drives the same gateway-controller that api-platform's platform-api drives, through a copy of platform-api's control-plane code that has drifted. These documents explain how the shared pieces work and what it takes to bring agent-manager back in line. As of 2026-10-16.

Paths in the onboarding guides point into the [wso2/api-platform](https://github.com/wso2/api-platform) repository unless they say otherwise.

| Document | What it covers |
| --- | --- |
| [gateway-sync-explainer.md](gateway-sync-explainer.md) | Start here. How a deploy reaches a gateway, why missed events persist until a controller restart, how agent-manager differs from platform-api, and five changes that fix it |
| [platform-api.md](platform-api.md) | platform-api as a control plane: how it runs, its public and gateway APIs, the WebSocket, the SQL event hub, the data model and key flows |
| [gateway-controller.md](gateway-controller.md) | The gateway-controller: the contract any control plane must implement, sync and reconciliation, its local data model, and its xDS feeds |
| [policy-engine.md](policy-engine.md) | The policy engine in the request path: processes, ext_proc, the xDS resources it receives, route identity, policies and failure behaviour |
| [agent-manager-divergence.md](agent-manager-divergence.md) | Side-by-side comparison of agent-manager-service and platform-api, the consequences on the gateway today, and a prioritised alignment plan |

The A2A gateway sync redesign that motivated this work is in [../superpowers/specs/2026-10-16-a2a-gateway-sync-design.md](../superpowers/specs/2026-10-16-a2a-gateway-sync-design.md) and its plan in [../superpowers/plans/2026-10-16-a2a-gateway-sync.md](../superpowers/plans/2026-10-16-a2a-gateway-sync.md).
