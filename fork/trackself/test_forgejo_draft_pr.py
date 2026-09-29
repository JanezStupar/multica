"""Local transport tests for the bundled Forgejo draft PR helper."""

import importlib.util
import io
import json
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch
from urllib.error import URLError


SCRIPT = Path(__file__).parent / "policies/mica-v1/scripts/forgejo_draft_pr.py"
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("forgejo_draft_pr", SCRIPT)
helper = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = helper
spec.loader.exec_module(helper)

REPO_URL = "https://forge.example/team/repo"
SHA = "a" * 40


def pull(title="WIP: Improve widget", draft=True, sha=SHA, number=7):
    repo = {"full_name": "team/repo"}
    return {"number": number, "html_url": f"{REPO_URL}/pulls/{number}",
            "title": title, "draft": draft, "state": "open", "merged": False,
            "head": {"ref": "feature", "sha": sha, "repo": repo},
            "base": {"ref": "main", "repo": repo}}


class Response:
    def __init__(self, value, status=200):
        self.status = status
        self.stream = io.BytesIO(json.dumps(value).encode())

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.stream.close()

    def read(self, size=-1):
        return self.stream.read(size)


class FakeOpen:
    def __init__(self, *responses):
        self.responses = list(responses)
        self.calls = []

    def __call__(self, request, timeout):
        self.calls.append((request.get_method(), request.full_url,
                           json.loads(request.data) if request.data else None,
                           request.get_header("Authorization")))
        value = self.responses.pop(0)
        if isinstance(value, Exception):
            raise value
        return Response(value, 201 if request.get_method() == "POST" else 200)


class ForgejoDraftTest(unittest.TestCase):
    def setUp(self):
        self.repo = helper.parse_repo(REPO_URL)

    def client(self, fake):
        return helper.Forgejo(self.repo, "secret-token", opener=fake)

    def test_create_uses_wip_and_reads_back_exact_draft(self):
        fake = FakeOpen([], pull(), pull())
        result = helper.create(self.client(fake), "feature", "main", SHA,
                               "Improve widget", "Review this")
        self.assertEqual(result["pr_url"], REPO_URL + "/pulls/7")
        self.assertEqual(result["commit_sha"], SHA)
        self.assertTrue(result["draft"])
        self.assertEqual([call[0] for call in fake.calls], ["GET", "POST", "GET"])
        self.assertEqual(fake.calls[1][2], {"base": "main", "head": "feature",
                                            "title": "WIP: Improve widget", "body": "Review this"})
        self.assertEqual({call[3] for call in fake.calls}, {"token secret-token"})

    def test_existing_draft_is_preserved_without_mutation(self):
        fake = FakeOpen([pull(title="[WIP]: Existing")], [],
                        pull(title="[WIP]: Existing"))
        result = helper.create(self.client(fake), "feature", "main", SHA,
                               "Different suggested title", "")
        self.assertEqual(result["title"], "[WIP]: Existing")
        self.assertEqual([call[0] for call in fake.calls], ["GET", "GET", "GET"])

    def test_ensure_ready_pr_patches_only_title_after_exact_check(self):
        fake = FakeOpen(pull(title="Improve widget", draft=False), {}, pull())
        result = helper.ensure(self.client(fake), 7, "feature", "main", SHA)
        self.assertTrue(result["draft"])
        self.assertEqual([call[0] for call in fake.calls], ["GET", "PATCH", "GET"])
        self.assertEqual(fake.calls[1][2], {"title": "WIP: Improve widget"})

    def test_stale_head_reports_observed_commit_and_does_not_patch(self):
        observed = "b" * 40
        fake = FakeOpen(pull(title="Improve widget", draft=False, sha=observed))
        with self.assertRaisesRegex(helper.DraftError, observed):
            helper.ensure(self.client(fake), 7, "feature", "main", SHA)
        self.assertEqual([call[0] for call in fake.calls], ["GET"])

    def test_patch_must_read_back_draft(self):
        fake = FakeOpen(pull(title="Improve widget", draft=False), {},
                        pull(title="WIP: Improve widget", draft=False))
        with self.assertRaisesRegex(helper.DraftError, "did not report.*draft"):
            helper.ensure(self.client(fake), 7, "feature", "main", SHA)

    def test_head_change_after_patch_fails_readback(self):
        observed = "b" * 40
        fake = FakeOpen(pull(title="Improve widget", draft=False), {},
                        pull(sha=observed))
        with self.assertRaisesRegex(helper.DraftError, observed):
            helper.ensure(self.client(fake), 7, "feature", "main", SHA)
        self.assertEqual([call[0] for call in fake.calls], ["GET", "PATCH", "GET"])

    def test_closed_or_merged_pr_is_never_patched(self):
        for field, value in (("state", "closed"), ("merged", True)):
            with self.subTest(field=field):
                existing = pull(title="Improve widget", draft=False)
                existing[field] = value
                fake = FakeOpen(existing)
                with self.assertRaises(helper.DraftError):
                    helper.ensure(self.client(fake), 7, "feature", "main", SHA)
                self.assertEqual([call[0] for call in fake.calls], ["GET"])

    def test_branch_or_repo_mismatch_never_mutates(self):
        wrong = pull(title="Improve widget", draft=False)
        wrong["head"]["repo"] = {"full_name": "other/repo"}
        fake = FakeOpen(wrong)
        with self.assertRaisesRegex(helper.DraftError, "branch"):
            helper.ensure(self.client(fake), 7, "feature", "main", SHA)
        self.assertEqual([call[0] for call in fake.calls], ["GET"])

    def test_incomplete_list_identity_fails_before_create(self):
        listed = pull()
        listed["head"].pop("repo")
        fake = FakeOpen([listed])
        with self.assertRaisesRegex(helper.DraftError, "incomplete repository identity"):
            helper.create(self.client(fake), "feature", "main", SHA,
                          "Improve widget", "")
        self.assertEqual([call[0] for call in fake.calls], ["GET"])

    def test_ambiguous_create_reconciles_without_second_mutation(self):
        fake = FakeOpen([], URLError("connection lost"), [pull()], [], pull())
        result = helper.create(self.client(fake), "feature", "main", SHA,
                               "Improve widget", "")
        self.assertTrue(result["draft"])
        self.assertEqual([call[0] for call in fake.calls], ["GET", "POST", "GET", "GET", "GET"])

    def test_ambiguous_create_without_verified_pr_fails(self):
        fake = FakeOpen([], URLError("connection lost"), [])
        with self.assertRaisesRegex(helper.DraftError, "outcome uncertain"):
            helper.create(self.client(fake), "feature", "main", SHA,
                          "Improve widget", "")
        self.assertEqual([call[0] for call in fake.calls], ["GET", "POST", "GET"])

    def test_rejects_noncanonical_or_cross_origin_urls(self):
        for raw in ("http://forge.example/team/repo", "https://user@forge.example/team/repo",
                    "https://forge.example/team/repo/", "https://forge.example/team/%72epo",
                    "https://forge.example/team/repo?token=x",
                    "https://forge.example/team/repo\n"):
            with self.subTest(raw=raw), self.assertRaises(helper.DraftError):
                helper.parse_repo(raw)
        for raw in (REPO_URL + "/pulls/0", REPO_URL + "/pulls/7?x=1",
                    "https://other.example/team/repo/pulls/7"):
            with self.subTest(raw=raw), self.assertRaises(helper.DraftError):
                helper.parse_pr(self.repo, raw)

    def test_cli_rejects_untrusted_origin_before_network(self):
        with patch.dict(helper.os.environ, {"FORGEJO_URL": "https://trusted.example",
                                            "FORGEJO_TOKEN": "secret-token"}):
            with patch.object(helper, "Forgejo") as client:
                with patch("sys.stderr", new_callable=io.StringIO) as stderr:
                    status = helper.main(["ensure", "--repo-url", REPO_URL,
                                          "--pr-url", REPO_URL + "/pulls/7",
                                          "--head", "feature", "--base", "main", "--sha", SHA])
                self.assertEqual(status, 1)
                self.assertIn("differs from FORGEJO_URL", stderr.getvalue())
                self.assertNotIn("secret-token", stderr.getvalue())
                client.assert_not_called()

    def test_cli_body_file_preserves_multiline_literals(self):
        body = "First line\n\nLiteral `code` and $(text)\\n\r\nLast line\n"
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "description.md"
            path.write_bytes(body.encode("utf-8"))
            fake = FakeOpen([], pull(), pull())
            with patch.dict(helper.os.environ, {"FORGEJO_URL": "https://forge.example",
                                                "FORGEJO_TOKEN": "secret-token"}):
                with patch.object(helper, "build_opener", return_value=SimpleNamespace(open=fake)):
                    with redirect_stdout(io.StringIO()) as stdout:
                        status = helper.main(["create", "--repo-url", REPO_URL,
                                              "--head", "feature", "--base", "main", "--sha", SHA,
                                              "--title", "Improve widget", "--body-file", str(path)])
            self.assertEqual(status, 0)
            self.assertEqual(fake.calls[1][2]["body"], body)
            self.assertEqual(json.loads(stdout.getvalue())["pr_url"], REPO_URL + "/pulls/7")

    def test_cli_body_file_errors_are_safe(self):
        with tempfile.TemporaryDirectory() as temporary:
            path = Path(temporary) / "invalid.txt"
            path.write_bytes(b"\xff")
            with patch.dict(helper.os.environ, {"FORGEJO_URL": "https://forge.example",
                                                "FORGEJO_TOKEN": "secret-token"}):
                with patch.object(helper, "build_opener") as build:
                    with patch("sys.stderr", new_callable=io.StringIO) as stderr:
                        status = helper.main(["create", "--repo-url", REPO_URL,
                                              "--head", "feature", "--base", "main", "--sha", SHA,
                                              "--title", "Improve widget", "--body-file", str(path)])
            self.assertEqual(status, 1)
            self.assertIn("readable UTF-8", stderr.getvalue())
            self.assertNotIn("secret-token", stderr.getvalue())
            build.return_value.open.assert_not_called()

    def test_cli_body_and_body_file_are_mutually_exclusive(self):
        with patch("sys.stderr", new_callable=io.StringIO):
            with self.assertRaises(SystemExit) as caught:
                helper.main(["create", "--repo-url", REPO_URL,
                             "--head", "feature", "--base", "main", "--sha", SHA,
                             "--title", "Improve widget", "--body", "short",
                             "--body-file", "description.md"])
        self.assertEqual(caught.exception.code, 2)

    def test_redirect_handler_refuses_to_forward_token(self):
        with patch.object(helper, "build_opener") as build:
            helper.Forgejo(self.repo, "secret-token")
        handler = build.call_args.args[0]
        self.assertIsInstance(handler, helper.NoRedirect)
        request = helper.Request(self.repo.api, headers={"Authorization": "token secret-token"})
        with self.assertRaisesRegex(helper.DraftError, "redirected"):
            handler.redirect_request(request, None, 302, "Found", {},
                                     "https://evil.example/receive")


if __name__ == "__main__":
    unittest.main()
