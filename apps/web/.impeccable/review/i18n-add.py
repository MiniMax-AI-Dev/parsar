#!/usr/bin/env python3
"""Add or update i18n keys atomically (file-locked), so parallel page agents
never clobber each other's edits to the locale JSON files.

Usage:
  python3 .impeccable/review/i18n-add.py <namespace> <dotted.key> <en-US value> <zh-CN value>
  e.g. python3 .impeccable/review/i18n-add.py admin members.filter.label "Filter" "筛选"
Namespace is `admin` or `common`. Both locales are written in one locked pass.
"""
import collections
import fcntl
import json
import os
import sys

if len(sys.argv) != 5:
    sys.exit(__doc__)
ns, key, en, zh = sys.argv[1:]
root = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "src", "i18n", "locales")
lock_path = os.path.join(root, ".i18n.lock")
with open(lock_path, "w") as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    for loc, val in (("en-US", en), ("zh-CN", zh)):
        p = os.path.join(root, loc, f"{ns}.json")
        d = json.load(open(p, encoding="utf-8"), object_pairs_hook=collections.OrderedDict)
        node = d
        parts = key.split(".")
        for part in parts[:-1]:
            if part not in node or not isinstance(node[part], dict):
                node[part] = collections.OrderedDict()
            node = node[part]
        node[parts[-1]] = val
        json.dump(d, open(p, "w", encoding="utf-8"), ensure_ascii=False, indent=2)
        open(p, "a", encoding="utf-8").write("\n")
    fcntl.flock(lock, fcntl.LOCK_UN)
print(f"ok {ns}:{key}")
