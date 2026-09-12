#!/usr/bin/env python3
"""Offline installer -> real my -> event-reading harness checks on macOS/Linux.

Requires Python 3 and a candidate binary (--binary); no credentials or network.
Unlike the container adapter stories, this exercises an actual PTY/event poller.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import shlex
import shutil
import signal
import subprocess
import sys
import tarfile
import tempfile
import time


def write_exec(path, body):
    path.write_text(body)
    path.chmod(0o755)


def run_pty(command, env, cwd, write_only_output=False):
    pid, fd = pty.fork()
    if pid == 0:
        os.chdir(cwd)
        if write_only_output:
            output_fd = os.open(os.ttyname(1), os.O_WRONLY)
            os.dup2(output_fd, 1)
            os.dup2(output_fd, 2)
            os.close(output_fd)
        os.execve('/bin/sh', ['sh', '-c', command], env)
    output = bytearray()
    deadline = time.monotonic() + 60
    status = None
    sent = False
    try:
        while time.monotonic() < deadline:
            if select.select([fd], [], [], 0.1)[0]:
                try:
                    chunk = os.read(fd, 65536)
                except OSError:
                    break
                if not chunk:
                    break
                output.extend(chunk)
                if not sent and b'READY_FOR_INPUT' in output:
                    os.write(fd, b'operator-input\n')
                    sent = True
            done, status_value = os.waitpid(pid, os.WNOHANG)
            if done:
                status = status_value
                break
        if status is None:
            done, status = os.waitpid(pid, os.WNOHANG)
            if not done:
                try:
                    os.killpg(pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                except PermissionError:
                    os.kill(pid, signal.SIGKILL)
                _, status = os.waitpid(pid, 0)
    finally:
        os.close(fd)
    return os.waitstatus_to_exitcode(status), output.decode(errors='replace')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', required=True, type=Path)
    args = parser.parse_args()
    repo = Path(__file__).resolve().parents[2]
    with tempfile.TemporaryDirectory(prefix='my-terminal-test-') as temporary:
        base = Path(temporary)
        fixtures = base / 'fixtures'
        fixtures.mkdir()
        binary = args.binary.resolve()
        arch = {'aarch64': 'arm64', 'arm64': 'arm64', 'x86_64': 'amd64'}[os.uname().machine]
        platform = 'darwin' if sys.platform == 'darwin' else 'linux'
        tarname = f'my-cli_0.0.0-terminal_{platform}_{arch}.tar.gz'
        with tarfile.open(fixtures / tarname, 'w:gz') as archive:
            archive.add(binary, arcname='my')
        digest = hashlib.sha256((fixtures / tarname).read_bytes()).hexdigest()
        (fixtures / 'checksums.txt').write_text(f'{digest}  {tarname}\n')
        cases = ['pipe', 'direct', 'stdout-log', 'outputs-log', 'no-tty-command', 'no-terminal', 'write-only-output', 'harness-crash']
        for case in cases:
            home = base / case
            home.mkdir()
            bindir = home / 'bin'
            bindir.mkdir()
            # Deliberately omit git, gh, package managers, runtimes and compilers.
            for tool in ['uname', 'mktemp', 'grep', 'awk', 'sha256sum', 'shasum', 'mkdir',
                         'tar', 'gzip', 'mv', 'chmod', 'rm', 'sed', 'dirname', 'cat', 'tty', 'sh']:
                source = shutil.which(tool)
                if source and not (case == 'no-tty-command' and tool == 'tty'):
                    (bindir / tool).symlink_to(source)
            write_exec(bindir / 'curl', f'''#!{sys.executable}
import pathlib, shutil, sys
args=sys.argv[1:]; url=next(a for a in args if a.startswith('https://')); dest=args[args.index('-o')+1] if '-o' in args else None
if url.endswith('/releases/latest'): print('https://github.com/fluxinc/my-cli/releases/tag/v0.0.0-terminal',end='')
elif 'raw.githubusercontent.com' in url: shutil.copyfile({str(repo / 'install.sh')!r},dest)
elif url.endswith('/install.sh'): sys.stdout.write(pathlib.Path({str(repo / 'site/public/install.sh')!r}).read_text())
else: shutil.copyfile(str(pathlib.Path({str(fixtures)!r})/url.rsplit('/',1)[-1]),dest)
''')
            write_exec(bindir / 'codex', f'''#!{sys.executable}
import os, select, shutil, sys
assert shutil.which('git') is None and shutil.which('gh') is None
assert os.isatty(0), 'stdin is not a terminal'
if hasattr(select, 'kqueue'):
    poller=select.kqueue()
    poller.control([select.kevent(0,filter=select.KQ_FILTER_READ,flags=select.KQ_EV_ADD)],0,0)
else:
    poller=select.epoll(); poller.register(0,select.EPOLLIN)
assert 'JOIN_BOOTSTRAP' in sys.argv[-1]
print('READY_FOR_INPUT',flush=True)
assert sys.stdin.readline().strip() == 'operator-input'
print('AGENT_INPUT_OK',flush=True)
''')
            if case == 'harness-crash':
                write_exec(bindir / 'codex', '#!/bin/sh\necho BOOTSTRAP_CRASH >&2\nexit 42\n')
            env = {'HOME': str(home), 'PATH': str(bindir), 'SHELL': '/bin/sh', 'TERM': 'xterm-256color',
                   'MYCLI_NO_AUTO_REFRESH': '1', 'MYCLI_NO_UPDATE_CHECK': '1'}
            flags = '--manifest acme https://github.com/acme/acme-manifest.git --harness codex'
            command = 'curl -fsSL https://my-cli.com/install.sh | sh -s -- ' + flags
            log = home / 'output.log'
            if case in ['direct', 'outputs-log']:
                command = 'sh ' + shlex.quote(str(repo / 'site/public/install.sh')) + ' ' + flags
            if case == 'stdout-log':
                command = '{ ' + command + '; } >' + shlex.quote(str(log))
            if case == 'outputs-log':
                # stdin remains a real terminal even when both outputs are logs.
                # Send input up front because READY is deliberately invisible.
                command = command + ' >' + shlex.quote(str(log)) + ' 2>&1'
                # This mode checks retained stdin with no read round trip.
                stub = (bindir / 'codex').read_text().replace("assert sys.stdin.readline().strip() == 'operator-input'", 'pass')
                write_exec(bindir / 'codex', stub)
            if case == 'no-terminal':
                result = subprocess.run(['/bin/sh', '-c', command], cwd=home, env=env,
                                        stdin=subprocess.DEVNULL, capture_output=True, text=True,
                                        start_new_session=True, timeout=20)
                code, output = result.returncode, result.stdout + result.stderr
            else:
                if case == 'stdout-log':
                    # Keep protocol markers on terminal stderr when stdout is logged.
                    stub = (bindir / 'codex').read_text().replace("print('READY_FOR_INPUT',flush=True)", "print('READY_FOR_INPUT',file=sys.stderr,flush=True)")
                    write_exec(bindir / 'codex', stub)
                code, output = run_pty(command, env, home, case == 'write-only-output')
            if log.exists():
                output += log.read_text()
            expected = 'onboarding was not started' if case in ['no-tty-command', 'no-terminal'] else 'AGENT_INPUT_OK'
            if case == 'harness-crash':
                expected = 'onboarding harness exited with status 42'
                assert '--harness codex' in output and '--harness <name>' in output and '--no-agent' in output, output
                assert output.count('BOOTSTRAP_CRASH') == 1, output
            assert code == 0 and expected in output, f'{case}: exit={code}\n{output}'
            print(json.dumps({'case': case, 'platform': platform + '/' + arch, 'status': 'pass'}))


if __name__ == '__main__':
    main()
