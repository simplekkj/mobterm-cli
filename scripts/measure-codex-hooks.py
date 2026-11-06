#!/usr/bin/env python3
"""Measure actual CLI children against private, synthetic local fixtures.

Never reads user Codex/MobTerm configuration or calls a model/production API.
The Herdr server is a protocol fixture, not a real Herdr application. Only the
real-tmux scenario starts a terminal server, on its own temporary socket.
"""
import argparse
import base64
import concurrent.futures
import csv
import hashlib
import http.server
import json
import math
import os
from pathlib import Path
import platform
import socketserver
import subprocess
import tempfile
import threading
import time

INSTALLATION = 'measure_installation_0001'


def write_json(path, value):
    path.write_text(json.dumps(value))
    path.chmod(0o600)


def percentile(values, quantile):
    return sorted(values)[max(0, math.ceil(len(values) * quantile) - 1)]


class Fixture:
    def __init__(self, binary, root, mode='normal', pairs=1, tmux=False):
        self.binary, self.root, self.mode = binary, root, mode
        self.store = root / 'store'
        self.store.mkdir(mode=0o700)
        self.lock = threading.Lock()
        self.http_count = self.target_queries = self.http_active = self.http_max = 0
        self.worker_max = 0
        self.stop = threading.Event()
        self.rows = []
        self.env = dict(os.environ)
        for name in ('TMUX', 'TMUX_PANE', 'HERDR_PANE_ID', 'HERDR_SESSION', 'HERDR_SOCKET_PATH'):
            self.env.pop(name, None)
        self.env.update(MOBTERM_CONFIG_DIR=str(self.store), XDG_CONFIG_HOME=str(root), MOBTERM_NETWORK_DIAGNOSTICS='1')
        owner = self

        class API(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                self.respond()

            do_POST = do_PUT = do_GET

            def respond(self):
                with owner.lock:
                    owner.http_count += 1
                    owner.http_active += 1
                    owner.http_max = max(owner.http_max, owner.http_active)
                try:
                    data = json.loads(self.rfile.read(int(self.headers.get('Content-Length', 0))) or b'{}')
                    if owner.mode == 'slow':
                        time.sleep(0.3)
                    pair = self.path.split('/')[3]
                    if self.path.endswith('/capabilities'):
                        body = dict(v=2, pair_id=pair, installation_id=INSTALLATION, enabled=True,
                                    states=True, inbox=True, capability_generation=1, policy_generation=1,
                                    expires_at=int(time.time()) + 60,
                                    approvals={name: False for name in ['codex', 'claude', 'opencode', 'pi']},
                                    policy=dict(turn_completed=True, needs_input=True, abnormal_end=True))
                    elif '/snapshots/' in self.path:
                        body = dict(v=2)
                    else:
                        body = dict(v=2, event_id=data.get('event_id', self.path.rsplit('/', 1)[-1]), state='accepted', outbox_handoff=True)
                    raw = json.dumps(body).encode()
                    self.send_response(200)
                    self.send_header('Content-Length', str(len(raw)))
                    self.end_headers()
                    self.wfile.write(raw)
                except (BrokenPipeError, ConnectionResetError):
                    pass
                finally:
                    with owner.lock:
                        owner.http_active -= 1

        self.http = http.server.ThreadingHTTPServer(('127.0.0.1', 0), API)
        self.http.daemon_threads = True
        self.http_thread = threading.Thread(target=self.http.serve_forever, daemon=True)
        self.http_thread.start()
        port = self.http.server_port
        # Bind then close a private port to create a bounded connection-refused fixture.
        if mode == 'offline':
            self.http.shutdown()
            self.http.server_close()
        pairs_data = {}
        for i in range(pairs):
            key = f'measure_pair_{i:04d}'
            pairs_data[key] = dict(v=1, pair_id=key, installation_id=INSTALLATION,
                                   api_url=f'http://127.0.0.1:{port}', status='active',
                                   send_token='synthetic-test-token', encryption_key=base64.b64encode(bytes(range(32))).decode())
        write_json(self.store / 'installation.json', dict(v=1, installation_id=INSTALLATION))
        write_json(self.store / 'pairs.json', pairs_data)
        self.tmux = tmux
        self.herdr = None
        if tmux:
            self.tmux_socket = root / 'tmux.sock'
            pane = subprocess.check_output(['tmux', '-S', str(self.tmux_socket), 'new-session', '-d', '-s', 'measure', '-P', '-F', '#{pane_id}'], text=True).strip()
            self.env.update(TMUX=f'{self.tmux_socket},0,0', TMUX_PANE=pane)
        else:
            socket_dir = root / 'herdr'
            socket_dir.mkdir(mode=0o700)
            class Herdr(socketserver.StreamRequestHandler):
                def handle(self):
                    self.rfile.readline()
                    with owner.lock:
                        owner.target_queries += 1
                    response = dict(id='mobterm_snapshot', result=dict(type='session_snapshot', snapshot=dict(
                        version='0.9.3', protocol=22, panes=[dict(pane_id='w1:p1', terminal_id='term_measure', workspace_id='w1', tab_id='w1:t1')])))
                    try:
                        self.wfile.write((json.dumps(response) + '\n').encode())
                    except (BrokenPipeError, ConnectionResetError):
                        pass
            class UnixServer(socketserver.ThreadingUnixStreamServer):
                daemon_threads = True
                request_queue_size = 128
            self.herdr = UnixServer(str(socket_dir / 'herdr.sock'), Herdr)
            threading.Thread(target=self.herdr.serve_forever, daemon=True).start()
            self.env.update(HERDR_PANE_ID='w1:p1', HERDR_SESSION='default')
        self.sampler = threading.Thread(target=self.sample_workers, daemon=True)
        self.sampler.start()

    def sample_workers(self):
        while not self.stop.wait(0.05):
            output = subprocess.check_output(['ps', '-axo', 'command='], text=True)
            # The binary is a task-specific path, so unrelated CLI processes are excluded.
            count = sum(line.strip() in (f'{self.binary} activity flush', f'{self.binary} activity dispatch') for line in output.splitlines())
            self.worker_max = max(self.worker_max, count)

    def invoke(self, event, index, lane=0, cold=False, record=True):
        if cold:
            (self.store / 'capabilities-cache-v2.json').unlink(missing_ok=True)
        turn = f'turn_{lane}' if event in ('PreToolUse', 'PostToolUse', 'PermissionRequest') else f'turn_{lane}_{index}'
        tool = f'tool_{lane}_{index}'
        payload = dict(hook_event_name=event, session_id='measure_session', turn_id=turn, tool_use_id=tool,
                       tool_name='exec_command', tool_input={'command': 'true'}, cwd=str(self.root))
        command = ['approval', 'wait'] if event == 'PermissionRequest' else ['observe']
        start = time.perf_counter_ns()
        child = subprocess.run([str(self.binary), *command, '--source', 'codex'], input=json.dumps(payload).encode(),
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=self.env, timeout=135)
        elapsed = (time.perf_counter_ns() - start) / 1_000_000
        http = sum('"event":"network_request"' in line for line in child.stderr.decode().splitlines())
        persisted = None
        if event in ('PreToolUse', 'PostToolUse'):
            occurrence = hashlib.sha256(('codex\0measure_session\0' + turn + '\0' + event + '\0' + tool).encode()).hexdigest()
            try:
                db = json.loads((self.store / 'runs-v2.json').read_text())
                persisted = any(occurrence in run['seen'] for run in db['runs'].values())
            except FileNotFoundError:
                persisted = False
        row = dict(event=event, index=index, lane=lane, elapsed_ms=round(elapsed, 4), synchronous_http=http,
                   persisted=persisted, returncode=child.returncode, stdout_bytes=len(child.stdout))
        if record:
            with self.lock:
                self.rows.append(row)
        return row

    def close(self):
        # Let the bounded dispatcher finish before removing its store/server.
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            output = subprocess.check_output(['ps', '-axo', 'command='], text=True)
            if not any(command in output for command in (f'{self.binary} activity flush', f'{self.binary} activity dispatch')):
                break
            time.sleep(0.1)
        self.stop.set()
        self.sampler.join()
        if self.tmux:
            subprocess.run(['tmux', '-S', str(self.tmux_socket), 'kill-server'], check=True)
        if self.herdr:
            self.herdr.shutdown()
            self.herdr.server_close()
        if self.mode != 'offline':
            self.http.shutdown()
            self.http.server_close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--source', required=True, help='Exact source SHA or SHA + diff fingerprint')
    parser.add_argument('--label', required=True)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--samples', type=int, default=1000)
    parser.add_argument('--scenarios', nargs='+', choices=['normal-sequential', 'normal-concurrent8', 'cold-cache', 'slow-api', 'offline-api', 'history-multipair', 'real-tmux', 'installed-UserPromptSubmit', 'installed-PermissionRequest', 'installed-Interrupt', 'installed-SessionStart', 'installed-SessionEnd'], help='Run only these scenarios; omitted means the full matrix')
    args = parser.parse_args()
    binary = args.binary.resolve()
    args.output.mkdir(parents=True, exist_ok=True)
    metadata = dict(label=args.label, source=args.source, harness_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
                    binary_version=subprocess.check_output([str(binary), 'version'], text=True).strip(),
                    platform=platform.platform(), machine=platform.machine(), python=platform.python_version(),
                    go=subprocess.check_output(['go', 'version'], text=True).strip(),
                    cpu=subprocess.check_output(['sysctl', '-n', 'machdep.cpu.brand_string'], text=True).strip() if platform.system() == 'Darwin' else platform.processor(),
                    wall_clock='perf_counter_ns around subprocess.run; includes launch, stdin/stdout, target, locks, persistence, foreground exit',
                    sample_percentiles='nearest rank; no removed outliers', model_calls=0, real_apns=False,
                    lock_wait_ms=None, fsync_count=None, worker_sampling_interval_ms=50, scenarios={})
    all_rows = []
    cases = [('normal-sequential', 'normal', 1, False, 1, args.samples, False),
             ('normal-concurrent8', 'normal', 1, False, 8, min(args.samples, 256), False),
             ('cold-cache', 'normal', 1, False, 1, min(args.samples, 40), True),
             ('slow-api', 'slow', 1, False, 1, min(args.samples, 16), True),
             ('offline-api', 'offline', 1, False, 1, min(args.samples, 16), True),
             ('history-multipair', 'normal', 3, False, 1, min(args.samples, 32), False),
             ('real-tmux', 'normal', 1, True, 1, min(args.samples, 100), False)]
    for name, mode, pairs, tmux, lanes, count, cold in cases:
        if args.scenarios and name not in args.scenarios:
            continue
        with tempfile.TemporaryDirectory(prefix='mt-measure-', dir='/tmp') as directory:
            f = Fixture(binary, Path(directory), mode, pairs, tmux)
            try:
                if name == 'history-multipair':
                    for i in range(16):
                        f.invoke('Interrupt', i, record=False)
                for i in range(8):
                    f.invoke('PreToolUse', -i-1, record=False)
                    f.invoke('PostToolUse', -i-1, record=False)
                before_http, before_queries = f.http_count, f.target_queries
                if name == 'history-multipair':
                    metadata['history_fixture'] = dict(runs=len(json.loads((f.store / 'runs-v2.json').read_text())['runs']), outbox_files=len(list(f.store.rglob('*.json'))))
                def lane_run(lane):
                    for i in range(lane, count, lanes):
                        f.invoke('PreToolUse', i, lane, cold)
                        f.invoke('PostToolUse', i, lane, cold)
                with concurrent.futures.ThreadPoolExecutor(max_workers=lanes) as pool:
                    list(pool.map(lane_run, range(lanes)))
                summarize(name, f, all_rows, metadata, before_http, before_queries)
            finally:
                f.close()
            metadata['scenarios'][name]['max_sampled_worker_processes'] = f.worker_max
    # All seven installed native hooks; PermissionRequest is intentionally disabled.
    for event in ('UserPromptSubmit', 'PermissionRequest', 'Interrupt', 'SessionStart', 'SessionEnd'):
        name = 'installed-' + event
        if args.scenarios and name not in args.scenarios:
            continue
        with tempfile.TemporaryDirectory(prefix='mt-measure-', dir='/tmp') as directory:
            f = Fixture(binary, Path(directory))
            try:
                for i in range(min(args.samples, 32)):
                    f.invoke(event, i)
                summarize(name, f, all_rows, metadata, 0, 0)
            finally:
                f.close()
            metadata['scenarios'][name]['max_sampled_worker_processes'] = f.worker_max
    with (args.output / (args.label + '.csv')).open('w', newline='') as stream:
        writer = csv.DictWriter(stream, lineterminator='\n', fieldnames=['scenario', 'event', 'index', 'lane', 'elapsed_ms', 'synchronous_http', 'persisted', 'returncode', 'stdout_bytes'])
        writer.writeheader()
        writer.writerows(all_rows)
    (args.output / (args.label + '.json')).write_text(json.dumps(metadata, indent=2) + '\n')


def summarize(name, fixture, rows, metadata, before_http, before_queries):
    summary = dict(total_receiver_http=fixture.http_count-before_http,
                   target_queries=fixture.target_queries-before_queries if not fixture.tmux else None,
                   target_queries_note='real tmux queries not instrumented' if fixture.tmux else 'counted Herdr protocol fixture requests',
                   max_parallel_receiver_requests=fixture.http_max, events={})
    for event in sorted(set(row['event'] for row in fixture.rows)):
        selected = [row for row in fixture.rows if row['event'] == event]
        elapsed = [row['elapsed_ms'] for row in selected]
        summary['events'][event] = dict(n=len(selected), p50_ms=percentile(elapsed,.5), p95_ms=percentile(elapsed,.95),
                                       p99_ms=percentile(elapsed,.99), synchronous_http=sum(row['synchronous_http'] for row in selected),
                                       persisted_true=sum(row['persisted'] is True for row in selected),
                                       persisted_false=sum(row['persisted'] is False for row in selected),
                                       nonzero_exit=sum(row['returncode'] != 0 for row in selected))
    rows.extend(dict(scenario=name, **row) for row in fixture.rows)
    metadata['scenarios'][name] = summary
    print(json.dumps(dict(scenario=name, **summary)), flush=True)


if __name__ == '__main__':
    main()
