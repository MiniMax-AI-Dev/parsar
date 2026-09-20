"""Run during image construction, before installing any Runtime or harness code."""
import hashlib
import json
from pathlib import Path
import tarfile


OUTPUT = Path('/opt/agents-runtime')
EXCLUDED = {'etc/hostname', 'etc/hosts', 'etc/resolv.conf', 'etc/machine-id',
            'etc/mtab', 'etc/shadow', 'etc/gshadow', 'opt/agents-runtime'}


def member(info):
    if any(info.name == path or info.name.startswith(path + '/') for path in EXCLUDED):
        return None
    return info


def main():
    OUTPUT.mkdir(mode=0o755)
    seed = OUTPUT / 'system-root.tar.gz'
    # Preserve the matching package database and all base tool symlink targets.
    with tarfile.open(seed, 'w:gz', compresslevel=1, dereference=False) as archive:
        for name in ('usr', 'bin', 'sbin', 'lib', 'lib64', 'opt', 'etc',
                     'var/lib/dpkg', 'var/lib/apt', 'var/cache/debconf'):
            path = Path('/') / name
            if path.exists() or path.is_symlink():
                archive.add(path, arcname=name, filter=member)
        for name in ('dev', 'proc', 'sys', 'tmp', 'run', 'home', 'root', 'workspace',
                     'environment', 'var/log', 'var/cache/apt/archives/partial',
                     'var/lib/apt/lists/partial'):
            info = tarfile.TarInfo(name)
            info.type, info.mode = tarfile.DIRTYPE, 0o755
            archive.addfile(info)
        for name in ('etc/hostname', 'etc/hosts', 'etc/resolv.conf', 'etc/machine-id'):
            archive.addfile(tarfile.TarInfo(name))
    seed.chmod(0o444)
    with seed.open('rb') as stream:
        digest = hashlib.file_digest(stream, 'sha256').hexdigest()
    manifest = OUTPUT / 'system-root.json'
    manifest.write_text(json.dumps({'version': 1, 'sha256': digest, 'size_bytes': seed.stat().st_size}) + '\n')
    manifest.chmod(0o444)


if __name__ == '__main__':
    main()
