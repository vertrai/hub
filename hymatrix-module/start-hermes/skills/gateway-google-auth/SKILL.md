---
name: gateway-google-auth
description: Obtain a short-lived Google OAuth access token for the Workspace user assigned by Hub Gateway. MUST use when the request explicitly needs a Google access token, OAuth token, bearer token, API token, Gmail API authorization, Drive API authorization, Google API credentials, 获取/申请 Google token、access token、访问令牌、API 令牌、Gmail/Drive API 授权. Use gateway-google-workspace instead for ordinary mailbox, email, Drive, file, folder, or document operations. Never start interactive OAuth or use local Google credentials.
---

# Gateway Google Auth

Configure the Gateway URL and API key, then run:

```bash
python3 scripts/google_auth.py
```

Required environment:

```bash
export HUB_GATEWAY_URL="https://gateway.example.com"
export HUB_GATEWAY_API_KEY="gw_sk_..."
```

The Gateway caches valid Google tokens and refreshes them near expiry. Request a token when needed instead of maintaining a refresh token in the Agent.

Use `--token-only` only when piping directly into a local process:

```bash
python3 scripts/google_auth.py --token-only
```

Keep tokens out of chat, logs, source control, and command-line arguments. Prefer `gateway-google-workspace` for Gmail and Drive operations so the token is not exposed to the model-facing workflow.

Completion criterion: verify the response has `accessToken`, `email`, and a future `expiresAt` before calling Google APIs.
