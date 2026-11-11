package matrix

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/ExpTechTW/proxygate/internal/frontproxy"
)

// frontProxyDialer builds the upstream dialer used to chain SoftEther/SSTP
// transports. It returns false when chaining is disabled or misconfigured so the
// slot falls back to a direct connection.
func (m *Manager) frontProxyDialer(slotID int) (frontproxy.Dialer, bool) {
	settings := m.config.Get()
	if !settings.Matrix.FrontProxyEnabled {
		return nil, false
	}
	timeout := 15 * time.Second
	if parsed, err := time.ParseDuration(settings.Matrix.FrontProxyTimeout); err == nil && parsed > 0 {
		timeout = parsed
	}
	kind := strings.ToLower(strings.TrimSpace(settings.Matrix.FrontProxyType))
	switch kind {
	case "slot":
		targetID := settings.Matrix.FrontProxySlot
		if targetID <= 0 || targetID == slotID {
			return nil, false
		}
		slot, exists := m.slotByID(targetID)
		if !exists {
			return nil, false
		}
		status := slot.Status()
		if status.State != StateReady {
			return nil, false
		}
		return &timeoutDialer{timeout: timeout, inner: frontproxy.DialFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
			return slot.DialContext(ctx, network, address)
		})}, true
	case "api":
		endpoint := strings.TrimSpace(settings.Matrix.FrontProxyAPI)
		if endpoint == "" {
			return nil, false
		}
		return &timeoutDialer{timeout: timeout, inner: &frontproxy.APIDialer{URL: endpoint}}, true
	case "", "socks5", "socks5h", "http", "https", "list":
		raw := strings.TrimSpace(settings.Matrix.FrontProxyURL)
		if raw == "" {
			return nil, false
		}
		dialer, err := frontproxy.FromURL(raw)
		if err != nil {
			m.logger.Printf("[matrix] front proxy config: %v", err)
			return nil, false
		}
		return &timeoutDialer{timeout: timeout, inner: dialer}, true
	default:
		// A bare URL or host:port was supplied as the type by mistake; accept it.
		if dialer, err := frontproxy.FromURL(kind); err == nil {
			return &timeoutDialer{timeout: timeout, inner: dialer}, true
		}
		return nil, false
	}
}

type timeoutDialer struct {
	timeout time.Duration
	inner   frontproxy.Dialer
}

func (d *timeoutDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()
	connection, err := d.inner.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return connection, nil
}
