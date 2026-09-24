package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// MCServer : mini serveur Minecraft pilotable depuis le panel.
type MCServer struct {
	mu       sync.RWMutex
	host     string
	port     int
	motd     string
	version  string
	protocol int
	maxPlay  int

	listener net.Listener
	running  bool
	started  time.Time

	// Stats
	pings        int
	loginTries   int
	logs         []string
}

func NewMCServer(host string, port int) *MCServer {
	return &MCServer{
		host:     host,
		port:     port,
		motd:     "§aMon serveur Go",
		version:  "1.21",
		protocol: 767,
		maxPlay:  20,
		logs:     make([]string, 0, 200),
	}
}

func (s *MCServer) log(format string, args ...any) {
	line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	s.mu.Lock()
	s.logs = append(s.logs, line)
	if len(s.logs) > 200 {
		s.logs = s.logs[len(s.logs)-200:]
	}
	s.mu.Unlock()
}

func (s *MCServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return fmt.Errorf("déjà démarré")
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.host, s.port))
	if err != nil {
		return err
	}
	s.listener = ln
	s.running = true
	s.started = time.Now()

	go s.acceptLoop(ln)
	s.log("Serveur démarré sur %s:%d", s.host, s.port)
	return nil
}

func (s *MCServer) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return fmt.Errorf("déjà arrêté")
	}
	_ = s.listener.Close()
	s.running = false
	s.log("Serveur arrêté")
	return nil
}

func (s *MCServer) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

func (s *MCServer) SetMOTD(m string) {
	s.mu.Lock()
	s.motd = m
	s.mu.Unlock()
}

func (s *MCServer) Snapshot() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	uptime := 0
	if s.running && !s.started.IsZero() {
		uptime = int(time.Since(s.started).Seconds())
	}
	logsCopy := append([]string(nil), s.logs...)
	return map[string]any{
		"running":     s.running,
		"port":        s.port,
		"motd":        s.motd,
		"max_players": s.maxPlay,
		"uptime":      uptime,
		"pings":       s.pings,
		"logins":      s.loginTries,
		"logs":        logsCopy,
	}
}

func (s *MCServer) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener fermé
		}
		go s.handle(conn)
	}
}

func (s *MCServer) handle(conn net.Conn) {
	defer conn.Close()
	addr := conn.RemoteAddr().String()

	state, err := s.readHandshake(conn)
	if err != nil {
		s.log("Handshake échoué %s : %v", addr, err)
		return
	}

	switch state {
	case 1:
		s.handleStatus(conn, addr)
	case 2:
		s.handleLogin(conn, addr)
	}
}

// readHandshake lit le packet Handshake et renvoie next_state (1 = status, 2 = login).
func (s *MCServer) readHandshake(conn net.Conn) (int, error) {
	r, err := readPacket(conn)
	if err != nil {
		return 0, err
	}
	if pid, _ := readVarInt(r); pid != 0x00 {
		return 0, fmt.Errorf("packet ID inattendu")
	}
	_, _ = readVarInt(r)   // protocol version
	_, _ = readString(r)   // server address
	_, _ = readUint16(r)   // port
	return readVarInt(r)   // next state
}

func (s *MCServer) handleStatus(conn net.Conn, addr string) {
	s.mu.Lock()
	s.pings++
	motd := s.motd
	version := s.version
	protocol := s.protocol
	maxPlay := s.maxPlay
	s.mu.Unlock()
	s.log("Ping depuis %s", addr)

	if _, err := readPacket(conn); err != nil { // status request
		return
	}

	resp := map[string]any{
		"version":     map[string]any{"name": version, "protocol": protocol},
		"players":     map[string]any{"max": maxPlay, "online": 0, "sample": []any{}},
		"description": map[string]any{"text": motd},
	}
	body, _ := json.Marshal(resp)

	var buf strings.Builder
	writeVarInt(&buf, 0x00)
	writeString(&buf, string(body))
	writePacket(conn, buf.String())

	// Ping -> Pong
	pr, err := readPacket(conn)
	if err != nil {
		return
	}
	pid, _ := readVarInt(pr)
	if pid != 0x01 {
		return
	}
	payload := make([]byte, 8)
	if _, err := io.ReadFull(pr, payload); err != nil {
		return
	}
	var pong strings.Builder
	writeVarInt(&pong, 0x01)
	pong.Write(payload)
	writePacket(conn, pong.String())
}

func (s *MCServer) handleLogin(conn net.Conn, addr string) {
	s.mu.Lock()
	s.loginTries++
	s.mu.Unlock()
	s.log("Tentative de connexion depuis %s", addr)

	if _, err := readPacket(conn); err != nil { // Login Start
		return
	}

	msg := `{"text":"§cCe serveur n'accepte pas encore le jeu."}`
	var buf strings.Builder
	writeVarInt(&buf, 0x00)
	writeString(&buf, msg)
	writePacket(conn, buf.String())
}

// ------------------------- Protocole helpers -------------------------

type packetReader struct {
	r   io.Reader
	buf [1]byte
}

func readPacket(r io.Reader) (io.Reader, error) {
	length, err := readVarIntReader(r)
	if err != nil {
		return nil, err
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	return strings.NewReader(string(data)), nil
}

func readVarIntReader(r io.Reader) (int, error) {
	var result int
	var b [1]byte
	for i := 0; i < 5; i++ {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		result |= int(b[0]&0x7F) << (7 * i)
		if b[0]&0x80 == 0 {
			return result, nil
		}
	}
	return 0, fmt.Errorf("VarInt trop long")
}

func readVarInt(r io.Reader) (int, error) { return readVarIntReader(r) }

func readString(r io.Reader) (string, error) {
	n, err := readVarInt(r)
	if err != nil {
		return "", err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func readUint16(r io.Reader) (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b[:]), nil
}

func writeVarInt(w io.Writer, v int) {
	for {
		b := byte(v & 0x7F)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		w.Write([]byte{b})
		if v == 0 {
			return
		}
	}
}

func writeString(w io.Writer, s string) {
	writeVarInt(w, len(s))
	w.Write([]byte(s))
}

func writePacket(w io.Writer, body string) {
	var frame strings.Builder
	writeVarInt(&frame, len(body))
	frame.WriteString(body)
	w.Write([]byte(frame.String()))
}