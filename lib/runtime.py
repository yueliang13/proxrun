#!/usr/bin/env python3
"""跨 CLI 共享隧道：文件锁串行化状态，租约记录使用者，启动者负责回收。"""
from __future__ import annotations

from contextlib import contextmanager
import fcntl
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tempfile
import time
import uuid


class Runtime:
    def __init__(self):
        self.data = Path(os.environ['SP_DATA_DIR']).resolve()
        self.data.mkdir(parents=True, exist_ok=True)
        self.state_file = self.data / 'mihomo.state.json'
        self.pid_file = self.data / 'mihomo.pid'
        self.port = int(os.environ['SP_HTTP_PORT'])
        self.kernel = None
        self.command = None
        self.interrupted = 0
        self.lease = uuid.uuid4().hex
        self.session = None

    @contextmanager
    def locked(self):
        fd = os.open(self.data / 'runtime.lock', os.O_CREAT | os.O_RDWR, 0o600)
        with os.fdopen(fd, 'r+') as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            yield

    @staticmethod
    def identity(pid):
        if not isinstance(pid, int) or pid <= 1:
            return None
        try:
            if sys.platform.startswith('linux'):
                fields = Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()
                if fields[0] == 'Z':
                    return None
                return {'pid': pid, 'birth': fields[19],
                        'boot': Path('/proc/sys/kernel/random/boot_id').read_text().strip()}
            result = subprocess.run(['ps', '-p', str(pid), '-o', 'lstart=', '-o', 'stat='],
                                    capture_output=True, text=True, check=False)
            text = result.stdout.strip()
            if result.returncode or not text or text.split()[-1].startswith('Z'):
                return None
            return {'pid': pid, 'birth': text.rsplit(None, 1)[0]}
        except (OSError, IndexError):
            return None

    @classmethod
    def alive(cls, record):
        return isinstance(record, dict) and record == cls.identity(record.get('pid'))

    def read(self):
        try:
            state = json.loads(self.state_file.read_text())
            if not isinstance(state, dict) or not self.alive(state.get('kernel')):
                return None
            return state
        except (OSError, ValueError):
            return None

    def write(self, state):
        fd, name = tempfile.mkstemp(prefix='.state-', dir=self.data)
        try:
            with os.fdopen(fd, 'w') as stream:
                json.dump(state, stream)
            os.replace(name, self.state_file)
            # 保留纯数字 PID 文件供人工查看；绝不凭这个文件发送信号。
            self.pid_file.write_text(str(state['kernel']['pid']) + '\n')
            self.pid_file.chmod(0o600)
        finally:
            Path(name).unlink(missing_ok=True)

    def clear(self):
        self.state_file.unlink(missing_ok=True)
        self.pid_file.unlink(missing_ok=True)

    @staticmethod
    def send(record, sig):
        """Linux 的 pidfd 将检查和发信号绑定到同一个进程，避免 PID 重用竞态。"""
        if not Runtime.alive(record):
            return
        if hasattr(os, 'pidfd_open') and hasattr(signal, 'pidfd_send_signal'):
            try:
                fd = os.pidfd_open(record['pid'])
            except ProcessLookupError:
                return
            try:
                if Runtime.alive(record):
                    signal.pidfd_send_signal(fd, sig)
            finally:
                os.close(fd)
        elif Runtime.alive(record):
            try:
                os.kill(record['pid'], sig)
            except ProcessLookupError:
                pass

    def stop_kernel(self, state):
        record = state['kernel']
        self.send(record, signal.SIGTERM)
        deadline = time.monotonic() + 2
        while self.alive(record) and time.monotonic() < deadline:
            if self.kernel:
                self.kernel.poll()
            time.sleep(.05)
        if self.alive(record):
            self.send(record, signal.SIGKILL)
            deadline = time.monotonic() + 2
            while self.alive(record) and time.monotonic() < deadline:
                if self.kernel:
                    self.kernel.poll()
                time.sleep(.05)
        if self.alive(record):
            raise RuntimeError('停止 mihomo 失败，保留状态以便重试')
        self.clear()

    def owns_listener(self, record):
        if not sys.platform.startswith('linux'):
            return True  # 其他平台由启动前占用检查、进程身份和存活检查共同保证。
        try:
            inodes = set()
            for entry in Path(f"/proc/{record['pid']}/fd").iterdir():
                try:
                    target = os.readlink(entry)
                    if target.startswith('socket:['):
                        inodes.add(target[8:-1])
                except FileNotFoundError:
                    pass
            for table in ('tcp', 'tcp6'):
                for line in Path('/proc/net/' + table).read_text().splitlines()[1:]:
                    fields = line.split()
                    if (fields[3] == '0A' and int(fields[1].split(':')[1], 16) == self.port
                            and fields[9] in inodes):
                        return True
        except OSError:
            return False
        return False

    def ready(self, record):
        if not self.alive(record) or not self.owns_listener(record):
            return False
        try:
            with socket.create_connection(('127.0.0.1', self.port), timeout=.2):
                return self.alive(record)
        except OSError:
            return False

    def prepare(self):
        if not Path(os.environ['SP_MIHOMO_BIN']).is_file():
            raise RuntimeError('找不到 mihomo，请先运行 simple-proxy install')
        # 不把别人的监听端口当成本次启动的成功信号。
        with socket.socket() as probe:
            probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            try:
                probe.bind(('127.0.0.1', self.port))
            except OSError as error:
                raise RuntimeError(f'端口 {self.port} 已被其他进程占用') from error
        subprocess.run([sys.executable, os.environ['SP_BUILDER'],
                        '--subscription-config', str(self.data / 'config.yaml'),
                        '--rules', os.environ['SP_RULES_FILE'],
                        '--output', str(self.data / 'config.runtime.yaml'),
                        '--http-port', str(self.port),
                        '--enable-api', os.environ['SP_ENABLE_API'],
                        '--dns-fallbacks', os.environ['SP_DNS_FALLBACKS']], check=True)
        subprocess.run(['bash', os.environ['SP_CLEANER'], str(self.data),
                        os.environ['SP_LOG_KEEP_DAYS']], check=True)
        with open(self.data / 'mihomo.log', 'ab') as log:
            self.kernel = subprocess.Popen(
                [str(Path(os.environ['SP_MIHOMO_BIN']).resolve()), '-d', str(self.data), '-f',
                 str(self.data / 'config.runtime.yaml')], cwd=self.data,
                stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
        try:
            record = self.identity(self.kernel.pid)
            timeout = float(os.environ.get('SP_START_TIMEOUT', '30'))
            deadline = time.monotonic() + timeout
            while self.kernel.poll() is None and time.monotonic() < deadline:
                if self.interrupted:
                    raise InterruptedError()
                if record and self.ready(record):
                    return record
                time.sleep(.05)
            raise RuntimeError(f'mihomo 未能监听端口 {self.port}，请查看 {self.data / "mihomo.log"}')
        except BaseException:
            if self.kernel.poll() is None:
                self.kernel.terminate()
                try:
                    self.kernel.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    self.kernel.kill()
            self.kernel.wait()
            raise

    def acquire(self, keep):
        with self.locked():
            state = self.read()
            if state and state.get('port') != self.port:
                raise RuntimeError('当前隧道使用其他端口，请先执行 simple-proxy off')
            if state and not self.ready(state['kernel']):
                raise RuntimeError('mihomo 进程存在但代理端口不可用，请先执行 simple-proxy off')
            if state:
                state['users'] = {key: value for key, value in state.get('users', {}).items()
                                  if self.alive(value)}
                if not state['keep'] and not state['users']:
                    self.stop_kernel(state)
                    state = None
            if state is None:
                self.clear()  # 旧格式 PID 文件不能用于控制任何进程。
                record = self.prepare()
                state = {'kernel': record, 'port': self.port, 'keep': False,
                         'session': uuid.uuid4().hex, 'users': {}}
            state['keep'] = state['keep'] or keep
            state['users'][self.lease] = self.identity(os.getpid())
            self.session = state['session']
            self.write(state)

    def release(self):
        with self.locked():
            state = self.read()
            if not state:
                self.clear()
                return
            if state.get('session') != self.session:
                return
            state['users'] = {key: value for key, value in state['users'].items()
                              if key != self.lease and self.alive(value)}
            if not state['keep'] and not state['users']:
                self.stop_kernel(state)
            else:
                self.write(state)

    def handle_signal(self, sig, _frame):
        self.interrupted = sig
        if self.command is not None and self.command.poll() is None:
            try:
                self.command.send_signal(sig)
            except ProcessLookupError:
                pass

    def install_handlers(self):
        for sig in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
            signal.signal(sig, self.handle_signal)

    def wait_command(self):
        interrupted_at = None
        while True:
            try:
                return self.command.wait(timeout=.1)
            except subprocess.TimeoutExpired:
                if self.interrupted:
                    interrupted_at = interrupted_at or time.monotonic()
                    if time.monotonic() - interrupted_at > 2:
                        self.command.kill()

    def on(self, args, hold=False):
        keep = hold or (args and args[0] == '--start')
        if args and args[0] == '--start':
            args = args[1:]
        if not hold and not args:
            raise RuntimeError('用法: simple-proxy on [--start] <cmd> [args...]')
        self.install_handlers()
        if hold:
            signal.signal(signal.SIGHUP, signal.SIG_IGN)
        acquired = False
        try:
            self.acquire(bool(keep))
            acquired = True
            if hold:
                Path(args[0], 'ready').touch(mode=0o600)
                # holder 是 shell 的直接子进程，负责等待并回收内核。
                while self.kernel is None or self.kernel.poll() is None:
                    with self.locked():
                        state = self.read()
                        if not state or state.get('session') != self.session:
                            break
                    if self.interrupted:
                        self.off()
                        break
                    time.sleep(.1)
                return 0
            if self.interrupted:
                return 128 + self.interrupted
            env = os.environ.copy()
            url = f'http://127.0.0.1:{self.port}'
            for key in ('http_proxy', 'https_proxy', 'HTTP_PROXY', 'HTTPS_PROXY'):
                env[key] = url
            extra = os.environ['SP_NO_PROXY_EXTRA']
            env['no_proxy'] = ','.join(filter(None, [env.get('no_proxy') or env.get('NO_PROXY'), extra]))
            env['NO_PROXY'] = env['no_proxy']
            self.command = subprocess.Popen(args, env=env)
            code = self.wait_command()
            return 128 + self.interrupted if self.interrupted else (128 - code if code < 0 else code)
        finally:
            if acquired:
                self.release()
            if self.kernel and not acquired and self.kernel.poll() is None:
                self.kernel.terminate()
                try:
                    self.kernel.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    self.kernel.kill()
                    self.kernel.wait()
            if self.kernel:
                # --start 的独立 CLI 必须返回；普通模式保留父子关系直至所有使用者完成。
                if not keep or hold:
                    while self.kernel.poll() is None:
                        with self.locked():
                            state = self.read()
                            if not state or state.get('session') != self.session:
                                self.kernel.terminate()
                            elif state.get('session') == self.session:
                                state['users'] = {k: v for k, v in state['users'].items() if self.alive(v)}
                                if not state['keep'] and not state['users']:
                                    self.stop_kernel(state)
                        time.sleep(.1)
                    self.kernel.wait()

    def off(self):
        with self.locked():
            state = self.read()
            if state:
                self.stop_kernel(state)
            else:
                self.clear()

    def status(self):
        with self.locked():
            state = self.read()
            if state:
                usable = self.ready(state['kernel'])
                print(f'● mihomo: 运行中 (pid {state["kernel"]["pid"]})  HTTP 代理 http://127.0.0.1:{state["port"]}')
                print('  端口状态  : ' + ('可用' if usable else '不可用'))
                print('  生命周期  : ' + ('常驻，直到 off' if state['keep'] else '一次性，共享至最后一个命令完成'))
            else:
                print('○ mihomo: 未运行')
            print(f'  端口      : {self.port}\n  数据目录  : {self.data}')
            print(f'  配置文件  : {self.data / "config.runtime.yaml"}')
            print(f'  规则文件  : {os.environ["SP_RULES_FILE"]}')


def main():
    try:
        runtime = Runtime()
        action, *args = sys.argv[1:]
        if action == 'on':
            return runtime.on(args)
        if action == 'hold':
            return runtime.on(args, hold=True)
        if action == 'off':
            runtime.off()
        elif action == 'status':
            runtime.status()
        elif action in ('running', 'kept'):
            state = runtime.read()
            return 0 if state and (action == 'running' or state['keep']) else 1
        elif action != 'on':
            raise RuntimeError('未知运行命令')
        return 0
    except InterruptedError:
        return 128 + runtime.interrupted
    except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f'❌ {error}', file=sys.stderr)
        return 127 if isinstance(error, FileNotFoundError) else 1


if __name__ == '__main__':
    sys.exit(main())
