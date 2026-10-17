// Package cnip 判断 IP 是否属于中国大陆（数据来自 APNIC，用 scripts/update-cnip.py 更新）
package cnip

import (
	_ "embed"
	"encoding/binary"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed cn.txt
var data string

type span struct{ start, end uint32 }

var (
	once sync.Once
	v4   []span
	v6   []netip.Prefix
)

func load() {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if start, count, ok := strings.Cut(line, " "); ok {
			ip := net.ParseIP(start).To4()
			n, err := strconv.ParseUint(count, 10, 32)
			if ip == nil || err != nil || n == 0 {
				continue
			}
			s := binary.BigEndian.Uint32(ip)
			v4 = append(v4, span{s, s + uint32(n-1)})
			continue
		}
		if p, err := netip.ParsePrefix(line); err == nil {
			v6 = append(v6, p)
		}
	}
	sort.Slice(v4, func(i, j int) bool { return v4[i].start < v4[j].start })
}

// Contains 返回 ip 是否为中国大陆地址；无法解析时返回 false
func Contains(ip string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	once.Do(load)
	addr = addr.Unmap()
	if addr.Is4() {
		b := addr.As4()
		x := binary.BigEndian.Uint32(b[:])
		i := sort.Search(len(v4), func(i int) bool { return v4[i].end >= x })
		return i < len(v4) && v4[i].start <= x
	}
	for _, p := range v6 {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}
