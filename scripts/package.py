#!/usr/bin/env python3
"""Build reproducible, standalone release archives. Does not tag, upload or publish."""
import argparse, gzip, hashlib, io, json, os, pathlib, re, subprocess, tarfile

parser = argparse.ArgumentParser()
parser.add_argument('--version', required=True)
parser.add_argument('--commit', required=True)
parser.add_argument('--output', default='dist')
args = parser.parse_args()
if not re.fullmatch(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', args.version):
    parser.error('version must be vX.Y.Z')
if not re.fullmatch(r'[0-9a-f]{40}', args.commit):
    parser.error('commit must be a full SHA')
root = pathlib.Path(__file__).resolve().parents[1]
out = pathlib.Path(args.output).resolve()
out.mkdir(parents=True, exist_ok=True)
checks = []
for system, arch in [('linux','amd64'), ('linux','arm64'), ('darwin','amd64'), ('darwin','arm64')]:
    name = f'mobterm-cli_{args.version}_{system}_{arch}.tar.gz'
    binary = out / f'.mobterm-cli-{system}-{arch}'
    env = dict(os.environ, GOOS=system, GOARCH=arch, CGO_ENABLED='0')
    subprocess.run(['go', 'build', '-trimpath', '-buildvcs=false', '-ldflags',
                    f'-s -w -X main.version={args.version} -X main.commit={args.commit}',
                    '-o', str(binary), '.'], cwd=root, env=env, check=True)
    metadata = json.dumps({'version':args.version,'commit':args.commit,'os':system,'arch':arch},sort_keys=True).encode()+b'\n'
    entries = [('mobterm-cli',binary.read_bytes(),0o755),('LICENSE',(root/'LICENSE').read_bytes(),0o644),
               ('README.md',(root/'README.md').read_bytes(),0o644),('THIRD_PARTY.md',(root/'THIRD_PARTY.md').read_bytes(),0o644),('build-info.json',metadata,0o644)]
    with (out/name).open('wb') as raw, gzip.GzipFile(fileobj=raw,mode='wb',mtime=0,filename='') as zipped:
        with tarfile.open(fileobj=zipped, mode='w') as archive:
            for entry, data, mode in entries:
                info = tarfile.TarInfo(entry)
                info.size,info.mode,info.mtime,info.uid,info.gid = len(data),mode,0,0,0
                archive.addfile(info,io.BytesIO(data))
    binary.unlink()
    with tarfile.open(out/name,'r:gz') as archive:
        assert archive.getnames()==[entry[0] for entry in entries]
        assert archive.getmember('mobterm-cli').mode==0o755
    checks.append(f'{hashlib.sha256((out/name).read_bytes()).hexdigest()}  {name}\n')
(out/'checksums.txt').write_text(''.join(checks))
print('Built and verified four archives with SHA-256 checksums; no release published.')
