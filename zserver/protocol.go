package zserver

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	MsgCreateSession uint8 = 0x01
	MsgAttachSession uint8 = 0x02
	MsgDetachSession uint8 = 0x03
	MsgInput         uint8 = 0x04
	MsgOutput        uint8 = 0x05
	MsgResize        uint8 = 0x06
	MsgListSessions  uint8 = 0x07
	MsgSessionDead   uint8 = 0x08
	MsgSessionInfo   uint8 = 0x09
	MsgError         uint8 = 0x0A
	MsgSessionList   uint8 = 0x0B
)

// Message is a framed protocol message.
type Message struct {
	Type    uint8
	Payload []byte
}

// CreateSessionRequest is the JSON payload for MsgCreateSession.
type CreateSessionRequest struct {
	Shell string   `json:"shell"`
	Args  []string `json:"args"`
	Cols  int      `json:"cols"`
	Rows  int      `json:"rows"`
	Dir   string   `json:"dir"`
	Env   []string `json:"env"`
}

// AttachSessionRequest is the JSON payload for MsgAttachSession.
type AttachSessionRequest struct {
	ID string `json:"id"`
}

// SessionInfo is returned by the server after CreateSession or AttachSession.
type SessionInfo struct {
	ID   string `json:"id"`
	PID  int    `json:"pid"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
	Dir  string `json:"dir"`
}

// WriteMessage writes a length-prefixed message: [4-byte LE length][1-byte type][payload].
func WriteMessage(w io.Writer, msgType uint8, payload []byte) error {
	hdr := make([]byte, 5)
	binary.LittleEndian.PutUint32(hdr[:4], uint32(len(payload))) // #nosec G115 — payload length bounded by 64MB check in ReadMessage
	hdr[4] = msgType
	if _, err := w.Write(hdr); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return fmt.Errorf("write payload: %w", err)
		}
	}
	return nil
}

// ReadMessage reads one length-prefixed message from r.
func ReadMessage(r io.Reader) (Message, error) {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return Message{}, fmt.Errorf("read header: %w", err)
	}
	length := binary.LittleEndian.Uint32(hdr[:4])
	msgType := hdr[4]
	var payload []byte
	if length > 0 {
		const maxPayload = 64 * 1024 * 1024 // 64 MB sanity limit
		if length > maxPayload {
			return Message{}, fmt.Errorf("payload too large: %d bytes", length)
		}
		payload = make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return Message{}, fmt.Errorf("read payload: %w", err)
		}
	}
	return Message{Type: msgType, Payload: payload}, nil
}
