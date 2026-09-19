#!/usr/bin/env python3
"""Package an already qualified Docker Runtime as a pinned E2B template build."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess
import tarfile
import tempfile

from e2b import Template

BASE = 'node:22.23.1-bookworm-slim@sha256:8607a9064d4a571140998ae9e52a3b3fcf9cff361d04642d5971e6cd76d39e27'
parser = argparse.ArgumentParser()
parser.add_argument('--image', required=True, help='Qualified linux/amd64 Runtime image digest')
parser.add_argument('--name', required=True)
parser.add_argument('--api-key-file', type=Path, required=True)
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
if not args.image.startswith('sha256:') or not args.output.is_absolute() or not args.api_key_file.is_absolute():
    parser.error('Use an image digest and absolute private key/output paths')
image = json.loads(subprocess.check_output(['docker', 'image', 'inspect', args.image]))[0]
if image['Architecture'] != 'amd64' or image['Os'] != 'linux':
    parser.error('A qualified Linux amd64 image is required')
environment = dict(value.split('=', 1) for value in image['Config']['Env']
                   if value.startswith(('HOME=', 'PARSAR_')))
if environment.get('PARSAR_RUNTIME_WORKSPACE') != '/environment/workspace':
    parser.error('Image does not use the colocated Runtime layout')
state = Path.home() / '.parsar/build/e2b'
state.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(dir=state) as temporary:
    context = Path(temporary)
    tree = context / 'runtime'
    tree.mkdir()
    container = subprocess.check_output(['docker', 'create', args.image], text=True).strip()
    try:
        for path in ['/usr/local/bin', '/usr/local/codex-resources', '/etc/codex', '/opt']:
            destination = tree / path.lstrip('/')
            destination.parent.mkdir(parents=True, exist_ok=True)
            with tempfile.TemporaryFile() as copied:
                result = subprocess.run(['docker', 'cp', container + ':' + path, '-'],
                                        stdout=copied, stderr=subprocess.PIPE)
                if result.returncode:
                    if path in ['/usr/local/codex-resources', '/etc/codex'] and b'Could not find the file' in result.stderr:
                        continue
                    raise RuntimeError('Cannot extract Runtime path: ' + path)
                copied.seek(0)
                with tarfile.open(fileobj=copied) as archive:
                    # Qualified images contain absolute native executable symlinks.
                    archive.extractall(destination.parent, filter='tar')
    finally:
        subprocess.run(['docker', 'rm', container], check=True, stdout=subprocess.DEVNULL)
    bundle = context / 'runtime.tar.gz'
    with tarfile.open(bundle, 'w:gz') as archive:
        for entry in tree.iterdir():
            archive.add(entry, arcname=entry.name)
    (context / 'runtime-env.json').write_text(json.dumps(environment))
    (context / 'init.py').write_bytes(Path(__file__).with_name('init.py').read_bytes())
    template = (Template(file_context_path=context).from_image(BASE)
                .run_cmd('apt-get update && apt-get install -y --no-install-recommends '
                         'ca-certificates bash git python3 ripgrep bubblewrap socat util-linux '
                         '&& rm -rf /var/lib/apt/lists/*', user='root')
                .copy('runtime.tar.gz', '/root/runtime.tar.gz', user='root')
                .copy('runtime-env.json', '/etc/parsar-runtime-env.json', user='root')
                .copy('init.py', '/opt/parsar-e2b/init.py', user='root')
                .run_cmd('tar --no-same-owner -xzf /root/runtime.tar.gz -C / && rm /root/runtime.tar.gz '
                         '&& usermod -l runtime -d /home/runtime node '
                         '&& mkdir -p /home/runtime/.parsar /environment/workspace /environment/staging /workspace '
                         '&& chown -R 1000:1000 /home/runtime /environment '
                         '&& chmod 0700 /home/runtime/.parsar /environment/staging '
                         '&& chmod 0444 /etc/parsar-runtime-env.json '
                         '&& chmod 0555 /opt/parsar-e2b /opt/parsar-e2b/init.py', user='root')
                .set_user('runtime').set_workdir('/environment/workspace'))
    result = Template.build(template, name=args.name, cpu_count=2, memory_mb=2048,
                            on_build_logs=lambda entry: print(entry.message, flush=True),
                            api_key=args.api_key_file.read_text().strip())
    report = {'template': result.template_id + ':' + result.build_id, 'image': image['Id'],
              'runtime_sha256': hashlib.sha256(bundle.read_bytes()).hexdigest(), 'base': BASE}
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report))
