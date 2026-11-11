package subscription

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// WebSocket framing is implemented locally: the gateway only needs a binary
// byte stream, and avoiding an extra module keeps the scratch image small.
const (
	wsOpcodeContinuation = 0x0
	wsOpcodeText         = 0x1
	wsOpcodeBinary       = 0x2
	wsOpcodeClose        = 0x8
	wsOpcodePing         = 0x9
	wsOpcodePong         = 0xA
	wsMaxFramePayload    = 1 << 20
	wsMaxMessagePayload  = 8 << 20
)

var errWebSocketClosed = errors.New("websocket closed")

type wsConn struct {
	connection net.Conn
	reader     *bufio.Reader
	writeMu    sync.Mutex
	pending    []byte
	closed     bool
}

func upgradeWebSocket(writer http.ResponseWriter, r *http.Request) (*wsConn, error) {
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		return nil, errors.New("missing Sec-WebSocket-Key")
	}
	if version := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")); version != "13" {
		return nil, fmt.Errorf("unsupported websocket version %q", version)
	}
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		return nil, errors.New("websocket hijacking is unavailable")
	}
	connection, buffered, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	if _, err := fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		_ = connection.Close()
		return nil, err
	}
	if err := buffered.Flush(); err != nil {
		_ = connection.Close()
		return nil, err
	}
	if tcp, ok := connection.(*net.TCPConn); ok {
		_ = tcp.SetKeepAlive(true)
		_ = tcp.SetKeepAlivePeriod(30 * time.Second)
	}
	return &wsConn{connection: connection, reader: buffered.Reader}, nil
}

func (c *wsConn) Read(target []byte) (int, error) {
	for {
		if len(c.pending) > 0 {
			read := copy(target, c.pending)
			c.pending = c.pending[read:]
			return read, nil
		}
		opcode, payload, err := c.readFrame()
		if err != nil {
			return 0, err
		}
		switch opcode {
		case wsOpcodeClose:
			return 0, io.EOF
		case wsOpcodePing:
			if err := c.writeFrame(wsOpcodePong, payload); err != nil {
				return 0, err
			}
			continue
		case wsOpcodePong:
			continue
		case wsOpcodeText, wsOpcodeBinary, wsOpcodeContinuation:
			if len(payload) == 0 {
				continue
			}
			c.pending = payload
		default:
			return 0, fmt.Errorf("unsupported websocket opcode %d", opcode)
		}
	}
}

func (c *wsConn) readFrame() (byte, []byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(c.reader, header[:]); err != nil {
		return 0, nil, err
	}
	if header[0]&0x70 != 0 {
		return 0, nil, errors.New("websocket reserved bits are set")
	}
	opcode := header[0] & 0x0f
	masked := header[1]&0x80 != 0
	length := int64(header[1] & 0x7f)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(c.reader, extended[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(c.reader, extended[:]); err != nil {
			return 0, nil, err
		}
		length = int64(binary.BigEndian.Uint64(extended[:]))
	}
	if length < 0 || length > wsMaxMessagePayload {
		return 0, nil, fmt.Errorf("websocket frame of %d bytes is too large", length)
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.reader, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for index := range payload {
			payload[index] ^= mask[index%4]
		}
	}
	return opcode, payload, nil
}

func (c *wsConn) Write(payload []byte) (int, error) {
	if err := c.writeFrame(wsOpcodeBinary, payload); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return errWebSocketClosed
	}
	first := true
	for first || len(payload) > 0 {
		chunk := payload
		if len(chunk) > wsMaxFramePayload {
			chunk = chunk[:wsMaxFramePayload]
		}
		payload = payload[len(chunk):]
		header := make([]byte, 0, 10)
		firstByte := opcode
		if !first {
			firstByte = wsOpcodeContinuation
		}
		if len(payload) == 0 {
			firstByte |= 0x80
		}
		header = append(header, firstByte)
		switch {
		case len(chunk) < 126:
			header = append(header, byte(len(chunk)))
		case len(chunk) <= 0xffff:
			header = append(header, 126, byte(len(chunk)>>8), byte(len(chunk)))
		default:
			header = append(header, 127)
			header = binary.BigEndian.AppendUint64(header, uint64(len(chunk)))
		}
		if _, err := c.connection.Write(append(header, chunk...)); err != nil {
			return err
		}
		first = false
	}
	return nil
}

func (c *wsConn) Close() error {
	c.writeMu.Lock()
	if c.closed {
		c.writeMu.Unlock()
		return nil
	}
	c.closed = true
	c.writeMu.Unlock()
	return c.connection.Close()
}
