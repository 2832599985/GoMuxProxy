package proxy

import (
	"fmt"
	"io"
	"net"
	"time"
)

// handleMixed 自动检测协议：读取第一个字节判断是 SOCKS5 还是 HTTP
// SOCKS5 第一个字节是 0x05，HTTP 第一个字节是方法名的 ASCII (G/P/C/H 等)
func handleMixed(client net.Conn, upstreamProxy string, upstreamTimeout, detectTimeout int) (protocol string, target string, err error) {
	buf := make([]byte, 1)
	client.SetReadDeadline(time.Now().Add(time.Duration(detectTimeout) * time.Second))
	_, err = io.ReadFull(client, buf)
	client.SetReadDeadline(time.Time{})
	if err != nil {
		return "", "", fmt.Errorf("读取首字节失败: %w", err)
	}

	firstByte := buf[0]

	// 包装连接，把首字节放回去
	protoConn := &mixedConn{
		first: firstByte,
		Conn:  client,
	}

	if firstByte == 0x05 {
		target, err = handleSocks5(protoConn)
		return ProtoSocks5, target, err
	}

	target, _, err = handleHTTPProxy(protoConn, upstreamProxy, upstreamTimeout)
	return ProtoHTTP, target, err
}

type mixedConn struct {
	first    byte
	consumed bool
	net.Conn
}

func (c *mixedConn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if !c.consumed {
		p[0] = c.first
		c.consumed = true
		if len(p) == 1 {
			return 1, nil
		}
		n, err := c.Conn.Read(p[1:])
		if n > 0 {
			return n + 1, nil
		}
		return 1, err
	}
	return c.Conn.Read(p)
}
