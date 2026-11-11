package vpn

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/ExpTechTW/proxygate/internal/config"
	"github.com/ExpTechTW/proxygate/internal/model"
)

// Tunnel owns exactly one VPN session for one matrix slot. It deliberately
// bypasses Manager so every slot can select a different exit at the same time.
type Tunnel struct {
	ctx      context.Context
	cancel   context.CancelFunc
	config   *config.Store
	logger   *log.Logger
	drivers  map[string]Driver
	onDown   func(string)
	mu       sync.RWMutex
	session  Session
	node     model.Node
	protocol string
	generation uint64
	closed   bool
}

func NewTunnel(parent context.Context, configStore *config.Store, logger *log.Logger, drivers []Driver, onDown func(string)) *Tunnel {
	ctx, cancel := context.WithCancel(parent)
	indexed := make(map[string]Driver, len(drivers))
	for _, driver := range drivers {
		indexed[driver.Protocol()] = driver
	}
	return &Tunnel{ctx: ctx, cancel: cancel, config: configStore, logger: logger, drivers: indexed, onDown: onDown}
}

// Connect establishes a fresh session, replacing and closing any previous one.
// A forced protocol is honored; otherwise the configured priority order is
// filtered by the protocols the node advertises.
func (t *Tunnel) Connect(ctx context.Context, node model.Node, forcedProtocol string) error {
	if ip := net.ParseIP(node.IP); ip == nil || ip.To4() == nil {
		return fmt.Errorf("node endpoint %q is not IPv4", node.IP)
	}
	settings := t.config.Get()
	protocols := settings.ProtocolPriority
	if forcedProtocol != "" {
		if !slices.Contains(node.Protocols, forcedProtocol) {
			return fmt.Errorf("node %s does not support protocol %s", node.IP, forcedProtocol)
		}
		protocols = []string{forcedProtocol}
	}
	connectTimeout, _ := settings.ConnectTimeoutDuration()
	var failures []error
	for _, protocol := range protocols {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !slices.Contains(node.Protocols, protocol) {
			continue
		}
		driver, ok := t.drivers[protocol]
		if !ok {
			failures = append(failures, fmt.Errorf("%s driver is unavailable", protocol))
			continue
		}
		attemptCtx, cancel := context.WithTimeout(ctx, connectTimeout)
		session, err := driver.Start(attemptCtx, node, settings, t.logger)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", protocol, err))
			continue
		}
		session = newDNSSession(session)
		if err := probeSessionHealth(ctx, session, settings); err != nil {
			_ = session.Close()
			failures = append(failures, fmt.Errorf("%s candidate health check: %w", protocol, err))
			continue
		}
		if err := t.install(session, node, protocol); err != nil {
			_ = session.Close()
			return err
		}
		t.logger.Printf("[matrix:vpn] connected node=%s country=%s protocol=%s", node.IP, node.CountryShort, protocol)
		return nil
	}
	if len(failures) == 0 {
		return errors.New("node has no enabled protocol with an available driver")
	}
	parts := make([]string, len(failures))
	for index, failure := range failures {
		parts[index] = failure.Error()
	}
	return errors.New(strings.Join(parts, "; "))
}

func (t *Tunnel) install(session Session, node model.Node, protocol string) error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errors.New("tunnel is closed")
	}
	old := t.session
	t.session, t.node, t.protocol = session, node, protocol
	t.generation++
	generation := t.generation
	t.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	go t.monitor(session, generation, node.IP)
	return nil
}

func (t *Tunnel) monitor(session Session, generation uint64, ip string) {
	err := session.Wait(t.ctx)
	if t.ctx.Err() != nil {
		return
	}
	t.mu.Lock()
	if t.closed || t.generation != generation || t.session != session {
		t.mu.Unlock()
		return
	}
	t.session = nil
	t.protocol = ""
	t.mu.Unlock()
	if err == nil {
		err = errors.New("VPN session ended")
	} else {
		err = fmt.Errorf("VPN session ended: %w", err)
	}
	t.logger.Printf("[matrix:vpn] node=%s down: %v", ip, err)
	if t.onDown != nil {
		t.onDown(ip)
	}
}

func (t *Tunnel) current() (Session, model.Node) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.session, t.node
}

func (t *Tunnel) Snapshot() (model.Node, string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.node, t.protocol, t.session != nil
}

func (t *Tunnel) ActiveIP() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.session == nil {
		return ""
	}
	return t.node.IP
}

func (t *Tunnel) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	session, _ := t.current()
	if session == nil {
		return nil, errors.New("no VPN session is active for this slot")
	}
	if strings.HasPrefix(network, "tcp") {
		return dialSessionIPv4(ctx, session, address, t.config.Get().DNSServers)
	}
	return session.DialContext(ctx, network, address)
}

func (t *Tunnel) ListenPacket(network, address string) (net.PacketConn, error) {
	session, _ := t.current()
	if session == nil {
		return nil, errors.New("no VPN session is active for this slot")
	}
	packets, ok := session.(packetSession)
	if !ok {
		return nil, errors.New("active VPN session does not support datagrams")
	}
	return packets.ListenPacket(network, address)
}

func (t *Tunnel) ResolveIPv4(ctx context.Context, host string) ([]net.IP, error) {
	session, _ := t.current()
	if session == nil {
		return nil, errors.New("no VPN session is active for this slot")
	}
	return resolveSessionIPv4(ctx, session, host, t.config.Get().DNSServers)
}

func (t *Tunnel) Close() error {
	t.cancel()
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	session := t.session
	t.session = nil
	t.generation++
	t.mu.Unlock()
	if session != nil {
		return session.Close()
	}
	return nil
}
