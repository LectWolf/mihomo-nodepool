package traffic

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
)

// NICPath 测试时可替换
var NICPath = "/proc/net/dev"

type nicSample struct{ rx, tx int64 }

// NICSampler 读取网卡累计字节，返回与上次读取的差值；容器内看到的是容器自己的网卡
type NICSampler struct {
	mu   sync.Mutex
	prev map[string]nicSample
}

func readNIC() (map[string]nicSample, error) {
	f, err := os.Open(NICPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]nicSample{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		fields := strings.Fields(rest)
		// 宿主机网络模式下跳过 Docker 网桥，只看物理网卡
		if name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") || len(fields) < 9 {
			continue
		}
		rx, e1 := strconv.ParseInt(fields[0], 10, 64)
		tx, e2 := strconv.ParseInt(fields[8], 10, 64)
		if e1 == nil && e2 == nil {
			out[name] = nicSample{rx, tx}
		}
	}
	return out, sc.Err()
}

// Sample 第一次只记录基线；计数回绕或网卡重建时以当前值为增量
func (s *NICSampler) Sample(m *Meter) {
	cur, err := readNIC()
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prev != nil {
		for name, v := range cur {
			p, ok := s.prev[name]
			if !ok {
				continue
			}
			rx, tx := v.rx-p.rx, v.tx-p.tx
			if rx < 0 {
				rx = v.rx
			}
			if tx < 0 {
				tx = v.tx
			}
			c := m.Counter(KindNIC, name)
			c.AddIn(rx)
			c.AddOut(tx)
		}
	}
	s.prev = cur
}
