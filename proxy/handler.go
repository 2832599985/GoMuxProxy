package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var bufPool = sync.Pool{New: func() any { b := make([]byte, 32*1024); return &b }}

func copyPooled(dst io.Writer, src io.Reader) (int64, error) {
	bp := bufPool.Get().(*[]byte)
	defer bufPool.Put(bp)
	return io.CopyBuffer(dst, src, *bp)
}

func (e *ProxyEngine) serve(ln net.Listener, entry ListenEntry) {
	defer e.serveWg.Done()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if !e.running.Load() {
				return
			}
			e.logf("[%s] 接受连接错误: %v", entry.Address, err)
			continue
		}
		e.serveWg.Add(1)
		go func() {
			defer e.serveWg.Done()
			e.connSemaphore <- struct{}{}
			e.handleConn(conn, entry)
			<-e.connSemaphore
		}()
	}
}

func (e *ProxyEngine) handleConn(client net.Conn, entry ListenEntry) {
	defer client.Close()

	connID := e.nextConnID.Add(1)
	e.totalConns.Add(1)

	// Per-port atomic count
	ppVal, _ := e.perPortCount.LoadOrStore(entry.Address, &atomic.Int64{})
	ppCounter := ppVal.(*atomic.Int64)
	ppCounter.Add(1)
	defer ppCounter.Add(-1)

	ci := &ConnInfo{
		ID:         connID,
		Source:     client.RemoteAddr().String(),
		ListenAddr: entry.Address,
		StartTime:  time.Now(),
	}

	var target string
	var err error
	var detectedProto string
	var httpBR *bufio.Reader // buffered reader from HTTP CONNECT, may contain client data

	// Snapshot upstream proxy under lock
	e.mu.RLock()
	upstream := e.config.UpstreamProxy
	upstreamTimeout := e.config.UpstreamTimeout
	detectTimeout := e.config.MixedDetectTimeout
	e.mu.RUnlock()

	if upstreamTimeout <= 0 {
		upstreamTimeout = 10
	}
	if detectTimeout <= 0 {
		detectTimeout = 5
	}

	switch entry.Protocol {
	case ProtoSocks5:
		ci.Protocol = "SOCKS5"
		target, err = handleSocks5(client)
		// Reply is sent below after connectViaUpstream succeeds or fails.
	case ProtoHTTP:
		ci.Protocol = "HTTP"
		target, httpBR, err = handleHTTPProxy(client, upstream, upstreamTimeout)
	case ProtoMixed:
		detectedProto, target, err = handleMixed(client, upstream, upstreamTimeout, detectTimeout)
		if detectedProto == ProtoSocks5 {
			ci.Protocol = "SOCKS5"
		} else {
			ci.Protocol = "HTTP"
		}
	default:
		err = fmt.Errorf("未知协议: %s", entry.Protocol)
	}

	if err != nil {
		if entry.Protocol == ProtoSocks5 || (entry.Protocol == ProtoMixed && detectedProto == ProtoSocks5) {
			writeSocks5Reply(client, 0x01) // general failure
		}
		e.logf("[%s] #%d 握手失败: %v", entry.Address, connID, err)
		return
	}

	// Plain HTTP proxy handles its own tunnel internally; nothing more to do.
	if entry.Protocol == ProtoHTTP && httpBR == nil && target == "" {
		return
	}

	ci.Target = target
	e.activeConns.Store(connID, ci)
	if e.onConnect != nil {
		e.onConnect(*ci)
	}
	e.logf("[%s] #%d %s -> %s (%s)", entry.Address, connID, ci.Source, target, ci.Protocol)

	isSocks5 := entry.Protocol == ProtoSocks5 || (entry.Protocol == ProtoMixed && detectedProto == ProtoSocks5)
	upstreamErr := e.connectViaUpstream(client, target, ci, upstream, upstreamTimeout, httpBR)

	if isSocks5 {
		if upstreamErr != nil {
			writeSocks5Reply(client, 0x05) // connection refused
		} else {
			writeSocks5Reply(client, 0x00) // success
		}
	}

	if upstreamErr != nil {
		e.logf("[%s] #%d 隧道错误: %v", entry.Address, connID, upstreamErr)
	}

	e.activeConns.Delete(connID)
	e.totalBytesUp.Add(ci.BytesUp)
	e.totalBytesDown.Add(ci.BytesDown)
	if e.onDisconnect != nil {
		e.onDisconnect(*ci)
	}
	e.logf("[%s] #%d 已断开 (上行:%d 下行:%d)", entry.Address, connID, ci.BytesUp, ci.BytesDown)
}

func (e *ProxyEngine) connectViaUpstream(client net.Conn, target string, ci *ConnInfo, upstreamAddr string, timeoutSec int, httpBR *bufio.Reader) error {
	upstream, err := net.DialTimeout("tcp", upstreamAddr, time.Duration(timeoutSec)*time.Second)
	if err != nil {
		return fmt.Errorf("连接上游 %s: %w", upstreamAddr, err)
	}
	defer upstream.Close()

	var connectReq strings.Builder
	connectReq.WriteString("CONNECT ")
	connectReq.WriteString(target)
	connectReq.WriteString(" HTTP/1.1\r\nHost: ")
	connectReq.WriteString(target)
	connectReq.WriteString("\r\n\r\n")
	if _, err := upstream.Write([]byte(connectReq.String())); err != nil {
		return fmt.Errorf("发送 CONNECT: %w", err)
	}

	br := newBufReader(upstream)
	resp, err := readHTTPResponse(br)
	if err != nil {
		return fmt.Errorf("读取上游响应: %w", err)
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("上游 CONNECT %s 返回 %d", target, resp.StatusCode)
	}

	// Build client reader: prepend any bytes already buffered from the client
	// (by handleHTTPProxy's bufio.Reader), then the raw connection.
	var clientR io.Reader
	if httpBR != nil {
		clientR = io.MultiReader(httpBR, client)
	} else {
		clientR = client
	}

	// Prepend any bytes buffered from the upstream CONNECT response.
	if br.Buffered() > 0 {
		extra := make([]byte, br.Buffered())
		br.Read(extra)
		clientR = newMultiReader(extra, clientR)
	}

	e.tunnel(clientR, client, upstream, ci)
	return nil
}

func (e *ProxyEngine) tunnel(clientR io.Reader, clientW net.Conn, upstream net.Conn, ci *ConnInfo) {
	done := make(chan struct{}, 2)

	go func() {
		n, _ := copyPooled(upstream, clientR)
		atomic.AddInt64(&ci.BytesUp, n)
		upstream.Close()
		done <- struct{}{}
	}()
	go func() {
		n, _ := copyPooled(clientW, upstream)
		atomic.AddInt64(&ci.BytesDown, n)
		clientW.Close()
		done <- struct{}{}
	}()

	<-done
	<-done
}
