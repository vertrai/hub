---
name: gateway-google-auth
description: Obtain a short-lived Google OAuth access token for the Workspace user assigned by Hub Gateway. MUST use when the request explicitly needs a Google access token, OAuth token, bearer token, API token, Gmail API authorization, Drive API authorization, Google API credentials, 获取/申请 Google token、access token、访问令牌、API 令牌、Gmail/Drive API 授权. Use gateway-google-workspace instead for ordinary mailbox, email, Drive, file, folder, or document operations. Never start interactive OAuth or use local Google credentials.
---

# Gateway Google Auth

Configure the Gateway URL and API key, then run:

```bash
python3 scripts/google_auth.py
```

The access-token endpoint always issues a token for the Google user already assigned to the API key. Account purpose is used only when the account is first acquired; do not pass a purpose when requesting a token.

Required environment:

```bash
export HUB_GATEWAY_URL="https://gateway.example.com"
export HUB_GATEWAY_API_KEY="gw_sk_..."
```

The Gateway caches valid Google tokens and refreshes them near expiry. Request a token when needed instead of maintaining a refresh token in the Agent.

The CLI never prints raw tokens or account email. Local code that genuinely needs a token must import `gateway_token()` from the helper and keep the returned value in-process. Keep tokens out of chat, logs, source control, files, and command-line arguments. Prefer `gateway-google-workspace` for Gmail and Drive operations so the token is not exposed to the model-facing workflow.

The default output is safe metadata and never contains the token or email. Completion criterion: verify `tokenAvailable`, `account: assigned`, and a future `expiresAt`.
