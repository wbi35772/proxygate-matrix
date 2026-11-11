package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Trojan-over-WebSocket lets clients carry UDP through the same public
// subscription endpoint. Current VLESS clients send mux/XUDP packets the
// gateway does not implement, while Trojan UDP is a plain length-framed
// datagram stream that mihomo, sing-box and Xray all support over WS.
const (
	trojanCommandTCP  = 0x01
	trojanCommandUDP  = 0x03
	trojanKeyLength   = 56
	trojanDialTimeout = 25 * time.Second
)

type trojanRequest struct {
	command byte
	host    string
	port    uint16
}

// trojanKey mirrors the reference implementation: hex(sha224(password)).
func trojanKey(password string) [trojanKeyLength]byte {
	hash := sha256.New224()
	_, _ = hash.Write([]byte(password))
	var key [trojanKeyLength]byte
	hex.Encode(key[:], hash.Sum(nil))
	return key
}

func (s *Server) handleTrojan(writer http.ResponseWriter, r *http.Request) {
	if !isWebSocketRequest(r) {
		writer.Header().Set("Upgrade", "websocket")
		http.Error(writer, "websocket upgrade required", http.StatusUpgradeRequired)
		return
	}
	connection, err := upgradeWebSocket(writer, r)
	if err != nil {
		s.logger.Printf("[subscription] trojan websocket upgrade from %s: %v", r.RemoteAddr, err)
		return
	}
	defer connection.Close()

	slotID, request, err := s.readTrojanRequest(connection)
	if err != nil {
		s.logger.Printf("[subscription] trojan request from %s: %v", r.RemoteAddr, err)
		return
	}
	target := net.JoinHostPort(request.host, strconv.Itoa(int(request.port)))
	switch request.command {
	case trojanCommandTCP:
		ctx, cancel := context.WithTimeout(context.Background(), trojanDialTimeout)
		upstream, dialErr := s.matrix.DialSlot(ctx, slotID, "tcp", target)
		cancel()
		if dialErr != nil {
			s.logger.Printf("[subscription] slot=%d trojan dial %s: %v", slotID, target, dialErr)
			return
		}
		defer upstream.Close()
		relayStream(connection, upstream)
	case trojanCommandUDP:
		s.relayTrojanDatagrams(connection, slotID)
	}
}

func (s *Server) readTrojanRequest(connection *wsConn) (int, trojanRequest, error) {
	var key [trojanKeyLength]byte
	if _, err := io.ReadFull(connection, key[:]); err != nil {
		return 0, trojanRequest{}, fmt.Errorf("read credentials: %w", err)
	}
	slotID, exists := s.trojanSlotID(key)
	if !exists {
		return 0, trojanRequest{}, errors.New("unknown trojan credentials")
	}
	if err := skipCRLF(connection); err != nil {
		return 0, trojanRequest{}, err
	}
	var command [1]byte
	if _, err := io.ReadFull(connection, command[:]); err != nil {
		return 0, trojanRequest{}, fmt.Errorf("read command: %w", err)
	}
	host, err := readTrojanAddress(connection)
	if err != nil {
		return 0, trojanRequest{}, err
	}
	var port [2]byte
	if _, err := io.ReadFull(connection, port[:]); err != nil {
		return 0, trojanRequest{}, fmt.Errorf("read port: %w", err)
	}
	if err := skipCRLF(connection); err != nil {
		return 0, trojanRequest{}, err
	}
	if command[0] != trojanCommandTCP && command[0] != trojanCommandUDP {
		return 0, trojanRequest{}, fmt.Errorf("unsupported trojan command %d", command[0])
	}
	return slotID, trojanRequest{
		command: command[0],
		host:    host,
		port:    binary.BigEndian.Uint16(port[:]),
	}, nil
}

func skipCRLF(reader io.Reader) error {
	var marker [2]byte
	if _, err := io.ReadFull(reader, marker[:]); err != nil {
		return fmt.Errorf("read crlf: %w", err)
	}
	return nil
}

// readTrojanAddress parses the SOCKS5-style address block used by Trojan
// (0x01 IPv4, 0x03 domain, 0x04 IPv6), which differs from VLESS numbering.
func readTrojanAddress(reader io.Reader) (string, error) {
	var kind [1]byte
	if _, err := io.ReadFull(reader, kind[:]); err != nil {
		return "", fmt.Errorf("read address type: %w", err)
	}
	switch kind[0] {
	case 0x01:
		var raw [4]byte
		if _, err := io.ReadFull(reader, raw[:]); err != nil {
			return "", fmt.Errorf("read ipv4 address: %w", err)
		}
		return net.IP(raw[:]).String(), nil
	case 0x03:
		var length [1]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return "", fmt.Errorf("read domain length: %w", err)
		}
		if length[0] == 0 {
			return "", errors.New("empty trojan domain")
		}
		raw := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, raw); err != nil {
			return "", fmt.Errorf("read domain: %w", err)
		}
		return string(raw), nil
	case 0x04:
		var raw [16]byte
		if _, err := io.ReadFull(reader, raw[:]); err != nil {
			return "", fmt.Errorf("read ipv6 address: %w", err)
		}
		return net.IP(raw[:]).String(), nil
	default:
		return "", fmt.Errorf("unsupported trojan address type %d", kind[0])
	}
}

func (s *Server) relayTrojanDatagrams(client *wsConn, slotID int) {
	packets, err := s.matrix.ListenSlotPacket(slotID, "udp4", ":0")
	if err != nil {
		s.logger.Printf("[subscription] slot=%d open trojan datagram socket: %v", slotID, err)
		return
	}
	defer packets.Close()

	// Trojan frames carry their own destination per packet, so one unconnected
	// socket on the slot tunnel serves every remote. Domain targets are resolved
	// through the tunnel and cached per session.
	resolved := make(map[string]net.IP)
	dialTarget := func(host string, port int) (*net.UDPAddr, bool) {
		address := &net.UDPAddr{Port: port}
		if ip := net.ParseIP(host); ip != nil {
			address.IP = ip
			return address, true
		}
		if cached, exists := resolved[host]; exists {
			address.IP = cached
			return address, true
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		addresses, resolveErr := s.matrix.ResolveSlotIPv4(ctx, slotID, host)
		cancel()
		if resolveErr != nil || len(addresses) == 0 {
			s.logger.Printf("[subscription] slot=%d resolve trojan udp target %s: %v", slotID, host, resolveErr)
			return nil, false
		}
		resolved[host] = addresses[0]
		address.IP = addresses[0]
		return address, true
	}

	go func() {
		buffer := make([]byte, 65535)
		for {
			read, from, readErr := packets.ReadFrom(buffer)
			if readErr != nil {
				break
			}
			frame, frameErr := trojanPacket(from, buffer[:read])
			if frameErr != nil {
				continue
			}
			if _, writeErr := client.Write(frame); writeErr != nil {
				break
			}
		}
		_ = client.Close()
	}()

	for {
		host, port, payload, readErr := readTrojanPacket(client)
		if readErr != nil {
			break
		}
		if len(payload) == 0 {
			continue
		}
		address, ok := dialTarget(host, port)
		if !ok {
			continue
		}
		if _, err := packets.WriteTo(payload, address); err != nil {
			break
		}
	}
}

// trojanPacket builds one datagram frame: ATYP + ADDR + PORT + LENGTH + CRLF + payload.
func trojanPacket(from net.Addr, payload []byte) ([]byte, error) {
	udpAddr, ok := from.(*net.UDPAddr)
	if !ok {
		return nil, fmt.Errorf("unexpected datagram source %T", from)
	}
	ip := udpAddr.IP
	if ip4 := ip.To4(); ip4 != nil {
		frame := make([]byte, 0, 1+4+2+2+2+len(payload))
		frame = append(frame, 0x01)
		frame = append(frame, ip4...)
		frame = binary.BigEndian.AppendUint16(frame, uint16(udpAddr.Port))
		frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
		frame = append(frame, '\r', '\n')
		frame = append(frame, payload...)
		return frame, nil
	}
	if ip16 := ip.To16(); ip16 != nil {
		frame := make([]byte, 0, 1+16+2+2+2+len(payload))
		frame = append(frame, 0x04)
		frame = append(frame, ip16...)
		frame = binary.BigEndian.AppendUint16(frame, uint16(udpAddr.Port))
		frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
		frame = append(frame, '\r', '\n')
		frame = append(frame, payload...)
		return frame, nil
	}
	return nil, errors.New("datagram source has no usable IP")
}

func readTrojanPacket(reader io.Reader) (string, int, []byte, error) {
	host, err := readTrojanAddress(reader)
	if err != nil {
		return "", 0, nil, err
	}
	var port [2]byte
	if _, err := io.ReadFull(reader, port[:]); err != nil {
		return "", 0, nil, fmt.Errorf("read packet port: %w", err)
	}
	var length [2]byte
	if _, err := io.ReadFull(reader, length[:]); err != nil {
		return "", 0, nil, fmt.Errorf("read packet length: %w", err)
	}
	if err := skipCRLF(reader); err != nil {
		return "", 0, nil, err
	}
	size := int(binary.BigEndian.Uint16(length[:]))
	if size == 0 {
		return host, int(binary.BigEndian.Uint16(port[:])), nil, nil
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return "", 0, nil, fmt.Errorf("read packet payload: %w", err)
	}
	return host, int(binary.BigEndian.Uint16(port[:])), payload, nil
}
