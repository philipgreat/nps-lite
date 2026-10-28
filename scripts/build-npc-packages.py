#!/usr/bin/env python3
"""Locally cross-compile six 64-bit NPCs into generic, credential-free ZIPs."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import zipfile

root = Path(__file__).resolve().parents[1]
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--output', type=Path, default=root/'dist/client-binaries')
args = parser.parse_args()
out = args.output.resolve()
out.mkdir(parents=True, exist_ok=True)
manifest = {'schema': 1, 'source_commit': subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(), 'source_dirty': bool(subprocess.check_output(['git','status','--porcelain'],cwd=root,text=True).strip()), 'packages': []}
with tempfile.TemporaryDirectory(prefix='npc-build-') as tmp:
    for platform in ('windows','darwin','linux'):
        for arch in ('amd64','arm64'):
            name = 'npc.exe' if platform=='windows' else 'npc'
            binary = Path(tmp)/name
            env = dict(os.environ, CGO_ENABLED='0', GOOS=platform, GOARCH=arch)
            subprocess.run(['go','build','-trimpath','-buildvcs=false','-ldflags=-s -w','-o',str(binary),'./cmd/npc/npc.go'],cwd=root,env=env,check=True)
            data = binary.read_bytes()
            archive = out/f'npc_{platform}_{arch}.zip'
            info = zipfile.ZipInfo(name, date_time=(2026,1,1,0,0,0))
            info.create_system = 3
            info.external_attr = ((0o100755 if platform!='windows' else 0o100644)<<16)
            info.compress_type = zipfile.ZIP_DEFLATED
            with zipfile.ZipFile(archive,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=6) as z:
                z.writestr(info,data,compresslevel=6)
            manifest['packages'].append({'platform':platform,'arch':arch,'file':archive.name,'sha256':hashlib.sha256(archive.read_bytes()).hexdigest(),'binary_sha256':hashlib.sha256(data).hexdigest(),'binary_size':len(data)})
            print(f'Built {archive.name} ({archive.stat().st_size} bytes)',flush=True)
(out/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
print('Artifacts:',out,flush=True)
