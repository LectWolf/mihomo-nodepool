#!/usr/bin/env python3
"""Regenerate backend/internal/cnip/cn.txt from APNIC delegated data.

Usage: python3 scripts/update-cnip.py [delegated-apnic-latest]
"""
import ipaddress, pathlib, sys, urllib.request

SRC = "https://ftp.apnic.net/stats/apnic/delegated-apnic-latest"
OUT = pathlib.Path(__file__).resolve().parent.parent / "backend/internal/cnip/cn.txt"

def main():
    if len(sys.argv) > 1:
        text = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
    else:
        text = urllib.request.urlopen(SRC, timeout=60).read().decode("utf-8")
    v4, v6 = [], []
    for line in text.splitlines():
        p = line.split("|")
        if len(p) < 7 or p[1] != "CN" or p[6] not in ("allocated", "assigned"):
            continue
        if p[2] == "ipv4":
            start = int(ipaddress.IPv4Address(p[3]))
            v4.append((start, start + int(p[4])))
        elif p[2] == "ipv6":
            v6.append(ipaddress.IPv6Network(f"{p[3]}/{p[4]}"))
    v4.sort()
    merged = []
    for s, e in v4:
        if merged and s <= merged[-1][1]:
            merged[-1][1] = max(merged[-1][1], e)
        else:
            merged.append([s, e])
    lines = [f"{ipaddress.IPv4Address(s)} {e - s}" for s, e in merged]
    lines += [str(n) for n in ipaddress.collapse_addresses(v6)]
    OUT.write_text("\n".join(lines) + "\n", encoding="utf-8", newline="\n")
    print(f"{len(merged)} IPv4 ranges, {len(lines) - len(merged)} IPv6 prefixes -> {OUT}")

if __name__ == "__main__":
    main()
