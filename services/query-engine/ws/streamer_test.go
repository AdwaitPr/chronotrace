package ws

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/chronotrace/chronotrace/shared/types"
)

func TestBinaryFrame_EncodeAndDecode(t *testing.T) {
	tsMs := time.Now().UnixMilli()
	state := &types.PointInTimeState{
		TimestampUnixNano: tsMs * 1e6,
		ActiveSpans:       42,
		TotalThroughput:   120.5,
		Nodes: []types.ServiceNode{
			{Name: "payment-worker", RPS: 45.0, Status: "critical", IsRootCause: true},
		},
	}

	frameBytes, err := EncodeBinaryFrame(MsgTypeStateFrame, tsMs, state)
	if err != nil {
		t.Fatalf("EncodeBinaryFrame failed: %v", err)
	}

	// Header checks: 13 bytes total header (1B type + 8B ts + 4B len)
	if len(frameBytes) < 13 {
		t.Fatalf("frame too short: %d bytes", len(frameBytes))
	}

	msgType := frameBytes[0]
	if msgType != MsgTypeStateFrame {
		t.Errorf("expected msgType %d, got %d", MsgTypeStateFrame, msgType)
	}

	decodedTs := int64(binary.BigEndian.Uint64(frameBytes[1:9]))
	if decodedTs != tsMs {
		t.Errorf("expected timestamp %d, got %d", tsMs, decodedTs)
	}

	payloadLen := binary.BigEndian.Uint32(frameBytes[9:13])
	if int(payloadLen) != len(frameBytes)-13 {
		t.Errorf("payload length mismatch: header says %d, actual is %d", payloadLen, len(frameBytes)-13)
	}

	decodedState, err := DecodeStatePayload(frameBytes[13:])
	if err != nil {
		t.Fatalf("DecodeStatePayload failed: %v", err)
	}

	if decodedState.ActiveSpans != 42 || len(decodedState.Nodes) != 1 {
		t.Errorf("decoded state mismatch: %+v", decodedState)
	}
}

func TestDecodeScrubRequest(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(MsgTypeScrubRequest)
	targetMs := int64(1700000000123)
	tsBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBytes, uint64(targetMs))
	buf.Write(tsBytes)

	parsedTs, err := ParseScrubRequest(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseScrubRequest failed: %v", err)
	}

	if parsedTs != targetMs {
		t.Errorf("expected timestamp %d, got %d", targetMs, parsedTs)
	}
}
