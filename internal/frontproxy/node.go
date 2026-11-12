package frontproxy

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// nodeDialer tunnels TCP through a vless:// or trojan:// node link that speaks
// WebSocket. It exists so the panel can accept a pasted share link from another
// gateway instead of requiring a bare SOCKS5 or HTTP proxy.
type nodeDialer struct {
	scheme   string
	server   string
	host     string
	path     string
	query    string
	secure   bool
	insecure bool
	uuid     [16]byte
	password string
}

func newNodeDialer(parsed *url.URL) (*nodeDialer, error) {
	scheme := strings.ToLower(parsed.Scheme)
	query := parsed.Query()
	kind := strings.ToLower(query.Get("type"))
	if kind == "" {
		kind = "tcp"
	}
	if kind != "ws" {
		return nil, fmt.Errorf("%s front proxy only supports type=ws, got %q", scheme, kind)
	}
	security := strings.ToLower(query.Get("security"))
	if security == "" {
		security = strings.ToLower(query.Get("tls"))
		if security == "tls" || security == "true" {
			security = "tls"
		}
	}
	switch security {
	case "", "none":
	case "tls":
	default:
		return nil, fmt.Errorf("%s front proxy does not support security=%q", scheme, security)
	}

	host := parsed.Hostname()
	if host == "" {
		return nil, errors.New("node link is missing a host")
	}
	port := parsed.Port()
	if port == "" {
		if security == "tls" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if _, err := strconv.Atoi(port); err != nil {
		return nil, fmt.Errorf("node link has an invalid port %q", port)
	}

	headerHost := strings.TrimSpace(query.Get("host"))
	if headerHost == "" {
		headerHost = host
	}
	path := query.Get("path")
	if path == "" {
		path = "/"
	}
	rawQuery := ""
	if index := strings.Index(path, "?"); index >= 0 {
		rawQuery = path[index+1:]
		path = path[:index]
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if headerHost != host && !strings.Contains(headerHost, ":") && security == "tls" {
		headerHost = net.JoinHostPort(headerHost, port)
	}

	dialer := &nodeDialer{
		scheme:   scheme,
		server:   net.JoinHostPort(host, port),
		host:     headerHost,
		path:     path,
		query:    rawQuery,
		secure:   security == "tls",
		insecure: truthyParam(query.Get("allowInsecure")) || truthyParam(query.Get("insecure")),
	}

	switch scheme {
	case "vless":
		if parsed.User == nil {
			return nil, errors.New("vless link is missing the user id")
		}
		dialer.uuid = parseNodeID(parsed.User.Username())
	case "trojan":
		if parsed.User == nil {
			return nil, errors.New("trojan link is missing the password")
		}
		password := parsed.User.Username()
		if password == "" {
			return nil, errors.New("trojan link is missing the password")
		}
		dialer.password = password
	default:
		return nil, fmt.Errorf("unsupported node scheme %q", scheme)
	}
	return dialer, nil
}

func truthyParam(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// parseNodeID accepts a UUID and falls back to Xray's free-form short id.
func parseNodeID(text string) [16]byte {
	var id [16]byte
	compact := strings.ReplaceAll(strings.TrimSpace(text), "-", "")
	if decoded, err := hex.DecodeString(compact); err == nil && len(decoded) == 16 {
		copy(id[:], decoded)
		return id
	}
	copy(id[:], text)
	return id
}

func (d *nodeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("%s front proxy only carries TCP, got %q", d.scheme, network)
	}
	dialer := net.Dialer{Timeout: dialTimeout}
	connection, err := dialer.DialContext(ctx, "tcp", d.server)
	if err != nil {
		return nil, fmt.Errorf("dial %s node %s: %w", d.scheme, d.server, err)
	}
	deadline := time.Now().Add(dialTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = connection.SetDeadline(deadline)

	if d.secure {
		serverName := d.host
		if host, _, splitErr := net.SplitHostPort(serverName); splitErr == nil {
			serverName = host
		}
		tlsConnection := tls.Client(connection, &tls.Config{
			ServerName:         serverName,
			InsecureSkipVerify: d.insecure,
			MinVersion:         tls.VersionTLS12,
		})
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			_ = connection.Close()
			return nil, fmt.Errorf("tls handshake with %s node %s: %w", d.scheme, d.server, err)
		}
		connection = tlsConnection
	}

	location := &url.URL{Scheme: "https", Host: d.host, Path: d.path, RawQuery: d.query}
	config, err := websocket.NewConfig(location.String(), "https://"+d.host)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("build websocket request: %w", err)
	}
	config.Header = http.Header{}
	if d.host != "" {
		config.Header.Set("X-Forwarded-Host", d.host)
	}
	tunnel, err := websocket.NewClient(config, connection)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("%s websocket handshake with %s: %w", d.scheme, d.server, err)
	}
	_ = connection.SetDeadline(time.Time{})

	switch d.scheme {
	case "vless":
		err = d.writeVlessHeader(tunnel, address)
	case "trojan":
		err = d.writeTrojanHeader(tunnel, address)
	}
	if err != nil {
		_ = tunnel.Close()
		_ = connection.Close()
		return nil, err
	}
	return &nodeStream{Conn: connection, tunnel: tunnel, vless: d.scheme == "vless"}, nil
}

func (d *nodeDialer) writeVlessHeader(writer io.ReadWriter, address string) error {
	host, port, err := splitTarget(address)
	if err != nil {
		return err
	}
	header := make([]byte, 0, 32+len(host))
	header = append(header, 0)
	header = append(header, d.uuid[:]...)
	header = append(header, 0)
	header = append(header, 0x01)
	header = append(header, byte(port>>8), byte(port))
	header = appendAddress(header, host)
	if _, err := writer.Write(header); err != nil {
		return fmt.Errorf("write vless request: %w", err)
	}
	return nil
}

func (d *nodeDialer) writeTrojanHeader(writer io.Writer, address string) error {
	host, port, err := splitTarget(address)
	if err != nil {
		return err
	}
	sum := sha256.Sum224([]byte(d.password))
	key := make([]byte, hex.EncodedLen(len(sum)))
	hex.Encode(key, sum[:])

	header := make([]byte, 0, len(key)+4+len(host)+4)
	header = append(header, key...)
	header = append(header, '\r', '\n')
	header = append(header, 0x01)
	header = appendAddress(header, host)
	header = append(header, byte(port>>8), byte(port))
	header = append(header, '\r', '\n')
	if _, err := writer.Write(header); err != nil {
		return fmt.Errorf("write trojan request: %w", err)
	}
	return nil
}

func appendAddress(buffer []byte, host string) []byte {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			buffer = append(buffer, 0x01)
			return append(buffer, v4...)
		}
		buffer = append(buffer, 0x03)
		return append(buffer, ip.To16()...)
	}
	if len(host) > 255 {
		host = host[:255]
	}
	buffer = append(buffer, 0x02, byte(len(host)))
	return append(buffer, host...)
}

func splitTarget(address string) (string, int, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("split target %q: %w", address, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("invalid target port %q", portText)
	}
	return host, port, nil
}

type nodeStream struct {
	net.Conn
	tunnel *websocket.Conn
	vless  bool
	once   sync.Once
	header error
}

func (s *nodeStream) Read(buffer []byte) (int, error) {
	if s.vless {
		s.once.Do(func() { s.header = discardVlessResponse(s.tunnel) })
		if s.header != nil {
			return 0, s.header
		}
	}
	return s.tunnel.Read(buffer)
}

// discardVlessResponse eats the two-byte VLESS response header (version and
// addon length) plus addons. Xray flushes it together with the first downlink
// payload, so it cannot be read before the caller has sent anything.
func discardVlessResponse(reader io.Reader) error {
	head := make([]byte, 2)
	if _, err := io.ReadFull(reader, head); err != nil {
		return fmt.Errorf("read vless response: %w", err)
	}
	if head[1] > 0 {
		addons := make([]byte, head[1])
		if _, err := io.ReadFull(reader, addons); err != nil {
			return fmt.Errorf("read vless response addons: %w", err)
		}
	}
	return nil
}

func (s *nodeStream) Write(buffer []byte) (int, error) { return s.tunnel.Write(buffer) }

func (s *nodeStream) Close() error {
	err := s.tunnel.Close()
	if closeErr := s.Conn.Close(); err == nil {
		err = closeErr
	}
	return err
}
