#!/usr/bin/env python3
"""Create or verify one exact, open Forgejo draft PR using FORGEJO_TOKEN.

Only same-repository branches are supported. The provider's WIP: title prefix
marks a Forgejo PR as draft; the API's returned draft field is authoritative.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
from dataclasses import dataclass
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener


SEGMENT = re.compile(r"[A-Za-z0-9_.-]+\Z")
BRANCH = re.compile(r"[A-Za-z0-9._/-]+\Z")
SHA = re.compile(r"(?:[0-9a-fA-F]{40}|[0-9a-fA-F]{64})\Z")
MAX_RESPONSE = 1 << 20


class DraftError(Exception):
    """A safe-to-display failure with no provider response body or credential."""


@dataclass(frozen=True)
class Repo:
    url: str
    origin: str
    owner: str
    name: str

    @property
    def api(self) -> str:
        return f"{self.origin}/api/v1/repos/{self.owner}/{self.name}/pulls"

    def pull_url(self, number: int) -> str:
        return f"{self.url}/pulls/{number}"


def parse_repo(raw: str) -> Repo:
    if any(ord(char) < 32 or ord(char) == 127 for char in raw):
        raise DraftError("repository URL contains control characters")
    parsed = urlsplit(raw)
    parts = parsed.path.split("/")
    if (parsed.scheme != "https" or not parsed.hostname or parsed.username is not None
            or parsed.password is not None or parsed.query or parsed.fragment
            or len(parts) != 3 or parts[0] != ""
            or any(not SEGMENT.fullmatch(part) or part in (".", "..") for part in parts[1:])
            or parsed.netloc != parsed.netloc.lower()):
        raise DraftError("repository URL must be canonical HTTPS /owner/repo without credentials or suffix")
    try:
        port = parsed.port
    except ValueError as exc:
        raise DraftError("repository URL has an invalid port") from exc
    if port is not None and port <= 0:
        raise DraftError("repository URL has an invalid port")
    return Repo(raw, f"https://{parsed.netloc}", parts[1], parts[2])


def parse_origin(raw: str) -> str:
    if any(ord(char) < 32 or ord(char) == 127 for char in raw):
        raise DraftError("FORGEJO_URL contains control characters")
    parsed = urlsplit(raw)
    if (parsed.scheme != "https" or not parsed.hostname or parsed.username is not None
            or parsed.password is not None or parsed.path or parsed.query or parsed.fragment
            or parsed.netloc != parsed.netloc.lower()):
        raise DraftError("FORGEJO_URL must be a canonical HTTPS origin")
    try:
        port = parsed.port
    except ValueError as exc:
        raise DraftError("FORGEJO_URL has an invalid port") from exc
    if port is not None and port <= 0:
        raise DraftError("FORGEJO_URL has an invalid port")
    return raw


def parse_pr(repo: Repo, raw: str) -> int:
    prefix = repo.url + "/pulls/"
    if not raw.startswith(prefix) or not re.fullmatch(r"[1-9][0-9]*", raw[len(prefix):]):
        raise DraftError("PR URL must be a canonical pull URL in the supplied repository")
    return int(raw[len(prefix):])


def validate_inputs(head: str, base: str, sha: str) -> None:
    for value, label in ((head, "head"), (base, "base")):
        if (not BRANCH.fullmatch(value) or value.startswith("/") or value.endswith("/")
                or "//" in value or any(part in (".", "..") for part in value.split("/"))):
            raise DraftError(f"{label} must be a same-repository branch name")
    if head == base:
        raise DraftError("head and base branches must differ")
    if not SHA.fullmatch(sha):
        raise DraftError("sha must be a full 40- or 64-character hexadecimal commit")


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        raise DraftError("Forgejo redirected the API request")


class Forgejo:
    def __init__(self, repo: Repo, token: str, opener=None):
        if not token or any(char in token for char in "\r\n\x00"):
            raise DraftError("FORGEJO_TOKEN is missing or malformed")
        self.repo = repo
        self.token = token
        self.opener = opener or build_opener(NoRedirect()).open

    def request(self, method: str, url: str, payload=None):
        if not url.startswith(self.repo.api) or (url != self.repo.api and
                not url.startswith(self.repo.api + "/") and
                not url.startswith(self.repo.api + "?")):
            raise DraftError("API endpoint is outside the supplied repository")
        data = None if payload is None else json.dumps(payload).encode("utf-8")
        req = Request(url, data=data, method=method, headers={
            "Authorization": "token " + self.token,
            "Accept": "application/json",
            **({"Content-Type": "application/json"} if data is not None else {}),
        })
        try:
            with self.opener(req, timeout=15) as response:
                status = response.status
                raw = response.read(MAX_RESPONSE + 1)
        except HTTPError as exc:
            # Provider bodies may echo request content or credentials.
            raise DraftError(f"Forgejo {method} returned HTTP {exc.code}") from None
        except (URLError, OSError, TimeoutError) as exc:
            raise DraftError(f"Forgejo {method} outcome is uncertain; inspect the exact PR before retrying") from None
        if status < 200 or status >= 300:
            raise DraftError(f"Forgejo {method} returned HTTP {status}")
        if len(raw) > MAX_RESPONSE:
            raise DraftError("Forgejo response exceeds size limit")
        try:
            return json.loads(raw)
        except (UnicodeDecodeError, ValueError):
            raise DraftError("Forgejo returned malformed JSON") from None

    def read(self, number: int):
        return self.request("GET", f"{self.repo.api}/{number}")

    def find_exact(self, head: str, base: str):
        matches = []
        for page in range(1, 21):
            query = urlencode({"state": "open", "limit": 50, "page": page})
            pulls = self.request("GET", f"{self.repo.api}?{query}")
            if not isinstance(pulls, list):
                raise DraftError("Forgejo returned an invalid PR list")
            if not pulls:
                break
            for pull in pulls:
                if not isinstance(pull, dict):
                    raise DraftError("Forgejo returned an invalid PR list")
                listed_head, listed_base = pull.get("head"), pull.get("base")
                if (isinstance(listed_head, dict) and listed_head.get("ref") == head
                        and isinstance(listed_base, dict) and listed_base.get("ref") == base
                        and (not isinstance(listed_head.get("repo"), dict)
                             or not isinstance(listed_base.get("repo"), dict))):
                    raise DraftError("matching branch pair has incomplete repository identity")
                if _branch_matches(pull.get("head"), self.repo, head) and _branch_matches(pull.get("base"), self.repo, base):
                    number = pull.get("number")
                    if type(number) is not int or number < 1 or pull.get("html_url") != self.repo.pull_url(number):
                        raise DraftError("Forgejo returned a matching PR with invalid identity")
                    matches.append(number)
        else:
            raise DraftError("too many open PRs to prove uniqueness")
        if len(matches) > 1:
            raise DraftError("multiple open PRs match the branch pair")
        return matches[0] if matches else None


def _branch_matches(value, repo: Repo, branch: str) -> bool:
    if not isinstance(value, dict) or value.get("ref") != branch:
        return False
    branch_repo = value.get("repo")
    return isinstance(branch_repo, dict) and branch_repo.get("full_name") == f"{repo.owner}/{repo.name}"


def check_identity(pull, repo: Repo, number: int, head: str, base: str, sha: str) -> None:
    if (not isinstance(pull, dict) or type(pull.get("number")) is not int
            or pull["number"] != number or pull.get("html_url") != repo.pull_url(number)):
        raise DraftError("PR read-back identity differs from the requested repository and URL")
    if pull.get("state") != "open" or pull.get("merged") is not False:
        raise DraftError("PR is closed or merged")
    if not _branch_matches(pull.get("head"), repo, head) or not _branch_matches(pull.get("base"), repo, base):
        raise DraftError("PR read-back branch or repository differs from requested head/base")
    observed_sha = pull["head"].get("sha")
    if not isinstance(observed_sha, str) or not SHA.fullmatch(observed_sha):
        raise DraftError("PR read-back has an invalid head commit")
    if observed_sha.lower() != sha.lower():
        raise DraftError(f"PR head is {observed_sha.lower()}, expected {sha.lower()}")
    if not isinstance(pull.get("title"), str) or not pull["title"].strip():
        raise DraftError("PR read-back has no title")


def verify(pull, repo: Repo, number: int, head: str, base: str, sha: str) -> dict:
    check_identity(pull, repo, number, head, base, sha)
    if pull.get("draft") is not True:
        raise DraftError("Forgejo did not report the PR as draft")
    return {"repository_url": repo.url, "pr_url": repo.pull_url(number), "branch": head,
            "base": base, "commit_sha": sha.lower(), "draft": True, "title": pull["title"]}


def ensure(client: Forgejo, number: int, head: str, base: str, sha: str) -> dict:
    pull = client.read(number)
    # Verify identity and revision before any title mutation.
    check_identity(pull, client.repo, number, head, base, sha)
    if type(pull.get("draft")) is not bool:
        raise DraftError("PR read-back has no draft field")
    if pull["draft"]:
        return verify(pull, client.repo, number, head, base, sha)
    if pull["title"].lower().startswith("wip:"):
        raise DraftError("Forgejo does not recognize WIP: as draft on this instance")
    client.request("PATCH", f"{client.repo.api}/{number}", {"title": "WIP: " + pull["title"]})
    return verify(client.read(number), client.repo, number, head, base, sha)


def create(client: Forgejo, head: str, base: str, sha: str, title: str, body: str) -> dict:
    if not title.strip() or title != title.strip() or "\n" in title or "\r" in title:
        raise DraftError("title must be one non-empty line without outer whitespace")
    existing = client.find_exact(head, base)
    if existing is not None:
        return ensure(client, existing, head, base, sha)
    draft_title = title if title.lower().startswith("wip:") else "WIP: " + title
    try:
        created = client.request("POST", client.repo.api, {"base": base, "head": head,
                                                            "title": draft_title, "body": body})
    except DraftError as exc:
        # A timed-out POST may already have created the PR. Reconcile once,
        # with no second mutation and no automatic retry.
        try:
            found = client.find_exact(head, base)
            if found is not None:
                return verify(client.read(found), client.repo, found, head, base, sha)
        except DraftError:
            pass
        raise DraftError(f"PR creation outcome uncertain ({exc}); inspect Forgejo before retrying") from None
    number = created.get("number") if isinstance(created, dict) else None
    if type(number) is not int or number < 1 or created.get("html_url") != client.repo.pull_url(number):
        raise DraftError("Forgejo creation response had an invalid PR identity; inspect before retrying")
    return verify(client.read(number), client.repo, number, head, base, sha)


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    for command in ("create", "ensure"):
        item = commands.add_parser(command)
        item.add_argument("--repo-url", required=True)
        item.add_argument("--head", required=True)
        item.add_argument("--base", required=True)
        item.add_argument("--sha", required=True)
        if command == "create":
            item.add_argument("--title", required=True)
            body = item.add_mutually_exclusive_group()
            body.add_argument("--body")
            body.add_argument("--body-file")
        else:
            item.add_argument("--pr-url", required=True)
    args = parser.parse_args(argv)
    try:
        repo = parse_repo(args.repo_url)
        if repo.origin != parse_origin(os.environ.get("FORGEJO_URL", "")):
            raise DraftError("repository origin differs from FORGEJO_URL")
        validate_inputs(args.head, args.base, args.sha)
        number = parse_pr(repo, args.pr_url) if args.command == "ensure" else None
        client = Forgejo(repo, os.environ.get("FORGEJO_TOKEN", ""))
        body = ""
        if args.command == "create":
            if args.body_file is not None:
                try:
                    with Path(args.body_file).open("rb") as stream:
                        raw = stream.read(MAX_RESPONSE + 1)
                    if len(raw) > MAX_RESPONSE:
                        raise DraftError("PR body file exceeds size limit")
                    body = raw.decode("utf-8")
                except (OSError, UnicodeDecodeError):
                    raise DraftError("PR body file must be readable UTF-8 text") from None
            elif args.body is not None:
                body = args.body
        result = (ensure(client, number, args.head, args.base, args.sha) if number is not None
                  else create(client, args.head, args.base, args.sha, args.title, body))
        print(json.dumps(result, sort_keys=True))
        return 0
    except DraftError as exc:
        print(f"forgejo draft PR: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
