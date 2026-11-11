// Package frontproxy chains a slot's VPN transport through an upstream proxy.
// SoftEther and SSTP speak TLS over TCP, so a local TCP forwarder can hand the
// session to a SOCKS5/HTTP proxy, another slot's tunnel, or an API-provided
// proxy list without touching the VPN drivers.
package frontproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dialTimeout = 15 * time.Second
	apiTTL      = 2 * time.Minute
	apiMaxBody  = 1 << 20
)

type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

func (f DialFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return f(ctx, network, address)
}

// FromURL parses socks5://, socks5h://, http:// and https:// proxy URLs. A
// bare host:port is treated as SOCKS5. A comma or space separated list yields a
// multi dialer that tries each entry in order.
func FromURL(raw string) (Dialer, error) {
	entries := splitList(raw)
	if len(entries) == 0 {
		return nil, errors.New("front proxy URL is empty")
	}
	dialers := make([]Dialer, 0, len(entries))
	for _, entry := range entries {
		dialer, err := singleDialer(entry)
		if err != nil {
			return nil, err
		}
		dialers = append(dialers, dialer)
	}
	if len(dialers) == 1 {
		return dialers[0], nil
	}
	return &multiDialer{dialers: dialers}, nil
}

func splitList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(symbol rune) bool {
		return symbol == ',' || symbol == ';' || symbol == '\n' || symbol == '\r' || symbol == ' ' || symbol == '\t'
	})
	entries := make([]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			entries = append(entries, field)
		}
	}
	return entries
}

func singleDialer(raw string) (Dialer, error) {
	if !strings.Contains(raw, "://") {
		raw = "socks5://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse front proxy %q: %w", raw, err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "socks5", "socks5h", "socks":
		return &socks5Dialer{proxy: parsed}, nil
	case "http", "https":
		return &httpDialer{proxy: parsed}, nil
	default:
		return nil, fmt.Errorf("unsupported front proxy scheme %q", parsed.Scheme)
	}
}

type multiDialer struct {
	dialers []Dialer
}

func (m *multiDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var lastErr error
	for _, dialer := range m.dialers {
		connection, err := dialer.DialContext(ctx, network, address)
		if err == nil {
			return connection, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no front proxy candidates")
	}
	return nil, lastErr
}

type socks5Dialer struct {
	proxy *url.URL
}

func (d *socks5Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if !strings.HasPrefix(network, "tcp") {
		return nil, fmt.Errorf("front proxy supports tcp only, got %q", network)
	}
	dialer := net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", proxyAddress(d.proxy, "1080"))
	if err != nil {
		return nil, fmt.Errorf("dial front proxy %s: %w", d.proxy.Host, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	} else {
		_ = connection.SetDeadline(time.Now().Add(dialTimeout))
	}
	if err := d.handshake(connection, address); err != nil {
		_ = connection.Close()
		return nil, err
	}
	_ = connection.SetDeadline(time.Time{})
	return connection, nil
}

func (d *socks5Dialer) handshake(connection net.Conn, address string) error {
	methods := []byte{0x00}
	if d.proxy.User != nil {
		methods = []byte{0x00, 0x02}
	}
	greeting := append([]byte{0x05, byte(len(methods))}, methods...)
	if _, err := connection.Write(greeting); err != nil {
		return err
	}
	var choice [2]byte
	if _, err := io.ReadFull(connection, choice[:]); err != nil {
		return err
	}
	if choice[0] != 0x05 {
		return fmt.Errorf("front proxy returned version %d", choice[0])
	}
	switch choice[1] {
	case 0x00:
	case 0x02:
		if d.proxy.User == nil {
			return errors.New("front proxy requested username/password but none is configured")
		}
		username := d.proxy.User.Username()
		password, _ := d.proxy.User.Password()
		if len(username) > 255 || len(password) > 255 {
			return errors.New("front proxy credentials are too long")
		}
		request := []byte{0x01, byte(len(username))}
		request = append(request, username...)
		request = append(request, byte(len(password)))
		request = append(request, password...)
		if _, err := connection.Write(request); err != nil {
			return err
		}
		var reply [2]byte
		if _, err := io.ReadFull(connection, reply[:]); err != nil {
			return err
		}
		if reply[1] != 0x00 {
			return errors.New("front proxy rejected credentials")
		}
	case 0xFF:
		return errors.New("front proxy accepts none of the offered auth methods")
	default:
		return fmt.Errorf("front proxy chose unsupported auth method %d", choice[1])
	}

	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("split front proxy target %q: %w", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid front proxy target port %q", portText)
	}
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
		if ipv4 := ip.To4(); ipv4 != nil {
			request = append(request, 0x01)
			request = append(request, ipv4...)
		} else {
			request = append(request, 0x04)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return errors.New("front proxy target host is too long")
		}
		request = append(request, 0x03, byte(len(host)))
		request = append(request, host...)
	}
	request = append(request, byte(port>>8), byte(port))
	if _, err := connection.Write(request); err != nil {
		return err
	}
	var head [4]byte
	if _, err := io.ReadFull(connection, head[:]); err != nil {
		return err
	}
	if head[0] != 0x05 {
		return fmt.Errorf("front proxy CONNECT returned version %d", head[0])
	}
	if head[1] != 0x00 {
		return fmt.Errorf("front proxy CONNECT failed with code %d", head[1])
	}
	var length int
	switch head[3] {
	case 0x01:
		length = 4
	case 0x04:
		length = 16
	case 0x03:
		var size [1]byte
		if _, err := io.ReadFull(connection, size[:]); err != nil {
			return err
		}
		length = int(size[0])
	default:
		return fmt.Errorf("front proxy CONNECT returned address type %d", head[3])
	}
	discard := make([]byte, length+2)
	_, err = io.ReadFull(connection, discard)
	return err
}

type httpDialer struct {
	proxy *url.URL
}

func (d *httpDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if !strings.HasPrefix(network, "tcp") {
		return nil, fmt.Errorf("front proxy supports tcp only, got %q", network)
	}
	target := proxyAddress(d.proxy, "80")
	secure := strings.EqualFold(d.proxy.Scheme, "https")
	if secure {
		target = proxyAddress(d.proxy, "443")
	}
	dialer := net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}
	connection, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		return nil, fmt.Errorf("dial front proxy %s: %w", target, err)
	}
	if secure {
		host := d.proxy.Hostname()
		tlsConnection := tls.Client(connection, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			_ = connection.Close()
			return nil, fmt.Errorf("front proxy TLS handshake: %w", err)
		}
		connection = tlsConnection
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	} else {
		_ = connection.SetDeadline(time.Now().Add(dialTimeout))
	}
	request := "CONNECT " + address + " HTTP/1.1\r\nHost: " + address + "\r\n"
	if d.proxy.User != nil {
		password, _ := d.proxy.User.Password()
		credentials := base64.StdEncoding.EncodeToString([]byte(d.proxy.User.Username() + ":" + password))
		request += "Proxy-Authorization: Basic " + credentials + "\r\n"
	}
	request += "Proxy-Connection: keep-alive\r\n\r\n"
	if _, err := io.WriteString(connection, request); err != nil {
		_ = connection.Close()
		return nil, err
	}
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("front proxy CONNECT response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		_ = connection.Close()
		return nil, fmt.Errorf("front proxy CONNECT rejected with %s", response.Status)
	}
	_ = connection.SetDeadline(time.Time{})
	return &bufferedConn{Conn: connection, reader: reader}, nil
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(buffer []byte) (int, error) {
	return c.reader.Read(buffer)
}

// APIDialer fetches a proxy list from an HTTP endpoint and rotates through it.
// Accepted bodies: ["socks5://..."], {"proxies":[...]} (string or {"url":...}
// entries), or one proxy per line.
type APIDialer struct {
	URL   string
	Token string

	mu        sync.Mutex
	dialers   []Dialer
	fetchedAt time.Time
}

func (a *APIDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	dialers, err := a.candidates(ctx)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, dialer := range dialers {
		connection, err := dialer.DialContext(ctx, network, address)
		if err == nil {
			return connection, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = errors.New("front proxy API returned no usable proxies")
	}
	return nil, lastErr
}

func (a *APIDialer) candidates(ctx context.Context) ([]Dialer, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.dialers) > 0 && time.Since(a.fetchedAt) < apiTTL {
		return a.dialers, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return nil, err
	}
	if a.Token != "" {
		request.Header.Set("Authorization", "Bearer "+a.Token)
	}
	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch front proxy API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("front proxy API returned %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, apiMaxBody))
	if err != nil {
		return nil, err
	}
	entries, err := parseAPIProxies(body)
	if err != nil {
		return nil, err
	}
	dialers := make([]Dialer, 0, len(entries))
	for _, entry := range entries {
		dialer, err := singleDialer(entry)
		if err != nil {
			continue
		}
		dialers = append(dialers, dialer)
	}
	if len(dialers) == 0 {
		return nil, errors.New("front proxy API returned no usable proxies")
	}
	a.dialers, a.fetchedAt = dialers, time.Now()
	return dialers, nil
}

func parseAPIProxies(body []byte) ([]string, error) {
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
		var list []string
		if err := json.Unmarshal([]byte(trimmed), &list); err == nil && len(list) > 0 {
			return list, nil
		}
		var wrapper struct {
			Proxies []json.RawMessage `json:"proxies"`
		}
		if err := json.Unmarshal([]byte(trimmed), &wrapper); err == nil && len(wrapper.Proxies) > 0 {
			entries := make([]string, 0, len(wrapper.Proxies))
			for _, raw := range wrapper.Proxies {
				var text string
				if err := json.Unmarshal(raw, &text); err == nil && text != "" {
					entries = append(entries, text)
					continue
				}
				var object struct {
					URL string `json:"url"`
				}
				if err := json.Unmarshal(raw, &object); err == nil && object.URL != "" {
					entries = append(entries, object.URL)
				}
			}
			if len(entries) > 0 {
				return entries, nil
			}
		}
		return nil, errors.New("front proxy API JSON has no proxy entries")
	}
	lines := splitList(trimmed)
	if len(lines) == 0 {
		return nil, errors.New("front proxy API response is empty")
	}
	return lines, nil
}

func proxyAddress(proxy *url.URL, defaultPort string) string {
	host := proxy.Hostname()
	port := proxy.Port()
	if port == "" {
		port = defaultPort
	}
	return net.JoinHostPort(host, port)
}

// Forwarder listens on loopback and tunnels every accepted TCP connection to
// target through dialer.
type Forwarder struct {
	listener net.Listener
	dialer   Dialer
	target   string
	logger   *log.Logger

	closeOnce sync.Once
	closed    chan struct{}
	wg        sync.WaitGroup
}

func StartForwarder(dialer Dialer, target string, logger *log.Logger) (*Forwarder, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start front proxy forwarder: %w", err)
	}
	forwarder := &Forwarder{
		listener: listener,
		dialer:   dialer,
		target:   target,
		logger:   logger,
		closed:   make(chan struct{}),
	}
	go forwarder.serve()
	return forwarder, nil
}

func (f *Forwarder) Port() int {
	if address, ok := f.listener.Addr().(*net.TCPAddr); ok {
		return address.Port
	}
	return 0
}

func (f *Forwarder) serve() {
	for {
		connection, err := f.listener.Accept()
		if err != nil {
			select {
			case <-f.closed:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.handle(connection)
		}()
	}
}

func (f *Forwarder) handle(connection net.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	upstream, err := f.dialer.DialContext(ctx, "tcp", f.target)
	if err != nil {
		if f.logger != nil {
			f.logger.Printf("[frontproxy] dial %s: %v", f.target, err)
		}
		_ = connection.Close()
		return
	}
	relay(connection, upstream)
}

func relay(left, right net.Conn) {
	done := make(chan struct{}, 2)
	copyDirection := func(destination, source net.Conn) {
		_, _ = io.Copy(destination, source)
		if closer, ok := destination.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyDirection(right, left)
	go copyDirection(left, right)
	<-done
	_ = left.Close()
	_ = right.Close()
	<-done
}

func (f *Forwarder) Close() error {
	var err error
	f.closeOnce.Do(func() {
		close(f.closed)
		err = f.listener.Close()
	})
	f.wg.Wait()
	return err
}
