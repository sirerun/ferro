#!/usr/bin/env python3
"""Verify the frozen bulk contract lock against repository file bytes."""

from __future__ import annotations

import hashlib
import json
import re
import sys
from pathlib import Path, PurePosixPath, PureWindowsPath
from typing import Any


LOCK_PATH = Path(__file__).with_name("bulk-v2.lock.json")
HASH_RE = re.compile(r"^[0-9a-fA-F]{64}$")
REVISION_RE = re.compile(r"^[0-9a-fA-F]{40}$")


class LockError(Exception):
    """An actionable lock validation error without exposing file contents."""


def _object_without_duplicate_keys(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise LockError("lock contains duplicate JSON keys")
        result[key] = value
    return result


def _read_lock(path: Path) -> Any:
    try:
        raw = path.read_bytes()
    except OSError:
        raise LockError("lock file is missing or unreadable") from None
    try:
        return json.loads(raw, object_pairs_hook=_object_without_duplicate_keys)
    except (UnicodeDecodeError, json.JSONDecodeError):
        raise LockError("lock file is not valid JSON") from None


def _validate_shape(lock: Any) -> tuple[int, list[dict[str, str]]]:
    if not isinstance(lock, dict):
        raise LockError("lock must be an object")
    if lock.get("schema") != "ferro.bulk-contract-lock/v1":
        raise LockError("unsupported lock schema")
    revision = lock.get("revision")
    if not isinstance(revision, int) or isinstance(revision, bool) or revision < 1:
        raise LockError("lock revision must be a positive integer")
    base = lock.get("source_base_revision")
    if not isinstance(base, str) or not REVISION_RE.fullmatch(base):
        raise LockError("source_base_revision must be a 40-character hexadecimal revision")

    files = lock.get("files")
    if not isinstance(files, list):
        raise LockError("files must be an array")
    seen_paths: set[str] = set()
    normalized: list[dict[str, str]] = []
    for entry in files:
        if not isinstance(entry, dict):
            raise LockError("each files entry must be an object")
        file_path, digest = entry.get("path"), entry.get("sha256")
        if not isinstance(file_path, str) or not file_path:
            raise LockError("each files entry needs a path")
        posix = PurePosixPath(file_path)
        windows = PureWindowsPath(file_path)
        if posix.is_absolute() or windows.is_absolute() or windows.drive or ".." in posix.parts:
            raise LockError("lock contains an unsafe file path")
        if not isinstance(digest, str) or not HASH_RE.fullmatch(digest):
            raise LockError("file sha256 must be a 64-character hexadecimal digest")
        key = posix.as_posix()
        if key in seen_paths:
            raise LockError("lock contains a duplicate file path")
        seen_paths.add(key)
        normalized.append({"path": file_path, "sha256": digest.lower()})

    signatures = lock.get("signatures")
    if not isinstance(signatures, dict) or any(
        not isinstance(packet, str)
        or not isinstance(items, list)
        or any(not isinstance(signature, str) or not signature for signature in items)
        for packet, items in signatures.items()
    ):
        raise LockError("signatures must map packet names to signature strings")
    if not isinstance(lock.get("import_direction"), dict):
        raise LockError("import_direction must be an object")
    checks = lock.get("checks")
    if not isinstance(checks, list) or any(not isinstance(check, str) for check in checks):
        raise LockError("checks must be an array of strings")
    return revision, normalized


def verify() -> tuple[int, int]:
    repo_root = Path(__file__).resolve().parents[2]
    lock = _read_lock(LOCK_PATH)
    revision, files = _validate_shape(lock)
    resolved_root = repo_root.resolve()
    resolved_files: set[Path] = set()

    for entry in files:
        candidate = repo_root / entry["path"]
        try:
            resolved = candidate.resolve(strict=True)
        except OSError:
            raise LockError("locked file is missing or unreadable") from None
        if not resolved.is_relative_to(resolved_root):
            raise LockError("locked file resolves outside the repository")
        if resolved in resolved_files:
            raise LockError("lock contains duplicate resolved file paths")
        resolved_files.add(resolved)
        if not resolved.is_file():
            raise LockError("locked path is not a regular file")
        try:
            actual = hashlib.sha256(resolved.read_bytes()).hexdigest()
        except OSError:
            raise LockError("locked file is unreadable") from None
        if actual != entry["sha256"]:
            raise LockError("locked file digest mismatch")
    return len(files), revision


def main() -> int:
    try:
        count, revision = verify()
    except LockError as exc:
        print(f"bulk contract lock: {exc}", file=sys.stderr)
        return 1
    print(f"verified {count} files (revision {revision})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
