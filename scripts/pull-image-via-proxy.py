#!/usr/bin/env python3
"""Pull a Docker Hub image through the shell's HTTP proxy and emit a
docker-archive tarball for `docker load`.

The Docker daemon on this host has no DNS/proxy of its own, so `docker
pull` cannot reach a registry; curl/urllib can, via http_proxy. This
speaks the Registry HTTP API v2 directly and writes the legacy
docker-save layout (config json + one layer.tar per layer + manifest.json),
which `docker load` accepts.

Usage: pull-image-via-proxy.py library/postgres 16-alpine /tmp/postgres-16-alpine.tar
"""
import gzip
import hashlib
import json
import os
import shutil
import sys
import tarfile
import tempfile
import urllib.request

AUTH = "https://auth.docker.io/token?service=registry.docker.io&scope=repository:{repo}:pull"
REGISTRY = "https://registry-1.docker.io/v2/{repo}"
MANIFEST_ACCEPT = ", ".join([
    "application/vnd.docker.distribution.manifest.v2+json",
    "application/vnd.docker.distribution.manifest.list.v2+json",
    "application/vnd.oci.image.manifest.v1+json",
    "application/vnd.oci.image.index.v1+json",
])


def get(url, token=None, accept=None, binary=False):
    req = urllib.request.Request(url)
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    if accept:
        req.add_header("Accept", accept)
    with urllib.request.urlopen(req, timeout=300) as r:
        data = r.read()
    return data if binary else json.loads(data)


def stream(url, token, dest):
    req = urllib.request.Request(url)
    req.add_header("Authorization", f"Bearer {token}")
    with urllib.request.urlopen(req, timeout=900) as r, open(dest, "wb") as f:
        shutil.copyfileobj(r, f, 1024 * 1024)


def main(repo, tag, out):
    token = get(AUTH.format(repo=repo))["token"]
    man = get(f"{REGISTRY.format(repo=repo)}/manifests/{tag}", token, MANIFEST_ACCEPT)

    if man.get("mediaType", "").endswith(("manifest.list.v2+json", "image.index.v1+json")):
        amd64 = next(
            m for m in man["manifests"]
            if m["platform"]["os"] == "linux" and m["platform"]["architecture"] == "amd64"
        )
        print(f"multi-arch: picking linux/amd64 {amd64['digest'][:19]}")
        man = get(f"{REGISTRY.format(repo=repo)}/manifests/{amd64['digest']}", token, MANIFEST_ACCEPT)

    work = tempfile.mkdtemp(prefix="imgpull-")
    cfg_digest = man["config"]["digest"]
    cfg = get(f"{REGISTRY.format(repo=repo)}/blobs/{cfg_digest}", token, binary=True)
    cfg_name = cfg_digest.split(":")[1] + ".json"
    with open(os.path.join(work, cfg_name), "wb") as f:
        f.write(cfg)

    layer_paths = []
    for i, layer in enumerate(man["layers"], 1):
        digest = layer["digest"]
        gz = os.path.join(work, digest.split(":")[1] + ".gz")
        print(f"layer {i}/{len(man['layers'])} {digest[:19]} {layer['size'] // 1048576}MB")
        stream(f"{REGISTRY.format(repo=repo)}/blobs/{digest}", token, gz)
        got = hashlib.sha256(open(gz, "rb").read()).hexdigest()
        assert got == digest.split(":")[1], f"digest mismatch for {digest}"
        d = os.path.join(work, digest.split(":")[1])
        os.makedirs(d, exist_ok=True)
        # docker load wants uncompressed layer tars in the legacy layout.
        with gzip.open(gz, "rb") as src, open(os.path.join(d, "layer.tar"), "wb") as dst:
            shutil.copyfileobj(src, dst, 1024 * 1024)
        os.remove(gz)
        layer_paths.append(f"{digest.split(':')[1]}/layer.tar")

    short = repo.split("/", 1)[1] if repo.startswith("library/") else repo
    with open(os.path.join(work, "manifest.json"), "w") as f:
        json.dump([{"Config": cfg_name, "RepoTags": [f"{short}:{tag}"], "Layers": layer_paths}], f)

    with tarfile.open(out, "w") as tar:
        for name in sorted(os.listdir(work)):
            tar.add(os.path.join(work, name), arcname=name)
    shutil.rmtree(work)
    print("wrote", out, os.path.getsize(out) // 1048576, "MB")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2], sys.argv[3])
