import importlib.util
import io
import json
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest.mock import MagicMock, patch


SCRIPT = Path(__file__).parents[1] / "scripts" / "google_auth.py"
SPEC = importlib.util.spec_from_file_location("gateway_google_auth", SCRIPT)
google_auth = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(google_auth)


class SafeOutputTest(unittest.TestCase):
    def test_default_output_never_contains_access_token(self):
        response = MagicMock()
        response.__enter__.return_value.read.return_value = b'{"accessToken":"secret-token","email":"bot@example.com","expiresAt":"2099-01-01T00:00:00Z"}'
        output = io.StringIO()
        with (
            patch.object(google_auth.urllib.request, "urlopen", return_value=response) as urlopen,
            patch.object(google_auth, "gateway_credentials", return_value=("https://hub.example", "gateway-key")),
            patch.object(google_auth.sys, "argv", ["google_auth.py"]),
            redirect_stdout(output),
        ):
            google_auth.main()
        result = json.loads(output.getvalue())
        self.assertNotIn("secret-token", output.getvalue())
        self.assertNotIn("bot@example.com", output.getvalue())
        self.assertTrue(result["tokenAvailable"])
        self.assertNotIn("purpose=", urlopen.call_args.args[0].full_url)


if __name__ == "__main__":
    unittest.main()
