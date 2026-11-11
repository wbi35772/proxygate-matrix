package subscription

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

const (
	vlessCommandTCP  = 0x01
	vlessCommandUDP  = 0x02
	vlessMaxAddress  = 255
	vlessDialTimeout = 25 * time.Second
)

type vlessRequest struct {
	uuid    string
	command byte
	host    string
	port    uint16
}

func (s *Server) handleVLESS(writer http.ResponseWriter, r *http.Request) {
	if !isWebSocketRequest(r) {
		writer.Header().Set("Upgrade", "websocket")
		http.Error(writer, "websocket upgrade required", http.StatusUpgradeRequired)
		return
	}
	connection, err := upgradeWebSocket(writer, r)
	if err != nil {
		s.logger.Printf("[subscription] websocket upgrade from %s: %v", r.RemoteAddr, err)
		return
	}
	defer connection.Close()

	request, err := readVLESSRequest(connection)
	if err != nil {
		s.logger.Printf("[subscription] vless request from %s: %v", r.RemoteAddr, err)
		return
	}
	slotID, exists := s.slotID(request.uuid)
	if !exists {
		s.logger.Printf("[subscription] unknown vless uuid from %s", r.RemoteAddr)
		return
	}

	target := net.JoinHostPort(request.host, strconv.Itoa(int(request.port)))
	switch request.command {
	case vlessCommandTCP:
		ctx, cancel := context.WithTimeout(context.Background(), vlessDialTimeout)
		upstream, dialErr := s.matrix.DialSlot(ctx, slotID, "tcp", target)
		cancel()
		if dialErr != nil {
			s.logger.Printf("[subscription] slot=%d dial %s: %v", slotID, target, dialErr)
			return
		}
		defer upstream.Close()
		if _, err := connection.Write([]byte{0, 0}); err != nil {
			return
		}
		relayStream(connection, upstream)
	case vlessCommandUDP:
		s.relayDatagrams(connection, slotID, target)
	}
}

func readVLESSRequest(reader io.Reader) (vlessRequest, error) {
	var prefix [18]byte
	if _, err := io.ReadFull(reader, prefix[:]); err != nil {
		return vlessRequest{}, err
	}
	if prefix[0] != 0 {
		return vlessRequest{}, fmt.Errorf("unsupported vless version %d", prefix[0])
	}
	if addonLength := int(prefix[17]); addonLength > 0 {
		if _, err := io.CopyN(io.Discard, reader, int64(addonLength)); err != nil {
			return vlessRequest{}, err
		}
	}
	var command [1]byte
	if _, err := io.ReadFull(reader, command[:]); err != nil {
		return vlessRequest{}, err
	}
	if command[0] != vlessCommandTCP && command[0] != vlessCommandUDP {
		return vlessRequest{}, fmt.Errorf("unsupported vless command %d", command[0])
	}
	var port [2]byte
	if _, err := io.ReadFull(reader, port[:]); err != nil {
		return vlessRequest{}, err
	}
	host, err := readVLESSAddress(reader)
	if err != nil {
		return vlessRequest{}, err
	}
	return vlessRequest{
		uuid:    uuidString(prefix[1:17]),
		command: command[0],
		host:    host,
		port:    binary.BigEndian.Uint16(port[:]),
	}, nil
}

func readVLESSAddress(reader io.Reader) (string, error) {
	var kind [1]byte
	if _, err := io.ReadFull(reader, kind[:]); err != nil {
		return "", err
	}
	switch kind[0] {
	case 1:
		var raw [4]byte
		if _, err := io.ReadFull(reader, raw[:]); err != nil {
			return "", err
		}
		return net.IP(raw[:]).String(), nil
	case 2:
		var length [1]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return "", err
		}
		if length[0] == 0 || int(length[0]) > vlessMaxAddress {
			return "", errors.New("invalid vless domain length")
		}
		raw := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, raw); err != nil {
			return "", err
		}
		return string(raw), nil
	case 3:
		var raw [16]byte
		if _, err := io.ReadFull(reader, raw[:]); err != nil {
			return "", err
		}
		return net.IP(raw[:]).String(), nil
	default:
		return "", fmt.Errorf("unsupported vless address type %d", kind[0])
	}
}

func relayStream(client *wsConn, upstream net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(upstream, client)
		if closer, ok := upstream.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		done <- struct{}{}
	}()
	<-done
	_ = client.Close()
	_ = upstream.Close()
	<-done
}

func (s *Server) relayDatagrams(client *wsConn, slotID int, target string) {
	packets, err := s.matrix.ListenSlotPacket(slotID, "udp4", ":0")
	if err != nil {
		s.logger.Printf("[subscription] slot=%d open datagram socket: %v", slotID, err)
		return
	}
	defer packets.Close()

	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return
	}
	address := &net.UDPAddr{Port: port}
	if ip := net.ParseIP(host); ip != nil {
		address.IP = ip
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		addresses, resolveErr := s.matrix.ResolveSlotIPv4(ctx, slotID, host)
		cancel()
		if resolveErr != nil || len(addresses) == 0 {
			s.logger.Printf("[subscription] slot=%d resolve udp target %s: %v", slotID, host, resolveErr)
			return
		}
		address.IP = addresses[0]
	}
	if _, err := client.Write([]byte{0, 0}); err != nil {
		return
	}

	go func() {
		buffer := make([]byte, 65535)
		for {
			read, _, readErr := packets.ReadFrom(buffer)
			if readErr != nil {
				break
			}
			frame := make([]byte, 2+read)
			binary.BigEndian.PutUint16(frame, uint16(read))
			copy(frame[2:], buffer[:read])
			if _, writeErr := client.Write(frame); writeErr != nil {
				break
			}
		}
		_ = client.Close()
	}()

	var header [2]byte
	for {
		if _, err := io.ReadFull(client, header[:]); err != nil {
			break
		}
		size := int(binary.BigEndian.Uint16(header[:]))
		if size == 0 {
			continue
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(client, payload); err != nil {
			break
		}
		if _, err := packets.WriteTo(payload, address); err != nil {
			break
		}
	}
}
