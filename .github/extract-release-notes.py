#!/usr/bin/env python3
import os
import re

tag = os.environ.get("TAG", "")
version = tag.lstrip("v")
with open("CHANGELOG.md", encoding="utf-8") as f:
    content = f.read()

pattern = r"## \[" + re.escape(version) + r"\].*?\n(.*?)(?=\n## \[|\Z)"
m = re.search(pattern, content, re.DOTALL)
if m:
    with open("release-notes.md", "w", encoding="utf-8") as f:
        f.write(m.group(1).strip() + "\n")
