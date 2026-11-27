#!/usr/bin/env python3
"""Build the bundled case snapshot for 002M Workbench.

Reads the approved candidate ids from nanobanana-site's
tools/mvp_001/fixtures/allowlist.json, merges every cases_*.json fixture,
and writes a deterministic snapshot to frontend/dist/data/cases.json.

Usage:
  python tools/build_cases.py /path/to/nanobanana-site/tools/mvp_001/fixtures
"""
import glob
import json
import os
import sys

FIELDS = ("title", "category", "tags", "image",
          "prompt_zh", "prompt_en", "remix_variables", "strict_ban")


def load_cases(path):
    data = json.load(open(path, encoding="utf-8"))
    if isinstance(data, list):
        return data
    if isinstance(data, dict):
        for key in ("cases", "cards", "items"):
            if isinstance(data.get(key), list):
                return data[key]
    return []


def main(fixtures):
    allow = json.load(open(os.path.join(fixtures, "allowlist.json"), encoding="utf-8"))
    approved = list(dict.fromkeys(allow.get("approved_candidate_ids") or []))
    approved_set = set(approved)

    # Non-draft files win over draft files; later duplicates are ignored.
    paths = sorted(glob.glob(os.path.join(fixtures, "cases_*.json")))
    paths = [p for p in paths if not p.endswith("_draft.json")] + \
            [p for p in paths if p.endswith("_draft.json")]

    by_id = {}
    for path in paths:
        for item in load_cases(path):
            cid = (item.get("candidate_id") or "").strip()
            if not cid or cid not in approved_set or cid in by_id:
                continue
            row = {"id": cid}
            for field in FIELDS:
                value = item.get(field)
                if field in ("tags", "remix_variables") and value is None:
                    value = []
                row[field] = value
            if row.get("category") == "人像":
                row["category"] = "portrait"
            if not (row.get("image") or "").startswith("/"):
                row["image"] = "/" + (row.get("image") or "").lstrip("/")
            by_id[cid] = row

    cases = [by_id[cid] for cid in approved if cid in by_id]
    missing = [cid for cid in approved if cid not in by_id]
    categories = {}
    tags = {}
    for case in cases:
        categories[case.get("category") or "uncategorized"] = \
            categories.get(case.get("category") or "uncategorized", 0) + 1
        for tag in case.get("tags") or []:
            tags[tag] = tags.get(tag, 0) + 1

    out = {
        "schema_version": "1.0",
        "source": "nanobanana-site/tools/mvp_001/fixtures (allowlist approved only)",
        "approved_count": len(approved),
        "count": len(cases),
        "missing_ids": missing,
        "categories": dict(sorted(categories.items())),
        "tags": dict(sorted(tags.items(), key=lambda kv: (-kv[1], kv[0]))),
        "cases": cases,
    }
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    dest = os.path.join(root, "frontend", "dist", "data", "cases.json")
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    with open(dest, "w", encoding="utf-8") as f:
        json.dump(out, f, ensure_ascii=False, separators=(",", ":"))
    print("approved=%d built=%d missing=%d -> %s" %
          (len(approved), len(cases), len(missing), dest))
    print("categories:", json.dumps(out["categories"], ensure_ascii=False))
    if missing:
        print("missing ids:", ",".join(missing))


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print(__doc__)
        sys.exit(2)
    main(sys.argv[1])
