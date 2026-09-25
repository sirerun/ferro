#!/usr/bin/env python3
"""Run the Go validators over the checked-in bulk v2 contract fixtures."""

import os
import subprocess
import sys


def main() -> int:
    env = os.environ.copy()
    env["GOWORK"] = "off"
    return subprocess.run(
        ["go", "test", "./internal/mcp", "-run", "^TestContractFixtures$", "-count=1"],
        env=env,
        check=False,
    ).returncode


if __name__ == "__main__":
    sys.exit(main())
