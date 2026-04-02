# Agent Manager MCP

This MCP server is embedded in the Agent Manager service. It is exposed at `/mcp` and protected by JWT auth.

## Start the MCP server (local dev)

From the repo root:

```bash
make dev-up
```

The Agent Manager service is exposed on the host at:

```
http://localhost:9000
```

MCP endpoint:

```
http://localhost:9000/mcp
```

## Obtain a dev JWT (local)

In local dev, if `KEY_MANAGER_JWKS_URL` is **not** configured, the server accepts unsigned JWTs as long as:

- `iss` is `Agent Management Platform Local`
- `aud` includes `localhost`
- `exp` is in the future

Generate a 1-hour dev token:

```bash
python3 - <<'PY'
import base64, json, time
def b64(d):
    return base64.urlsafe_b64encode(json.dumps(d,separators=(',',':')).encode()).decode().rstrip('=')
header = {"alg":"none","typ":"JWT"}
payload = {
  "iss": "Agent Management Platform Local",
  "aud": ["localhost"],
  "exp": int(time.time()) + 86400,
  "scope": ""
}
print(f"{b64(header)}.{b64(payload)}.")
PY
```

If JWKS is configured, you must use a real token issued by the configured IdP.

## Verify MCP auth

```bash
curl -i http://localhost:9000/mcp
```

Expected (no token):

```
HTTP/1.1 401 Unauthorized
WWW-Authenticate: Bearer resource_metadata="http://localhost:9000/.well-known/oauth-protected-resource"
```

OAuth metadata endpoint:

```bash
curl http://localhost:9000/.well-known/oauth-protected-resource
```

## VS Code MCP config

Create `.vscode/mcp.json` in your workspace:

```json
{
  "servers": {
    "agent-manager": {
      "url": "http://localhost:9000/mcp",
      "type": "http",
      "headers": {
        "Authorization": "Bearer ${input:amp_jwt}"
      }
    }
  },
  "inputs": [
    {
      "id": "amp_jwt",
      "type": "promptString",
      "description": "Agent Manager JWT",
      "password": true
    }
  ]
}
```

Reload VS Code and provide the token when prompted.