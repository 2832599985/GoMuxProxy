package proxy

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"strings"
)

// tunnelCopyPooled is a simple bidirectional tunnel using pooled buffers (no byte counters).
func tunnelCopyPooled(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		copyPooled(b, a)
		b.Close()
		done <- struct{}{}
	}()
	go func() {
		copyPooled(a, b)
		a.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
}

func hasPort(host string) bool {
	if strings.HasPrefix(host, "[") {
		return strings.LastIndex(host, ":") > strings.LastIndex(host, "]")
	}
	return strings.Count(host, ":") == 1
}

type bufReader struct {
	r *bufio.Reader
}

func newBufReader(r io.Reader) *bufReader {
	return &bufReader{r: bufio.NewReader(r)}
}

func (b *bufReader) Buffered() int {
	return b.r.Buffered()
}

func (b *bufReader) Read(p []byte) (int, error) {
	return b.r.Read(p)
}

func readHTTPResponse(br *bufReader) (*http.Response, error) {
	return http.ReadResponse(br.r, nil)
}

func newMultiReader(head []byte, rest io.Reader) io.Reader {
	return io.MultiReader(bytes.NewReader(head), rest)
}
