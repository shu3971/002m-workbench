#!/usr/bin/env python3
"""Zip a macOS .app bundle while preserving executable permissions.

Usage:
    python tools/package_macos_zip.py bin/002m-workbench.app out.zip

Windows zip tools often store Unix mode 0000, which makes the app binary
non-executable after unzip on macOS. This writer explicitly sets 0755 for
Contents/MacOS files and 0644 for everything else.
"""

from __future__ import annotations

import os
import stat
import sys
import zipfile
from pathlib import Path


def add_dir(zf: zipfile.ZipFile, name: str) -> None:
    info = zipfile.ZipInfo(name)
    info.create_system = 3
    info.external_attr = (stat.S_IFDIR | 0o755) << 16
    zf.writestr(info, b"")


def add_file(zf: zipfile.ZipFile, path: Path, name: str, executable: bool) -> None:
    info = zipfile.ZipInfo(name)
    info.create_system = 3
    info.external_attr = (stat.S_IFREG | (0o755 if executable else 0o644)) << 16
    info.compress_type = zipfile.ZIP_DEFLATED
    with path.open("rb") as handle:
        zf.writestr(info, handle.read())


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: package_macos_zip.py <app_dir> <out.zip>", file=sys.stderr)
        return 2

    app = Path(sys.argv[1]).resolve()
    out = Path(sys.argv[2]).resolve()
    if not app.is_dir():
        print(f"app bundle not found: {app}", file=sys.stderr)
        return 1

    if out.exists():
        out.unlink()

    with zipfile.ZipFile(out, "w") as zf:
        base = app.name
        add_dir(zf, base + "/")
        for dirpath, dirnames, filenames in os.walk(app):
            dirnames.sort()
            filenames.sort()
            rel_dir = Path(dirpath).relative_to(app).as_posix()
            if rel_dir != ".":
                add_dir(zf, f"{base}/{rel_dir}/")

            for filename in filenames:
                path = Path(dirpath) / filename
                rel = path.relative_to(app).as_posix()
                executable = rel.startswith("Contents/MacOS/")
                add_file(zf, path, f"{base}/{rel}", executable)

    print(f"packaged {app.name} -> {out} ({out.stat().st_size} bytes)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
