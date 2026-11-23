"""Own the loopback SSH, TLS and local Worker for the simulator append test."""

import getpass
import json
import shlex
import shutil
import socket
import subprocess
import time
from contextlib import ExitStack
from pathlib import Path


def start_live(stack: ExitStack, root: Path, cli: Path, api: Path):
    def run(*args):
        subprocess.run(
            args,
            cwd=root,
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )

    def launch(*args):
        log = stack.enter_context((root / (Path(args[0]).name + ".log")).open("wb"))
        proc = subprocess.Popen(args, stdout=log, stderr=log)

        def stop():
            proc.terminate()
            try:
                proc.wait(timeout=12)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()

        stack.callback(stop)
        return proc

    run(
        "openssl",
        "req",
        "-x509",
        "-nodes",
        "-newkey",
        "rsa:2048",
        "-days",
        "2",
        "-subj",
        "/CN=Mobterm Append Fixture CA",
        "-addext",
        "basicConstraints=critical,CA:TRUE",
        "-keyout",
        "ca.key",
        "-out",
        "ca.pem",
    )
    run(
        "openssl",
        "req",
        "-nodes",
        "-newkey",
        "rsa:2048",
        "-subj",
        "/CN=127.0.0.1",
        "-keyout",
        "tls.key",
        "-out",
        "tls.csr",
    )
    (root / "tls.ext").write_text(
        "basicConstraints=CA:FALSE\nextendedKeyUsage=serverAuth\nsubjectAltName=IP:127.0.0.1\n"
    )
    run(
        "openssl",
        "x509",
        "-req",
        "-in",
        "tls.csr",
        "-CA",
        "ca.pem",
        "-CAkey",
        "ca.key",
        "-CAcreateserial",
        "-days",
        "2",
        "-extfile",
        "tls.ext",
        "-out",
        "tls.pem",
    )
    worker = launch(
        shutil.which("node"),
        str(Path(__file__).with_name("codex_append_worker.mjs")),
        str(api),
        str(root),
    )
    settings = root / "worker.json"
    for _ in range(200):
        if settings.is_file():
            break
        if worker.poll() is not None:
            raise RuntimeError("local API failed to start")
        time.sleep(0.1)
    ids = json.loads(settings.read_text())
    for key in ("host", "user"):
        run("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key)
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    user = getpass.getuser()
    configuration = root / "sshd_config"
    force = root / "login"
    # Only the fixture key may execute commands; the remote shell has its own
    # HOME and no CODEX_HOME. The CLI has to find the daemon from the source.
    force.write_text(
        "#!/bin/sh\nexec /usr/bin/env -i "
        + " ".join(
            shlex.quote(k + "=" + str(v))
            for k, v in {
                "HOME": root,
                "MOBTERM_CONFIG_DIR": root / "store",
                "PATH": str(cli.parent) + ":/usr/bin:/bin",
                "SSL_CERT_FILE": root / "ca.pem",
                "GODEBUG": "x509sslcertoverrideplatform=1",
            }.items()
        )
        + ' /bin/sh -c "$SSH_ORIGINAL_COMMAND"\n'
    )
    force.chmod(0o700)
    configuration.write_text(f"""Port {port}
ListenAddress 127.0.0.1
HostKey {root}/host
PidFile {root}/sshd.pid
AuthorizedKeysFile {root}/user.pub
StrictModes no
PasswordAuthentication no
KbdInteractiveAuthentication no
UsePAM no
DisableForwarding yes
AllowUsers {user}
ForceCommand {force}
""")
    launch(shutil.which("sshd"), "-D", "-e", "-f", str(configuration))
    return ids, {
        "port": port,
        "username": user,
        "user_key_file": str(root / "user"),
        "host_key": (root / "host.pub").read_text().split()[1],
        "cli": str(cli),
        "tls_root": str(root / "ca.pem"),
    }
