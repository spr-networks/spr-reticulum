#!/usr/bin/env bash
# update-python-pins.sh — regenerate requirements.txt with pinned versions and
# sha256 hashes for rns and ALL transitive dependencies.
#
# How it works:
#   1. pip resolves `rns==<version>` for the runtime environment (CPython 3.12 /
#      manylinux, i.e. the Ubuntu noble container template) and downloads the
#      exact wheels that pip would install there.
#   2. For every resolved name==version, the sha256 digests of *all* release
#      files are fetched from the PyPI JSON API (pip-compile style, so both
#      amd64 and arm64 wheels are accepted at build time).
#   3. The locally downloaded wheels are verified against those digests before
#      requirements.txt is written — a mismatch aborts.
#
# Usage: scripts/update-python-pins.sh [rns-version]
# Prints the pinned rns version on stdout (used by ../update-pins.sh).
set -euo pipefail
cd "$(dirname "$0")/.."

RNS_VERSION="${1:-$(curl -fsSL https://pypi.org/pypi/rns/json \
  | python3 -c 'import json,sys; print(json.load(sys.stdin)["info"]["version"])')}"

TMPDIR_PINS="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_PINS"' EXIT

echo "Resolving rns==${RNS_VERSION} for cp312/manylinux..." >&2
python3 -m pip download "rns==${RNS_VERSION}" --only-binary=:all: \
  --python-version 3.12 --implementation cp \
  --platform manylinux_2_34_x86_64 --platform manylinux2014_x86_64 \
  --platform manylinux_2_17_x86_64 \
  -d "$TMPDIR_PINS" -q

python3 - "$TMPDIR_PINS" "$RNS_VERSION" <<'EOF'
import glob, hashlib, json, os, re, sys, urllib.request

tmpdir, rns_version = sys.argv[1], sys.argv[2]
resolved = {}   # canonical name -> (version, {filename: local sha256})
for path in sorted(glob.glob(os.path.join(tmpdir, "*.whl"))):
    fn = os.path.basename(path)
    m = re.match(r"^([A-Za-z0-9_.]+)-([A-Za-z0-9_.!+]+)-", fn)
    if not m:
        raise SystemExit(f"cannot parse wheel filename: {fn}")
    name = m.group(1).replace("_", "-").lower()
    digest = hashlib.sha256(open(path, "rb").read()).hexdigest()
    resolved.setdefault(name, (m.group(2), {}))[1][fn] = digest

lines = [
    "# Fully pinned, hash-checked requirements for the spr-reticulum runtime.",
    f"# Generated on the build host by resolving rns=={rns_version} for cp312/manylinux",
    "# (see update-pins.sh / scripts/update-python-pins.sh to regenerate).",
    "# Install with: pip install --require-hashes --only-binary :all: -r requirements.txt",
    "",
]
for name in sorted(resolved):
    version, local_files = resolved[name]
    with urllib.request.urlopen(f"https://pypi.org/pypi/{name}/{version}/json") as r:
        data = json.load(r)
    pypi = {u["filename"]: u["digests"]["sha256"] for u in data["urls"]}
    for fn, digest in local_files.items():
        if pypi.get(fn) != digest:
            raise SystemExit(f"sha256 mismatch vs PyPI for {fn}")
    hashes = sorted(set(pypi.values()))
    entry = f"{name}=={version} \\\n"
    entry += " \\\n".join(f"    --hash=sha256:{h}" for h in hashes)
    lines.append(entry)

open("requirements.txt", "w").write("\n".join(lines) + "\n")
print(f"wrote requirements.txt ({len(resolved)} packages)", file=sys.stderr)
EOF

echo "$RNS_VERSION"
