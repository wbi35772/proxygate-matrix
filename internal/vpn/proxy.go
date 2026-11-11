package vpn

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/model"
)

// proxyDriver turns a public SOCKS5 or HTTP CONNECT proxy into a VPN-like
// session. There is no permanent tunnel: every DialContext opens a fresh
// connection to the proxy and negotiates the requested target.
type proxyDriver struct{ protocol string }

func (d proxyDriver) Protocol() string { return d.protocol }

func (d proxyDriver) Start(ctx context.Context, node model.Node, settings config.Config, logger *log.Logger) (Session, error) {
	supported := false
	for _, protocol := range node.Protocols {
		if protocol == d.protocol {
			supported = true
			break
		}
	}
	if !supported {
		return nil, fmt.Errorf("node does not advertise %s", d.protocol)
	}
	port := node.Port
	if port == 0 {
		if d.protocol == model.ProtocolHTTP {
			port = 8080
		} else {
			port = 1080
		}
	}
	timeout, err := settings.ConnectTimeoutDuration()
	if err != nil || timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &proxySession{
		protocol: d.protocol,
		address:  net.JoinHostPort(node.IP, strconv.Itoa(port)),
		timeout:  timeout,
		closed:   make(chan struct{}),
	}, nil
}

type proxySession struct {
	protocol string
	address  string
	timeout  time.Duration
	closed   chan struct{}
	once     sync.Once
}

func (s *proxySession) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if !strings.HasPrefix(network, "tcp") {
		return nil, fmt.Errorf("%s proxy session supports TCP only, not %s", s.protocol, network)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: s.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", s.address)
	if err != nil {
		return nil, fmt.Errorf("dial %s proxy %s: %w", s.protocol, s.address, err)
	}
	deadline := time.Now().Add(s.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return nil, err
	}
	var result net.Conn = conn
	if s.protocol == model.ProtocolHTTP {
		result, err = httpConnectHandshake(conn, address)
	} else {
		err = socks5Handshake(conn, address)
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := result.SetDeadline(time.Time{}); err != nil {
		_ = result.Close()
		return nil, err
	}
	return result, nil
}

func (s *proxySession) Wait(ctx context.Context) error {
	select {
	case <-s.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *proxySession) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

// ResolveDirect lets proxy sessions resolve DNS locally. SOCKS5 and HTTP
// CONNECT proxies cannot carry the UDP DNS queries the VPN resolver emits, and
// both protocols accept IPv4 literals as the target anyway.
func (s *proxySession) ResolveDirect(ctx context.Context, host string) ([]net.IP, time.Duration, error) {
	addresses, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil, 0, err
	}
	if len(addresses) == 0 {
		return nil, 0, errors.New("host has no IPv4 address")
	}
	return addresses, time.Minute, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func socks5Handshake(conn net.Conn, address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("split SOCKS5 target %q: %w", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid SOCKS5 target port %q", portText)
	}
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return fmt.Errorf("SOCKS5 greeting: %w", err)
	}
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return fmt.Errorf("SOCKS5 greeting reply: %w", err)
	}
	if greeting[0] != 0x05 {
		return fmt.Errorf("SOCKS5 proxy returned version %d", greeting[0])
	}
	switch greeting[1] {
	case 0x00:
	case 0x02:
		return errors.New("SOCKS5 proxy requires username/password authentication")
	case 0xFF:
		return errors.New("SOCKS5 proxy accepts no supported authentication method")
	default:
		return fmt.Errorf("SOCKS5 proxy selected unsupported auth method 0x%02x", greeting[1])
	}
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, 0x01)
			request = append(request, ip4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("SOCKS5 target hostname exceeds 255 bytes")
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, host...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, err := conn.Write(request); err != nil {
		return fmt.Errorf("SOCKS5 connect request: %w", err)
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return fmt.Errorf("SOCKS5 connect reply: %w", err)
	}
	if header[0] != 0x05 {
		return errors.New("SOCKS5 proxy returned an invalid reply version")
	}
	if header[1] != 0x00 {
		return fmt.Errorf("SOCKS5 proxy rejected CONNECT with code 0x%02x", header[1])
	}
	switch header[3] {
	case 0x01:
		_, err = io.ReadFull(conn, make([]byte, 4+2))
	case 0x04:
		_, err = io.ReadFull(conn, make([]byte, 16+2))
	case 0x03:
		length := make([]byte, 1)
		if _, err = io.ReadFull(conn, length); err == nil {
			_, err = io.ReadFull(conn, make([]byte, int(length[0])+2))
		}
	default:
		return errors.New("SOCKS5 proxy returned an invalid address type")
	}
	if err != nil {
		return fmt.Errorf("SOCKS5 connect reply address: %w", err)
	}
	return nil
}

func httpConnectHandshake(conn net.Conn, address string) (net.Conn, error) {
	request := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Connection: keep-alive\r\nUser-Agent: proxygate/1.0\r\n\r\n", address, address)
	if _, err := io.WriteString(conn, request); err != nil {
		return nil, fmt.Errorf("HTTP CONNECT request: %w", err)
	}
	reader := bufio.NewReaderSize(conn, 4096)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("HTTP CONNECT reply: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(statusLine))
	if len(fields) < 2 {
		return nil, fmt.Errorf("HTTP proxy returned a malformed status line %q", strings.TrimSpace(statusLine))
	}
	code, err := strconv.Atoi(fields[1])
	if err != nil {
		return nil, fmt.Errorf("HTTP proxy returned a malformed status line %q", strings.TrimSpace(statusLine))
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("HTTP proxy rejected CONNECT with %s", strings.TrimSpace(statusLine))
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("HTTP CONNECT reply headers: %w", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}
