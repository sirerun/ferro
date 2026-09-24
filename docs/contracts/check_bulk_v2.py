#!/usr/bin/env python3
"""Check fixture JSON parses and its declared valid/invalid classification."""
import json
from pathlib import Path

root = Path(__file__).with_name("bulk-v2")
expected = {
    "request-valid.json": True, "request-invalid.json": False,
    "result-success.json": True, "result-failure.json": True,
    "profile.json": True, "receipt.json": True, "usage.json": True,
}
for name, is_valid in expected.items():
    value = json.loads((root / name).read_text(encoding="utf-8"))
    if not isinstance(value, dict):
        raise SystemExit(f"{name}: expected object fixture")
    if name == "request-invalid.json" and "unexpected" not in value:
        raise SystemExit(f"{name}: missing deliberate invalid field")
    print(f"{name}: {'invalid' if not is_valid else 'valid'} JSON fixture")
