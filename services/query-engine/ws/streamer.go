package ws

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/chronotrace/chronotrace/services/query-engine/timetravel"
	"github.com/chronotrace/chronotrace/shared/types"
	"github.com/gofiber/websocket/v2"
)

const (
	MsgTypeScrubRequest byte = 0x01
	MsgTypeStateFrame   byte = 0x02
	MsgTypeHeartbeat    byte = 0x03
)

// EncodeBinaryFrame serializes a binary frame: [1B msg_type][8B timestamp_ms][4B payload_len][payload].
func EncodeBinaryFrame(msgType byte, timestampMs int64, state *types.PointInTimeState) ([]byte, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal state payload: %w", err)
	}

	totalLen := 1 + 8 + 4 + len(payload)
	buf := make([]byte, totalLen)

	buf[0] = msgType
	binary.BigEndian.PutUint64(buf[1:9], uint64(timestampMs))
	binary.BigEndian.PutUint32(buf[9:13], uint32(len(payload)))
	copy(buf[13:], payload)

	return buf, nil
}

// DecodeStatePayload decodes the payload portion of a binary state frame.
func DecodeStatePayload(data []byte) (*types.PointInTimeState, error) {
	var state types.PointInTimeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// ParseScrubRequest extracts the target timestamp_ms from a client binary scrub frame.
func ParseScrubRequest(data []byte) (int64, error) {
	if len(data) < 9 {
		return 0, fmt.Errorf("scrub frame too short (%d bytes)", len(data))
	}
	if data[0] != MsgTypeScrubRequest {
		return 0, fmt.Errorf("unexpected msg_type: 0x%x", data[0])
	}
	tsMs := int64(binary.BigEndian.Uint64(data[1:9]))
	return tsMs, nil
}

// HandleScrubberWebSocket manages a high-frequency DAW scrubbing session with latest-wins coalescing.
func HandleScrubberWebSocket(reconstructor *timetravel.StateReconstructor) func(*websocket.Conn) {
	return func(c *websocket.Conn) {
		defer c.Close()

		var pendingTs atomic.Int64
		notifyChan := make(chan struct{}, 1)
		closeChan := make(chan struct{})

		// Background worker with request coalescing
		go func() {
			defer close(closeChan)
			for {
				select {
				case <-notifyChan:
					targetMs := pendingTs.Swap(0)
					if targetMs <= 0 {
						continue
					}

					// Compute reconstructed state at target timestamp
					targetNano := targetMs * 1e6
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					state, err := reconstructor.ReconstructState(ctx, targetNano, 30)
					cancel()

					if err != nil || state == nil {
						continue
					}

					// Encode binary frame
					frame, err := EncodeBinaryFrame(MsgTypeStateFrame, targetMs, state)
					if err != nil {
						continue
					}

					// Send binary frame to client
					if err := c.WriteMessage(websocket.BinaryMessage, frame); err != nil {
						return
					}
				}
			}
		}()

		// Reader loop
		for {
			msgType, data, err := c.ReadMessage()
			if err != nil {
				break
			}

			if msgType == websocket.BinaryMessage {
				targetMs, err := ParseScrubRequest(data)
				if err == nil && targetMs > 0 {
					// Coalesce: write latest timestamp into atomic slot
					pendingTs.Store(targetMs)
					select {
					case notifyChan <- struct{}{}:
					default:
						// Already notified; worker will pick up newest atomic value
					}
				}
			} else if msgType == websocket.TextMessage {
				// Fallback for JSON requests: {"ts": 1700000000000}
				var req struct {
					TS int64 `json:"ts"`
				}
				if err := json.Unmarshal(data, &req); err == nil && req.TS > 0 {
					pendingTs.Store(req.TS)
					select {
					case notifyChan <- struct{}{}:
					default:
					}
				}
			}
		}
	}
}
