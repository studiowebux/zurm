package zserver

import (
	"bytes"
	"io"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		msgType uint8
		payload []byte
	}{
		{"output with data", MsgOutput, []byte("hello world")},
		{"input with data", MsgInput, []byte{0x1B, '[', 'A'}},
		{"empty payload", MsgListSessions, nil},
		{"single byte", MsgError, []byte{0xFF}},
		{"large payload", MsgOutput, bytes.Repeat([]byte("x"), 65536)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteMessage(&buf, tt.msgType, tt.payload); err != nil {
				t.Fatalf("WriteMessage: %v", err)
			}

			msg, err := ReadMessage(&buf)
			if err != nil {
				t.Fatalf("ReadMessage: %v", err)
			}

			if msg.Type != tt.msgType {
				t.Errorf("Type = %d, want %d", msg.Type, tt.msgType)
			}
			if !bytes.Equal(msg.Payload, tt.payload) {
				t.Errorf("Payload length = %d, want %d", len(msg.Payload), len(tt.payload))
			}
		})
	}
}

func TestWriteReadMultipleMessages(t *testing.T) {
	var buf bytes.Buffer

	msgs := []struct {
		t uint8
		p []byte
	}{
		{MsgCreateSession, []byte(`{"shell":"/bin/zsh"}`)},
		{MsgSessionInfo, []byte(`{"id":"abc123"}`)},
		{MsgOutput, []byte("terminal output here")},
	}

	for _, m := range msgs {
		if err := WriteMessage(&buf, m.t, m.p); err != nil {
			t.Fatalf("WriteMessage: %v", err)
		}
	}

	for i, want := range msgs {
		got, err := ReadMessage(&buf)
		if err != nil {
			t.Fatalf("ReadMessage[%d]: %v", i, err)
		}
		if got.Type != want.t {
			t.Errorf("[%d] Type = %d, want %d", i, got.Type, want.t)
		}
		if !bytes.Equal(got.Payload, want.p) {
			t.Errorf("[%d] Payload mismatch", i)
		}
	}
}

func TestReadMessage_TruncatedHeader(t *testing.T) {
	buf := bytes.NewReader([]byte{0x01, 0x02}) // only 2 bytes, need 5
	_, err := ReadMessage(buf)
	if err == nil {
		t.Error("expected error for truncated header")
	}
}

func TestReadMessage_EmptyReader(t *testing.T) {
	buf := bytes.NewReader(nil)
	_, err := ReadMessage(buf)
	if err == nil {
		t.Error("expected error for empty reader")
	}
}

func TestReadMessage_PayloadTooLarge(t *testing.T) {
	var buf bytes.Buffer
	// Write a header claiming 128 MB payload (exceeds 64 MB limit).
	hdr := make([]byte, 5)
	hdr[0] = 0x00
	hdr[1] = 0x00
	hdr[2] = 0x00
	hdr[3] = 0x08 // 0x08000000 = 128 MB
	hdr[4] = MsgOutput
	buf.Write(hdr)

	_, err := ReadMessage(&buf)
	if err == nil {
		t.Error("expected error for payload > 64MB")
	}
}

func TestReadMessage_TruncatedPayload(t *testing.T) {
	var buf bytes.Buffer
	// Header says 100 bytes, but only provide 5.
	hdr := make([]byte, 5)
	hdr[0] = 100 // payload length = 100
	hdr[4] = MsgOutput
	buf.Write(hdr)
	buf.Write([]byte("short"))

	_, err := ReadMessage(&buf)
	if err == nil {
		t.Error("expected error for truncated payload")
	}
}

func TestWriteMessage_WriterError(t *testing.T) {
	w := &failWriter{failAfter: 0}
	err := WriteMessage(w, MsgOutput, []byte("test"))
	if err == nil {
		t.Error("expected error when writer fails on header")
	}
}

func TestWriteMessage_PayloadWriterError(t *testing.T) {
	w := &failWriter{failAfter: 1} // succeed on header, fail on payload
	err := WriteMessage(w, MsgOutput, []byte("test"))
	if err == nil {
		t.Error("expected error when writer fails on payload")
	}
}

type failWriter struct {
	failAfter int
	calls     int
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.calls >= f.failAfter {
		return 0, io.ErrClosedPipe
	}
	f.calls++
	return len(p), nil
}
