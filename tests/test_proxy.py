#!/usr/bin/env python3
"""针对实际故障的回归测试；真实内核测试仅在 SP_TEST_MIHOMO 指定时运行。"""
import ctypes
import gzip
import importlib.util
import json
import os
from pathlib import Path
import shlex
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import unittest
import threading
import struct
import http.server
import pty
import select
import re
from unittest import mock

REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO / 'lib/vendor'))
sys.path.insert(0, str(Path(os.environ.get('SP_DATA_DIR', str(Path.home() / '.local/share/simple-proxy'))) / 'vendor'))
import yaml
spec = importlib.util.spec_from_file_location('builder', REPO / 'lib/build-config.py')
builder = importlib.util.module_from_spec(spec)
spec.loader.exec_module(builder)
if sys.platform.startswith('linux'):
    ctypes.CDLL(None).prctl(36, 1, 0, 0, 0)  # 回收测试中独立 --start 创建的孤儿。

SUBSCRIPTION = '''rules: [MATCH,DIRECT]
port: 8080
mixed-port: 8081
socks-port: 8082
redir-port: 8083
mode: direct
allow-lan: true
bind-address: '*'
external-controller: 0.0.0.0:9090
external-controller-unix: /tmp/undesired.sock
external-controller-tls: 0.0.0.0:9443
tun: {enable: true}
listeners: [{name: extra, type: http, port: 8888}]
dns: {enable: true, listen: '0.0.0.0:53', nameserver: [8.8.8.8]}
profile: {store-selected: true}
proxies:
  - {name: TEST, type: socks5, server: 127.0.0.1, port: 9}
proxy-groups:
  - {name: PROXY, type: select, proxies: [DIRECT, TEST]}
'''
FAKE_KERNEL = '''#!/usr/bin/env python3
import os,sys,socket,time
from pathlib import Path
import yaml
if '-v' in sys.argv: print('Mihomo fake test');sys.exit(0)
config=yaml.safe_load(Path(sys.argv[sys.argv.index('-f')+1]).read_text())
if '-t' in sys.argv: sys.exit(0)
if os.environ.get('SP_FAKE_EXIT'):sys.exit(3)
if os.environ.get('SP_FAKE_NO_LISTEN'):time.sleep(120);sys.exit(0)
time.sleep(float(os.environ.get('SP_FAKE_DELAY','0')))
s=socket.socket();s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
s.bind(('127.0.0.1',config['port']));s.listen(128);s.settimeout(.1)
while True:
 try:
  c,a=s.accept();c.close()
 except socket.timeout:pass
'''


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def alive(pid):
    try:
        if sys.platform.startswith('linux'):
            return Path(f'/proc/{pid}/stat').read_text().rsplit(')', 1)[1].split()[0] != 'Z'
        os.kill(pid, 0)
        return True
    except (OSError, ValueError):
        return False


class BuildTests(unittest.TestCase):
    def merge(self, src=SUBSCRIPTION, rules=None):
        return builder.merge(builder.load(src), rules or {'rules': ['MATCH,PROXY']},
                             34567, ['https://doh.pub/dns-query'])

    def test_order_independent_and_scope(self):
        out = self.merge()
        self.assertEqual(out['proxies'][0]['name'], 'TEST')
        self.assertEqual(out['mode'], 'rule')
        self.assertEqual(out['port'], 34567)
        self.assertFalse(out['allow-lan'])
        self.assertEqual(out['bind-address'], '127.0.0.1')
        for key in ('mixed-port', 'socks-port', 'redir-port', 'listeners', 'tun',
                    'external-controller', 'external-controller-unix', 'external-controller-tls'):
            self.assertNotIn(key, out)
        self.assertNotIn('listen', out['dns'])
        self.assertFalse(out['profile']['store-selected'])
        self.assertEqual(out['proxy-groups'][-1]['proxies'], ['TEST'])

    def test_private_rules_precede_user_policy_and_resolve(self):
        out = self.merge(rules={'rules': ['MATCH,PROXY']})
        self.assertEqual(out['rules'][-1], 'MATCH,PROXY')
        self.assertIn('IP-CIDR,10.0.0.0/8,DIRECT', out['rules'])
        self.assertIn('IP-CIDR6,::1/128,DIRECT', out['rules'])
        self.assertFalse(any('no-resolve' in r for r in out['rules']))

    def test_duplicate_keys_rejected(self):
        with self.assertRaises(ValueError):
            builder.load('rules: []\nrules: []\n')

    def test_bad_policy_and_empty_nodes_rejected(self):
        with self.assertRaises(ValueError):
            self.merge(rules={'rules': 'MATCH,DIRECT'})
        with self.assertRaises(ValueError):
            self.merge('proxies: []')

    def test_api_requires_secret(self):
        with self.assertRaises(ValueError):
            builder.merge(builder.load(SUBSCRIPTION), {'rules': ['MATCH,PROXY']}, 7890, [], True)
        out = builder.merge(builder.load(SUBSCRIPTION + 'secret: local-secret\n'),
                            {'rules': ['MATCH,PROXY']}, 7890, [], True)
        self.assertEqual(out['external-controller'], '127.0.0.1:9090')

    def test_atomic_write_private_before_write(self):
        with tempfile.TemporaryDirectory() as directory:
            p = Path(directory) / 'config.yaml'
            original = os.fdopen
            modes = []
            def inspect(fd, *args, **kwargs):
                modes.append(os.fstat(fd).st_mode & 0o777)
                return original(fd, *args, **kwargs)
            old = os.umask(0o022)
            try:
                with mock.patch.object(builder.os, 'fdopen', inspect):
                    builder.atomic_write(p, 'secret: value\n')
            finally:
                os.umask(old)
            self.assertEqual(modes, [0o600])
            self.assertEqual(p.stat().st_mode & 0o777, 0o600)
            self.assertEqual(list(Path(directory).glob('*.tmp')), [])


class RuntimeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='sp-test-')
        self.root = Path(self.temp.name)
        self.env = {k: v for k, v in os.environ.items() if not k.startswith('SP_')
                    and k not in ('SIMPLE_PROXY_SUBSCRIPTION', 'SIMPLE_PROXY_DATA_DIR')
                    and k.lower() not in ('http_proxy', 'https_proxy', 'all_proxy', 'no_proxy')}
        self.env['PYTHONPATH'] = str(Path(yaml.__file__).resolve().parent.parent) + os.pathsep + self.env.get('PYTHONPATH', '')
        self.env.update(HOME=str(self.root / 'home'), SP_BIN_DIR=str(self.root / 'bin'),
                        SP_DATA_DIR=str(self.root / 'data'), SP_CONFIG_DIR=str(self.root / 'config'),
                        SP_START_TIMEOUT='1.5')
        Path(self.env['HOME']).mkdir()
        self.cli = str(self.root / 'bin/simple-proxy')
        self.children = []
        self.run_cli_install()
        self.port = free_port()
        self.conf = self.root / 'config/proxy.conf'
        self.conf.write_text(self.conf.read_text().replace('SP_HTTP_PORT=7890', f'SP_HTTP_PORT={self.port}'))
        (self.root / 'data/config.yaml').write_text(SUBSCRIPTION)
        kernel = self.root / 'bin/mihomo'
        kernel.write_text(FAKE_KERNEL)
        kernel.chmod(0o755)

    def run_cli_install(self):
        r = subprocess.run(['bash', str(REPO / 'install.sh'), '--no-download', '--no-shell'],
                           env=self.env, capture_output=True, text=True, timeout=15)
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)

    def tearDown(self):
        try:
            self.run_cli('off')
        finally:
            for p in self.children:
                try:
                    os.killpg(p.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                p.communicate(timeout=3)
            time.sleep(.1)
            while True:
                try:
                    pid, _ = os.waitpid(-1, os.WNOHANG)
                    if not pid:
                        break
                except ChildProcessError:
                    break
            self.temp.cleanup()

    def run_cli(self, *args, **kwargs):
        return subprocess.run([self.cli, *map(str, args)], env=self.env,
                              capture_output=True, text=True, timeout=10, **kwargs)

    def spawn(self, args):
        p = subprocess.Popen(args, env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                             text=True, start_new_session=True)
        self.children.append(p)
        return p

    def wait_file(self, path):
        deadline = time.monotonic() + 5
        while not path.exists() and time.monotonic() < deadline:
            time.sleep(.02)
        self.assertTrue(path.exists(), str(path))

    def command(self, name):
        marker = self.root / (name + '.ready')
        finish = self.root / (name + '.finish')
        code = (f'import pathlib,time;pathlib.Path({str(marker)!r}).touch();'
                f'\nwhile not pathlib.Path({str(finish)!r}).exists():time.sleep(.02)')
        p = self.spawn([self.cli, 'on', sys.executable, '-c', code])
        self.wait_file(marker)
        return p, finish

    def kernel_pid(self):
        return int((self.root / 'data/mihomo.pid').read_text())

    def assert_quiet_success(self, result):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(result.stderr, '')

    def test_quiet_environment_cwd_exit_code_and_custom_port(self):
        code = 'import os; print(os.getcwd()); print(os.environ["https_proxy"]); print("target stderr",file=__import__("sys").stderr); exit(7)'
        r = self.run_cli('on', sys.executable, '-c', code)
        self.assertEqual(r.returncode, 7)
        self.assertEqual(r.stdout.splitlines(), [os.getcwd(), f'http://127.0.0.1:{self.port}'])
        self.assertEqual(r.stderr, 'target stderr\n')
        self.assertFalse((self.root / 'data/mihomo.pid').exists())
        self.assert_quiet_success(self.run_cli('off'))

    def test_environment_overrides_config(self):
        self.port = free_port()
        self.env['SP_HTTP_PORT'] = str(self.port)
        self.env['SP_DNS_FALLBACKS'] = 'https://example.invalid/a https://example.invalid/b'
        r = self.run_cli('on', sys.executable, '-c', 'import os; print(os.environ["http_proxy"])')
        self.assert_quiet_success(r)
        self.assertEqual(r.stdout.strip(), f'http://127.0.0.1:{self.port}')
        dns = yaml.safe_load((self.root / 'data/config.runtime.yaml').read_text())['dns']['nameserver']
        self.assertIn('https://example.invalid/a', dns)
        self.assertIn('https://example.invalid/b', dns)

    def test_keep_survives_later_one_shot(self):
        self.assert_quiet_success(self.run_cli('on', '--start', 'true'))
        pid = self.kernel_pid()
        self.assert_quiet_success(self.run_cli('on', 'true'))
        self.assertTrue(alive(pid))
        self.assertEqual(self.kernel_pid(), pid)
        self.assert_quiet_success(self.run_cli('off'))
        self.assertFalse(alive(pid))

    def test_overlapping_commands_share_until_last_finishes(self):
        first, finish_first = self.command('first')
        pid = self.kernel_pid()
        second, finish_second = self.command('second')
        self.assertEqual(self.kernel_pid(), pid)
        finish_first.touch()
        time.sleep(.2)
        self.assertTrue(alive(pid))
        self.assertIsNone(second.poll())
        finish_second.touch()
        for p in (second, first):
            out, err = p.communicate(timeout=5)
            self.assertEqual(p.returncode, 0, out + err)
            self.assertEqual(err, '')
        self.assertFalse(alive(pid))

    def test_interrupting_one_user_does_not_stop_other_users(self):
        first, _ = self.command('interrupted-shared')
        second, finish = self.command('surviving-shared')
        pid = self.kernel_pid()
        first.terminate()
        time.sleep(.3)
        self.assertTrue(alive(pid))
        self.assertIsNone(second.poll())
        finish.touch()
        second.communicate(timeout=5)
        first.communicate(timeout=5)
        self.assertEqual(first.returncode, 143)
        self.assertFalse(alive(pid))

    def test_sigterm_cleans_up_target_and_kernel(self):
        p, _ = self.command('interrupted')
        pid = self.kernel_pid()
        p.terminate()
        out, err = p.communicate(timeout=5)
        self.assertEqual(p.returncode, 143, out + err)
        self.assertFalse(alive(pid))
        self.assertFalse((self.root / 'data/mihomo.state.json').exists())

    def test_sigint_cleans_up(self):
        p, _ = self.command('interrupt')
        pid = self.kernel_pid()
        p.send_signal(signal.SIGINT)
        p.communicate(timeout=5)
        self.assertEqual(p.returncode, 130)
        self.assertFalse(alive(pid))

    def test_startup_timeout_and_crash_leave_no_state_or_child(self):
        for setting in ('SP_FAKE_NO_LISTEN', 'SP_FAKE_EXIT'):
            self.env[setting] = '1'
            r = self.run_cli('on', 'true')
            self.assertNotEqual(r.returncode, 0)
            self.assertFalse((self.root / 'data/mihomo.pid').exists())
            self.assertFalse((self.root / 'data/mihomo.state.json').exists())
            self.env.pop(setting)

    def test_signal_during_startup_reaps_kernel(self):
        self.env['SP_FAKE_NO_LISTEN'] = '1'
        p = self.spawn([self.cli, 'on', 'true'])
        self.wait_file(self.root / 'data/config.runtime.yaml')
        time.sleep(.2)
        p.terminate()
        p.communicate(timeout=5)
        self.assertEqual(p.returncode, 143)
        self.assertFalse((self.root / 'data/mihomo.pid').exists())

    def test_occupied_port_does_not_run_command(self):
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', self.port));sock.listen()
            r = self.run_cli('on', 'printf', 'MUST_NOT_RUN')
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(r.stdout, '')
        self.assertIn('占用', r.stderr)

    def test_stale_pid_never_kills_unrelated_process(self):
        victim = self.spawn(['sleep', '120'])
        (self.root / 'data/mihomo.pid').write_text(str(victim.pid))
        self.assert_quiet_success(self.run_cli('off'))
        self.assertIsNone(victim.poll())
        (self.root / 'data/mihomo.state.json').write_text(json.dumps({
            'kernel': {'pid': victim.pid, 'birth': 'wrong', 'boot': 'wrong'}}))
        self.assert_quiet_success(self.run_cli('off'))
        self.assertIsNone(victim.poll())

    def test_installed_update_secure_and_preserves_old_on_failure(self):
        source = self.root / 'subscription.yaml';source.write_text(SUBSCRIPTION)
        self.assertEqual(self.run_cli('update', source.as_uri()).returncode, 0)
        target = self.root / 'data/config.yaml'
        self.assertEqual(target.read_text(), SUBSCRIPTION)
        self.assertEqual(target.stat().st_mode & 0o777, 0o600)
        r = self.run_cli('update', (self.root / 'missing').as_uri())
        self.assertNotEqual(r.returncode, 0)
        self.assertEqual(target.read_text(), SUBSCRIPTION)
        self.assertEqual(list(target.parent.glob('*.tmp')), [])

    def test_shell_keep_and_off_bash_and_zsh(self):
        for shell in ('bash', 'zsh'):
            if not shutil.which(shell):
                continue
            script = f'. {shlex.quote(str(self.root / "data/shell.sh"))}; proxy-on --start true; proxy-on true; proxy-off; proxy-status'
            p = self.spawn([shell, '-c', script])
            out, err = p.communicate(timeout=8)
            self.assertEqual(p.returncode, 0, out + err)
            self.assertEqual(err, '')
            self.assertIn('未运行', out)
            self.assertFalse((self.root / 'data/mihomo.pid').exists())

    def test_interactive_shell_is_quiet_and_reaps_holder(self):
        for shell in ('bash', 'zsh'):
            if not shutil.which(shell):
                continue
            child, master = pty.fork()
            if child == 0:
                env = self.env.copy()
                env.update(TERM='xterm', PS1='sp-test> ', ZDOTDIR=str(self.root))
                argv = [shell, '--noprofile', '--norc', '-i'] if shell == 'bash' else [shell, '-f', '-i']
                os.execvpe(shell, argv, env)
            script = (f'. {shlex.quote(str(self.root / "data/shell.sh"))}; '
                      'proxy-on --start true; proxy-off; jobs -p; exit\n')
            os.write(master, script.encode())
            output = b''
            deadline = time.monotonic() + 8
            try:
                while time.monotonic() < deadline:
                    readable, _, _ = select.select([master], [], [], .1)
                    if readable:
                        try:
                            part = os.read(master, 65536)
                        except OSError:
                            break
                        if not part:
                            break
                        output += part
                else:
                    os.kill(child, signal.SIGKILL)
                    self.fail('interactive shell timeout: ' + output.decode(errors='replace'))
            finally:
                os.close(master)
                os.waitpid(child, 0)
            text = output.decode(errors='replace')
            self.assertNotRegex(text, r'\[\d+\]\s+\d+', text)
            self.assertNotIn('❌', text)
            self.assertNotIn('运行时配置已生成', text)

    def test_installed_reinstall_and_uninstall(self):
        r = self.run_cli('install', '--no-download', '--no-shell', '--port', str(self.port))
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assert_quiet_success(self.run_cli('on', 'true'))
        r = self.run_cli('uninstall')
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertFalse(Path(self.cli).exists())
        self.assertTrue((self.root / 'data/config.yaml').exists())
        # teardown 仍需可调用 off。
        shutil.copy(REPO / 'bin/simple-proxy', self.cli)

    def test_download_uses_valid_asset_and_actual_uncompressed_file(self):
        mock_bin = self.root / 'mock';mock_bin.mkdir()
        (mock_bin / 'curl').write_text('''#!/usr/bin/env python3
import gzip,sys,os
from pathlib import Path
args=sys.argv[1:];Path(os.environ['SP_TEST_URL']).write_text(args[-1])
if os.environ.get('SP_TEST_CURL_FAIL'):sys.exit(22)
p=Path(args[args.index('-o')+1]);p.write_bytes(gzip.compress(b'#!/bin/sh\\necho fake-kernel\\n'))
''')
        (mock_bin / 'curl').chmod(0o755)
        (mock_bin / 'uname').write_text('#!/bin/sh\ncase "$1" in -s) echo Linux;; -m) echo x86_64;; esac\n')
        (mock_bin / 'uname').chmod(0o755)
        self.env['PATH'] = str(mock_bin) + os.pathsep + self.env['PATH']
        self.env['SP_TEST_URL'] = str(self.root / 'download-url')
        r = self.run_cli('install', '--no-shell')
        self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
        self.assertIn('mihomo-linux-amd64-compatible-v1.19.33.gz', (self.root / 'download-url').read_text())
        self.assertIn('fake-kernel', (self.root / 'bin/mihomo').read_text())
        self.assertEqual(list((self.root / 'bin').glob('.mihomo-install.*')), [])
        self.env['SP_TEST_CURL_FAIL'] = '1'
        before = (self.root / 'bin/mihomo').read_bytes()
        r = self.run_cli('install', '--no-shell')
        self.assertNotEqual(r.returncode, 0)
        self.assertNotIn('安装完成', r.stdout)
        self.assertEqual((self.root / 'bin/mihomo').read_bytes(), before)


@unittest.skipUnless(os.environ.get('SP_TEST_MIHOMO'), '设置 SP_TEST_MIHOMO 可运行真实内核验证')
class RealKernelTests(unittest.TestCase):
    def test_real_config_listener_and_private_dns_route(self):
        real = os.path.abspath(os.environ['SP_TEST_MIHOMO'])
        with tempfile.TemporaryDirectory(prefix='sp-real-') as directory:
            root = Path(directory)
            dns = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
            dns.bind(('127.0.0.1', 0));dns.settimeout(.1)
            stop = threading.Event()
            queries = []
            def serve_dns():
                while not stop.is_set():
                    try:
                        packet, address = dns.recvfrom(4096)
                        queries.append(packet)
                        # A 查询返回私有地址；AAAA 查询返回空结果。
                        ipv4 = packet[-4:-2] == b'\x00\x01'
                        header = packet[:2] + b'\x81\x80' + packet[4:6] + (b'\x00\x01' if ipv4 else b'\x00\x00') + b'\x00\x00\x00\x00'
                        answer = b'\xc0\x0c\x00\x01\x00\x01\x00\x00\x00\x3c\x00\x04\x7f\x00\x00\x01' if ipv4 else b''
                        dns.sendto(header + packet[12:] + answer, address)
                    except socket.timeout:
                        pass
            class Handler(http.server.BaseHTTPRequestHandler):
                def do_GET(self):
                    self.send_response(200);self.end_headers();self.wfile.write(b'PRIVATE_OK')
                def log_message(self, *args):
                    pass
            server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
            dns_thread = threading.Thread(target=serve_dns);dns_thread.start()
            http_thread = threading.Thread(target=server.serve_forever);http_thread.start()
            process = None
            try:
                port = free_port()
                with mock.patch.object(builder, 'system_resolver', return_value=f'127.0.0.1:{dns.getsockname()[1]}'):
                    config = builder.merge(builder.load(SUBSCRIPTION), {'rules': ['MATCH,PROXY']}, port, [])
                builder.atomic_write(root / 'config.yaml', yaml.safe_dump(config))
                r = subprocess.run([real, '-t', '-d', directory, '-f', str(root / 'config.yaml')],
                                   capture_output=True, text=True, timeout=5)
                self.assertEqual(r.returncode, 0, r.stdout + r.stderr)
                with open(root / 'log', 'w') as log:
                    process = subprocess.Popen([real, '-d', directory, '-f', str(root / 'config.yaml')], stdout=log, stderr=log)
                    deadline = time.monotonic() + 5
                    while time.monotonic() < deadline:
                        try:
                            with socket.create_connection(('127.0.0.1', port), timeout=.1):break
                        except OSError:time.sleep(.05)
                    r = subprocess.run(['curl', '-sS', '--max-time', '3', '--noproxy', '',
                                        '--proxy', f'http://127.0.0.1:{port}',
                                        f'http://private-review.invalid:{server.server_port}/'],
                                       capture_output=True, text=True, timeout=5)
                    self.assertEqual(r.returncode, 0, r.stderr)
                    self.assertEqual(r.stdout, 'PRIVATE_OK')
                    self.assertTrue(queries)
                    self.assertIn('DIRECT', (root / 'log').read_text())
                    if sys.platform.startswith('linux'):
                        inodes = set()
                        for entry in Path(f'/proc/{process.pid}/fd').iterdir():
                            try:
                                link = os.readlink(entry)
                                if link.startswith('socket:['):inodes.add(link[8:-1])
                            except FileNotFoundError:pass
                        listeners = []
                        for table in ('tcp', 'tcp6'):
                            for line in Path('/proc/net/' + table).read_text().splitlines()[1:]:
                                fields = line.split()
                                if fields[3] == '0A' and fields[9] in inodes:listeners.append(fields[1])
                        self.assertEqual(listeners, [f'0100007F:{port:04X}'])
            finally:
                if process is not None:
                    process.terminate();process.wait(timeout=3)
                stop.set();dns_thread.join();dns.close()
                server.shutdown();server.server_close();http_thread.join()


if __name__ == '__main__':
    unittest.main(verbosity=2)
