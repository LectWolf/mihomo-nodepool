package core

import (
	"context"
	"net"
	"net/netip"

	"github.com/LectWolf/mihomo-nodepool/backend/internal/traffic"
	"github.com/metacubex/mihomo/component/dialer"
)

// meteredDialer 节点的底层连接计入 tag 当前指向的分类；opts 在解析节点后取节点自己的拨号选项
type meteredDialer struct {
	tag  *traffic.Tag
	opts []dialer.Option
}

func (d *meteredDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	c, err := dialer.NewDialer(d.opts...).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return traffic.WrapConn(c, d.tag.Counter()), nil
}

func (d *meteredDialer) ListenPacket(ctx context.Context, network, address string, rAddrPort netip.AddrPort) (net.PacketConn, error) {
	pc, err := dialer.NewDialer(d.opts...).ListenPacket(ctx, network, address, rAddrPort)
	if err != nil {
		return nil, err
	}
	return traffic.WrapPacketConn(pc, d.tag.Counter()), nil
}
