#!/usr/bin/env python3

from __future__ import annotations

import re
import sys
from pathlib import Path


SPDX_LICENSE = "SPDX-License-Identifier: MIT"
SPDX_LICENSE = "SPDX-License-Identifier: Apache-2.0"
COPYRIGHT_RE = re.compile(r"SPDX-FileCopyrightText:\s+\d{4}(?:-\d{4})?\s+Luckfox Team\b")
HEADER_SCAN_LINES = 10


def has_required_header(path: Path) -> bool:
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except UnicodeDecodeError:
        return False
    head = "\n".join(lines[:HEADER_SCAN_LINES])
    return SPDX_LICENSE in head and COPYRIGHT_RE.search(head) is not None


def main(argv: list[str]) -> int:
    missing: list[str] = []

    for name in argv[1:]:
        path = Path(name)
        if not path.is_file():
            continue
        if not has_required_header(path):
            missing.append(name)

    if not missing:
        return 0

    print("Missing MIT SPDX header in:")
    for name in missing:
        print(name)
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
