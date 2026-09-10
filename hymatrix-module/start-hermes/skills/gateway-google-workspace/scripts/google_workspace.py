#!/usr/bin/env python3
"""Gmail and Drive operations backed by Hub Gateway credentials."""

import argparse
import base64
import json
import mimetypes
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
import uuid
from email.message import EmailMessage
from pathlib import Path


GMAIL_API = "https://gmail.googleapis.com/gmail/v1/users/me"
DRIVE_API = "https://www.googleapis.com/drive/v3"


def configured_value(name):
    return os.environ.get(name, "").strip() or hermes_env_value(name)


def gateway_credentials():
    current = configured_value("HUB_GATEWAY_URL"), configured_value("HUB_GATEWAY_API_KEY")
    if all(current):
        return current
    legacy = configured_value("AGENT_ACCESS_GATEWAY_URL"), configured_value("AGENT_ACCESS_GATEWAY_API_KEY")
    if all(legacy):
        return legacy
    raise RuntimeError("HUB_GATEWAY_URL and HUB_GATEWAY_API_KEY are required as a complete pair")


def hermes_env_value(name):
    path = Path(os.environ.get("HERMES_HOME", str(Path.home() / ".hermes"))) / ".env"
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError:
        return ""
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("export "):
            stripped = stripped[7:].lstrip()
        if stripped.startswith(name + "="):
            return stripped.split("=", 1)[1].strip().strip('"\'')
    return ""


def gateway_token():
    base_url, api_key = gateway_credentials()
    base_url = base_url.rstrip("/")
    request = urllib.request.Request(base_url + "/v1/google-user/access-token")
    request.add_header("Authorization", "Bearer " + api_key)
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            data = json.loads(response.read().decode("utf-8") or "{}")
    except urllib.error.HTTPError as error:
        raise_api_error("gateway", error)
    token = data.get("accessToken", "")
    if not token:
        raise RuntimeError("gateway response does not contain an access token")
    return token, data.get("email", "")


def raise_api_error(service, error):
    body = error.read().decode("utf-8", errors="replace")
    try:
        detail = json.loads(body).get("error", body)
    except Exception:
        detail = body
    raise RuntimeError(f"{service} returned HTTP {error.code}: {detail}") from error


def api_request(token, method, url, payload=None, content_type="application/json", raw=False):
    body = None
    if payload is not None:
        body = payload if isinstance(payload, bytes) else json.dumps(payload).encode("utf-8")
    request = urllib.request.Request(url, data=body, method=method)
    request.add_header("Authorization", "Bearer " + token)
    if body is not None:
        request.add_header("Content-Type", content_type)
    try:
        with urllib.request.urlopen(request, timeout=90) as response:
            data = response.read()
            if raw:
                return data
            return json.loads(data.decode("utf-8") or "{}")
    except urllib.error.HTTPError as error:
        raise_api_error("Google API", error)


def with_query(url, **values):
    query = {key: value for key, value in values.items() if value is not None and value != ""}
    return url + ("?" + urllib.parse.urlencode(query) if query else "")


def message_body(args):
    if getattr(args, "body_file", ""):
        return Path(args.body_file).read_text(encoding="utf-8")
    return getattr(args, "body", "")


def encoded_message(sender, args):
    message = EmailMessage()
    if sender:
        message["From"] = sender
    message["To"] = args.to
    message["Subject"] = args.subject
    message.set_content(message_body(args))
    return base64.urlsafe_b64encode(message.as_bytes()).decode("ascii").rstrip("=")


def gmail_profile(token, _email, _args):
    return api_request(token, "GET", GMAIL_API + "/profile")


def gmail_list(token, _email, args):
    return api_request(token, "GET", with_query(GMAIL_API + "/messages", q=args.query, maxResults=args.max_results))


def gmail_get(token, _email, args):
    message_id = urllib.parse.quote(args.message_id, safe="")
    return api_request(token, "GET", with_query(GMAIL_API + "/messages/" + message_id, format=args.format))


def gmail_send(token, email, args):
    return api_request(token, "POST", GMAIL_API + "/messages/send", {"raw": encoded_message(email, args)})


def gmail_draft(token, email, args):
    return api_request(token, "POST", GMAIL_API + "/drafts", {"message": {"raw": encoded_message(email, args)}})


def drive_fields():
    return "id,name,mimeType,size,createdTime,modifiedTime,webViewLink,webContentLink,parents,trashed"


def drive_list(token, _email, args):
    fields = "nextPageToken,files(" + drive_fields() + ")"
    return api_request(
        token,
        "GET",
        with_query(
            DRIVE_API + "/files",
            q=args.query,
            pageSize=args.page_size,
            fields=fields,
            supportsAllDrives="true",
            includeItemsFromAllDrives="true",
        ),
    )


def drive_get(token, _email, args):
    file_id = urllib.parse.quote(args.file_id, safe="")
    return api_request(
        token,
        "GET",
        with_query(DRIVE_API + "/files/" + file_id, fields=drive_fields(), supportsAllDrives="true"),
    )


def drive_share_anyone_reader(token, file):
    file_id = str(file.get("id", "")).strip()
    if not file_id:
        raise RuntimeError("Drive response does not contain a file id")
    encoded_id = urllib.parse.quote(file_id, safe="")
    permission = api_request(
        token,
        "POST",
        with_query(
            DRIVE_API + "/files/" + encoded_id + "/permissions",
            fields="id,type,role",
            supportsAllDrives="true",
        ),
        {"type": "anyone", "role": "reader"},
    )
    result = dict(file)
    result["sharing"] = {
        "permissionId": permission.get("id", ""),
        "type": "anyone",
        "role": "reader",
    }
    return result


def drive_create_folder(token, _email, args):
    payload = {"name": args.name, "mimeType": "application/vnd.google-apps.folder"}
    if args.parent_id:
        payload["parents"] = [args.parent_id]
    created = api_request(
        token,
        "POST",
        with_query(DRIVE_API + "/files", fields=drive_fields(), supportsAllDrives="true"),
        payload,
    )
    return drive_share_anyone_reader(token, created)


def multipart_body(metadata, content, mime_type):
    boundary = "gateway-" + uuid.uuid4().hex
    chunks = [
        f"--{boundary}\r\nContent-Type: application/json; charset=UTF-8\r\n\r\n".encode(),
        json.dumps(metadata).encode("utf-8"),
        f"\r\n--{boundary}\r\nContent-Type: {mime_type}\r\n\r\n".encode(),
        content,
        f"\r\n--{boundary}--\r\n".encode(),
    ]
    return b"".join(chunks), "multipart/related; boundary=" + boundary


def drive_upload_content(token, name, content, mime_type, parent_id=""):
    metadata = {"name": name}
    if parent_id:
        metadata["parents"] = [parent_id]
    body, content_type = multipart_body(metadata, content, mime_type)
    url = with_query(
        "https://www.googleapis.com/upload/drive/v3/files",
        uploadType="multipart",
        fields=drive_fields(),
        supportsAllDrives="true",
    )
    created = api_request(token, "POST", url, body, content_type)
    return drive_share_anyone_reader(token, created)


def drive_create_text(token, _email, args):
    content = args.content
    if args.content_file:
        content = Path(args.content_file).read_text(encoding="utf-8")
    return drive_upload_content(token, args.name, content.encode("utf-8"), "text/plain; charset=UTF-8", args.parent_id)


def drive_upload(token, _email, args):
    path = Path(args.path)
    if not path.is_file():
        raise RuntimeError(f"file not found: {path}")
    mime_type = args.mime_type or mimetypes.guess_type(path.name)[0] or "application/octet-stream"
    return drive_upload_content(token, args.name or path.name, path.read_bytes(), mime_type, args.parent_id)


def drive_share_link(token, _email, args):
    file_id = urllib.parse.quote(args.file_id, safe="")
    file = api_request(
        token,
        "GET",
        with_query(DRIVE_API + "/files/" + file_id, fields=drive_fields(), supportsAllDrives="true"),
    )
    return drive_share_anyone_reader(token, file)


def drive_download(token, _email, args):
    file_id = urllib.parse.quote(args.file_id, safe="")
    content = api_request(
        token,
        "GET",
        with_query(DRIVE_API + "/files/" + file_id, alt="media", supportsAllDrives="true"),
        raw=True,
    )
    output = Path(args.output)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_bytes(content)
    return {"fileId": args.file_id, "output": str(output.resolve()), "bytes": len(content)}


def drive_delete(token, _email, args):
    file_id = urllib.parse.quote(args.file_id, safe="")
    api_request(token, "DELETE", with_query(DRIVE_API + "/files/" + file_id, supportsAllDrives="true"))
    return {"fileId": args.file_id, "deleted": True}


def add_mail_fields(parser):
    parser.add_argument("--to", required=True)
    parser.add_argument("--subject", required=True)
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("--body")
    group.add_argument("--body-file")


def build_parser():
    parser = argparse.ArgumentParser(description="Operate Gmail and Drive through Hub Gateway")
    commands = parser.add_subparsers(dest="command", required=True)

    profile = commands.add_parser("gmail-profile")
    profile.set_defaults(func=gmail_profile)
    listing = commands.add_parser("gmail-list")
    listing.add_argument("--query", default="")
    listing.add_argument("--max-results", type=int, default=20)
    listing.set_defaults(func=gmail_list)
    get_message = commands.add_parser("gmail-get")
    get_message.add_argument("--message-id", required=True)
    get_message.add_argument("--format", choices=("minimal", "full", "raw", "metadata"), default="full")
    get_message.set_defaults(func=gmail_get)
    send = commands.add_parser("gmail-send")
    add_mail_fields(send)
    send.set_defaults(func=gmail_send)
    draft = commands.add_parser("gmail-draft")
    add_mail_fields(draft)
    draft.set_defaults(func=gmail_draft)

    listing = commands.add_parser("drive-list")
    listing.add_argument("--query", default="trashed = false")
    listing.add_argument("--page-size", type=int, default=50)
    listing.set_defaults(func=drive_list)
    get_file = commands.add_parser("drive-get")
    get_file.add_argument("--file-id", required=True)
    get_file.set_defaults(func=drive_get)
    folder = commands.add_parser("drive-create-folder")
    folder.add_argument("--name", required=True)
    folder.add_argument("--parent-id", default="")
    folder.set_defaults(func=drive_create_folder)
    text = commands.add_parser("drive-create-text")
    text.add_argument("--name", required=True)
    text.add_argument("--parent-id", default="")
    text_group = text.add_mutually_exclusive_group(required=True)
    text_group.add_argument("--content")
    text_group.add_argument("--content-file")
    text.set_defaults(func=drive_create_text)
    upload = commands.add_parser("drive-upload")
    upload.add_argument("--path", required=True)
    upload.add_argument("--name", default="")
    upload.add_argument("--mime-type", default="")
    upload.add_argument("--parent-id", default="")
    upload.set_defaults(func=drive_upload)
    share = commands.add_parser("drive-share-link")
    share.add_argument("--file-id", required=True)
    share.set_defaults(func=drive_share_link)
    download = commands.add_parser("drive-download")
    download.add_argument("--file-id", required=True)
    download.add_argument("--output", required=True)
    download.set_defaults(func=drive_download)
    delete = commands.add_parser("drive-delete")
    delete.add_argument("--file-id", required=True)
    delete.set_defaults(func=drive_delete)
    return parser


def main():
    args = build_parser().parse_args()
    token, email = gateway_token()
    result = args.func(token, email, args)
    print(json.dumps({"email": email, "result": result}, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(json.dumps({"error": str(error)}, ensure_ascii=False), file=sys.stderr)
        sys.exit(1)
