#!/usr/bin/env python3
"""订阅只提供节点；运行范围、DNS、规则由本地策略决定。"""
from __future__ import annotations

import argparse
import os
from pathlib import Path
import shlex
import sys
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parent / "vendor"))
sys.path.insert(0, str(Path(os.environ.get("SP_DATA_DIR", str(Path.home() / ".local/share/simple-proxy"))) / "vendor"))
try:
    import yaml
except ImportError:
    sys.exit("❌ 缺少 PyYAML，请重新运行 install.sh，或安装 requirements.txt 中的依赖")

DEFAULT_DNS_FALLBACKS = ["https://doh.pub/dns-query", "https://dns.alidns.com/dns-query"]
PRIVATE_RULES = [
    "DOMAIN-SUFFIX,localhost,DIRECT",
    "DOMAIN-SUFFIX,local,DIRECT",
    "DOMAIN-SUFFIX,svc,DIRECT",
    "DOMAIN-SUFFIX,cluster.local,DIRECT",
    "IP-CIDR,127.0.0.0/8,DIRECT",
    "IP-CIDR,10.0.0.0/8,DIRECT",
    "IP-CIDR,172.16.0.0/12,DIRECT",
    "IP-CIDR,192.168.0.0/16,DIRECT",
    "IP-CIDR,169.254.0.0/16,DIRECT",
    "IP-CIDR6,::1/128,DIRECT",
    "IP-CIDR6,fc00::/7,DIRECT",
    "IP-CIDR6,fe80::/10,DIRECT",
]


class UniqueLoader(yaml.SafeLoader):
    pass


def unique_mapping(loader, node, deep=False):
    loader.flatten_mapping(node)
    result = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node, deep=deep)
        if key in result:
            raise ValueError(f"YAML 字段重复: {key}")
        result[key] = loader.construct_object(value_node, deep=deep)
    return result


UniqueLoader.add_constructor(yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, unique_mapping)


def load(text):
    value = yaml.load(text, Loader=UniqueLoader)
    if not isinstance(value, dict):
        raise ValueError("配置必须是 YAML 对象")
    return value


def system_resolver():
    try:
        for line in Path('/etc/resolv.conf').read_text().splitlines():
            parts = line.split()
            if len(parts) >= 2 and parts[0] == 'nameserver':
                return parts[1]
    except OSError:
        pass
    return None


def merge(subscription, policy, port, fallbacks, enable_api=False):
    if not 1 <= port <= 65535:
        raise ValueError("HTTP 端口必须在 1–65535 之间")
    rules = policy.get('rules')
    if not isinstance(rules, list) or not rules or not all(isinstance(r, str) for r in rules):
        raise ValueError("用户 rules 必须是非空字符串列表")
    # 与字段顺序、缩进和订阅 mode 无关。订阅仅能提供节点及其引用数据。
    config = {key: subscription[key] for key in (
        'proxies', 'proxy-providers', 'proxy-groups', 'rule-providers',
    ) if key in subscription}
    nodes = config.get('proxies', [])
    providers = config.get('proxy-providers', {})
    groups = config.get('proxy-groups', [])
    if not isinstance(nodes, list) or not isinstance(providers, dict) or not isinstance(groups, list):
        raise ValueError("订阅的节点、代理组或 provider 格式错误")
    names = [node['name'] for node in nodes if isinstance(node, dict)
             and isinstance(node.get('name'), str)
             and str(node.get('type', '')).lower() not in ('direct', 'reject', 'reject-drop')]
    if not names and not providers:
        raise ValueError("订阅中没有可用代理节点")
    group = {'name': 'PROXY', 'type': 'select'}
    if names:
        group['proxies'] = names
    if providers:
        group['use'] = list(providers)
    config['proxy-groups'] = [g for g in groups if isinstance(g, dict) and g.get('name') != 'PROXY'] + [group]
    config.update({'port': port, 'allow-lan': False, 'bind-address': '127.0.0.1',
                   'mode': 'rule', 'log-level': 'info', 'ipv6': False,
                   'profile': {'store-selected': False, 'store-fake-ip': False}})
    if enable_api:
        secret = subscription.get('secret')
        if not isinstance(secret, str) or not secret.strip():
            raise ValueError("启用控制 API 前，必须在订阅中设置非空 secret")
        config['external-controller'] = '127.0.0.1:9090'
        config['secret'] = secret
    resolver = system_resolver() or '223.5.5.5'
    config['dns'] = {'enable': True, 'ipv6': False, 'enhanced-mode': 'redir-host',
                     'default-nameserver': [resolver],
                     'nameserver': list(dict.fromkeys([resolver] + fallbacks))}
    # 内网豁免始终先于用户规则；主动解析域名，IP 规则才能兜底保护内网。
    config['rules'] = list(dict.fromkeys(PRIVATE_RULES + rules))
    return config


def atomic_write(path, text):
    path = Path(path)
    fd, temporary = tempfile.mkstemp(prefix=path.name + '.', suffix='.tmp', dir=path.parent)
    try:
        with os.fdopen(fd, 'w', encoding='utf-8') as stream:
            stream.write(text)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--subscription-config', required=True)
    parser.add_argument('--rules', required=True)
    parser.add_argument('--output', required=True)
    parser.add_argument('--http-port', type=int, default=7890)
    parser.add_argument('--dns-fallbacks', nargs='*', default=DEFAULT_DNS_FALLBACKS)
    parser.add_argument('--enable-api', default='0')
    parser.add_argument('--data-dir')  # 保留旧调用兼容性
    args = parser.parse_args()
    try:
        subscription = load(Path(args.subscription_config).read_text())
        policy = load(Path(args.rules).read_text())
        fallbacks = [item for arg in args.dns_fallbacks for item in shlex.split(arg)]
        config = merge(subscription, policy, args.http_port, fallbacks,
                       args.enable_api.lower() in ('1', 'true', 'yes'))
        atomic_write(args.output, yaml.safe_dump(config, allow_unicode=True, sort_keys=False))
    except (OSError, ValueError, yaml.YAMLError) as error:
        print(f'❌ 生成运行时配置失败: {error}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
