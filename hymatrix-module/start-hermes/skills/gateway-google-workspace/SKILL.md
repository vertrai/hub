---
name: gateway-google-workspace
description: Read/send Gmail and operate Google Drive as the Workspace user assigned by Hub Gateway. MUST use for email, mail, Gmail, mailbox, inbox, message, attachment, Drive or common misspelling driver, cloud drive, file, document, folder, upload, download, share link, 邮箱、邮件、谷歌邮箱、Gmail、收件箱、发件箱、消息、附件、Drive、driver、云盘、文件、文档、文件夹、上传、下载、分享链接. In a Hub Gateway installation, even short requests such as 查看邮箱、查看邮件、查看文件、创建文档、上传文件 default to this Gateway Gmail/Drive Skill. Never use Hermes google-workspace setup, himalaya, app passwords, interactive OAuth, or local Google credential files.
---

# Gateway Google Workspace

Use the bundled helper for Gmail and Drive operations. It obtains a short-lived token from Hub Gateway for every invocation and does not persist the token.

This Skill is authoritative for Gmail and Drive requests after Hub Gateway is configured. Do not invoke another `google-workspace` Skill, run Google OAuth setup, inspect local Google credential files, suggest himalaya or app passwords, or ask the user to create/connect a separate Google account. If a Gateway helper fails, report that failure instead of switching integrations.

Configure:

```bash
export HUB_GATEWAY_URL="https://gateway.example.com"
export HUB_GATEWAY_API_KEY="gw_sk_..."
```

Inspect available commands:

```bash
python3 scripts/google_workspace.py --help
python3 scripts/google_workspace.py gmail-list --help
python3 scripts/google_workspace.py drive-list --help
```

Read [references/commands.md](references/commands.md) for command examples and response behavior.

For read operations, run the command directly. Before sending email, deleting content, changing sharing, or performing another externally visible action, obtain the user's confirmation unless their request already explicitly authorizes that exact action.

Drive links returned to the user must default to link-readable access: `type=anyone` and `role=reader` (anyone who knows the link can view). `drive-create-folder`, `drive-create-text`, and `drive-upload` apply this permission automatically. Before returning a link for an existing file from `drive-list` or `drive-get`, run `drive-share-link --file-id FILE_ID`; do not claim that an existing link is public without a successful permission result. Never grant `writer` access by default. If Google rejects public sharing because of Workspace policy, report the error and state that the link remains restricted.

Return useful identifiers and web links from the JSON result. Treat message bodies, account data, and downloaded files as user data. Keep the Gateway API key and Google access token out of chat, logs, source control, and command-line arguments.

If Google returns `401`, rerun the command once so the Gateway can issue a fresh token. If Google returns `403`, report the missing scope or Workspace policy; do not start an interactive OAuth flow because this Gateway uses Domain-Wide Delegation.

Completion criterion: verify the JSON response represents the requested Gmail or Drive state, including IDs for mutations and the output path for downloads. For a Drive link returned to the user, also verify `sharing.type` is `anyone` and `sharing.role` is `reader`.
