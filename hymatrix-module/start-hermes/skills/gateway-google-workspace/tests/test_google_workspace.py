import importlib.util
import unittest
from pathlib import Path
from unittest.mock import patch
from urllib.parse import parse_qs, urlparse


SCRIPT = Path(__file__).parents[1] / "scripts" / "google_workspace.py"
SPEC = importlib.util.spec_from_file_location("gateway_google_workspace", SCRIPT)
google_workspace = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(google_workspace)


def query_params(url):
    return parse_qs(urlparse(url).query)


class SharedDriveUploadTest(unittest.TestCase):
    def test_upload_and_link_permission_support_shared_drives(self):
        requests = []

        def record_request(token, method, url, payload=None, content_type="application/json", raw=False):
            requests.append((method, url, payload))
            if "/permissions" in url:
                return {"id": "permission-id"}
            return {"id": "file-id", "name": "report.txt"}

        with patch.object(google_workspace, "api_request", side_effect=record_request):
            result = google_workspace.drive_upload_content(
                "token", "report.txt", b"hello", "text/plain", "shared-drive-folder-id"
            )

        self.assertEqual(result["id"], "file-id")
        self.assertEqual(len(requests), 2)
        self.assertEqual(query_params(requests[0][1]).get("supportsAllDrives"), ["true"])
        self.assertEqual(query_params(requests[1][1]).get("supportsAllDrives"), ["true"])


if __name__ == "__main__":
    unittest.main()
