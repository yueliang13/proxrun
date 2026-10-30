#!/usr/bin/env python3
"""
simple-proxy · 运行时配置生成器

把「订阅配置」+「用户规则」合并成 mihomo 实际加载的运行时配置。

  config.yaml (订阅)  ┐
  rules.yaml  (用户)  ┼─►  config.runtime.yaml
  本地修正(DNS/端口)  ┘

设计要点:
  * 订阅配置被覆盖时，用户的规则不受影响（规则在独立文件里）
  * DNS 优先使用系统解析器（与直连行为一致），再叠加可配置的备用解析器
  * 默认只暴露 HTTP 代理端口，关闭控制 API
  * 输出文件权限 600（内含节点凭据）
"""
from __future__ import annotations

import argparse
import os
import re
import sys

DEFAULT_DNS_FALLBACKS = [
    "https://1.1.1.1/dns-query",
    "https://8.8.8.8/dns-query",
]


# ── 工具 ─────────────────────────────────────────────────────────────────────
def system_resolver() -> str | None:
    """读取系统 DNS（/etc/resolv.conf 的第一个 nameserver）。"""
    try:
        with open("/etc/resolv.conf", encoding="utf-8") as f:
            for line in f:
                line = line.strip()
                if line.startswith("nameserver"):
                    parts = line.split()
                    if len(parts) > 1:
                        return parts[1]
    except OSError:
        pass
    return None


def read(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return f.read()


# ── 规则注入 ─────────────────────────────────────────────────────────────────
def inject_rules(src: str, rules_text: str) -> str:
    """用用户规则整体替换订阅里的 rules 段。"""
    rules = rules_text.rstrip() + "\n"
    idx = src.find("\nrules:")
    if idx == -1:
        return src.rstrip() + "\n" + rules
    return src[:idx].rstrip() + "\n" + rules


# ── DNS 修正 ─────────────────────────────────────────────────────────────────
def patch_dns(src: str, fallbacks: list[str]) -> str:
    """
    订阅通常使用境外 DoH（1.1.1.1 / 8.8.8.8），在部分网络环境下不可直连，
    而直连域名需要本地解析 → 会整体解析失败。

    修正：首选系统解析器（与容器/主机直连行为一致），再叠加备用解析器。
    走代理的域名由远端解析，不受本地污染影响。
    """
    resolver = system_resolver()
    items = []
    if resolver:
        items.append(f"    - {resolver}\n")
    for fb in fallbacks:
        items.append(f"    - {fb}\n")

    default_items = f"    - {resolver}\n" if resolver else "    - 223.5.5.5\n"

    # nameserver 列表
    src = re.sub(
        r"(?m)^  nameserver:\n(?:    - .*\n)+",
        "  nameserver:\n" + "".join(items),
        src,
        count=1,
    )
    # default-nameserver（用于解析 nameserver 自身的主机名）
    src = re.sub(
        r"(?m)^  default-nameserver:\n(?:    - .*\n)+",
        "  default-nameserver:\n" + default_items,
        src,
        count=1,
    )
    return src


# ── 端口 / API 收窄 ──────────────────────────────────────────────────────────
def patch_ports(src: str, http_port: int, enable_api: bool) -> str:
    # mixed-port(同时开 SOCKS) → port(仅 HTTP)
    src = re.sub(r"(?m)^mixed-port:\s*\d+\s*$", f"port: {http_port}", src, count=1)
    src = re.sub(r"(?m)^socks-port:\s*\d+\s*$", "", src, count=1)
    if enable_api:
        src = re.sub(
            r"(?m)^# external-controller:.*$",
            f"external-controller: 127.0.0.1:9090",
            src,
            count=1,
        )
    else:
        src = re.sub(
            r"(?m)^external-controller:.*$",
            "# external-controller: 已关闭（减少暴露面）",
            src,
            count=1,
        )
        # secret 字段没有意义了
        src = re.sub(r"(?m)^secret:.*$\n?", "", src, count=1)
    return src


# ── 主流程 ───────────────────────────────────────────────────────────────────
def main() -> int:
    ap = argparse.ArgumentParser(description="生成 mihomo 运行时配置")
    ap.add_argument("--subscription-config", required=True)
    ap.add_argument("--rules", required=True)
    ap.add_argument("--output", required=True)
    ap.add_argument("--dns-fallbacks", nargs="*", default=DEFAULT_DNS_FALLBACKS)
    ap.add_argument("--http-port", type=int, default=7890)
    ap.add_argument("--enable-api", default="0")
    ap.add_argument("--data-dir", default=None)
    args = ap.parse_args()

    for p in (args.subscription_config, args.rules):
        if not os.path.exists(p):
            print(f"❌ 缺少文件: {p}", file=sys.stderr)
            return 1

    src = read(args.subscription_config)
    rules = read(args.rules)
    fallbacks = list(args.dns_fallbacks or [])

    merged = inject_rules(src, rules)
    merged = patch_dns(merged, fallbacks)
    merged = patch_ports(merged, args.http_port, str(args.enable_api) in ("1", "true", "yes"))

    tmp = args.output + ".tmp"
    with open(tmp, "w", encoding="utf-8") as f:
        f.write(merged)
    os.replace(tmp, args.output)
    try:
        os.chmod(args.output, 0o600)
    except OSError:
        pass

    n_rules = sum(1 for l in rules.splitlines() if l.strip().startswith("- "))
    print(f"✅ 运行时配置已生成: {args.output}", file=sys.stderr)
    print(f"   规则 {n_rules} 条 | HTTP 端口 {args.http_port} | 控制 API {'开' if str(args.enable_api) in ('1','true','yes') else '关'}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
