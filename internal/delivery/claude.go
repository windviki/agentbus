package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/windviki/agentbus/internal/bus"
)

func pushClaude(ctx context.Context, s bus.Session, msgs []bus.Msg) error {
	address := strings.TrimPrefix(s.Delivery.Address, "uds:")
	if address == "" || !strings.HasPrefix(address, "/") {
		return fmt.Errorf("invalid Claude messaging socket")
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", address)
	if err != nil {
		return fmt.Errorf("connect Claude inbox: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	enc := json.NewEncoder(conn)
	if err := enc.Encode(map[string]string{"type": "auth", "token": s.Delivery.Token}); err != nil {
		return fmt.Errorf("authenticate Claude inbox: %w", err)
	}
	frame := map[string]any{
		"msgV":       1,
		"msg_id":     messageUUID(msgs),
		"type":       "user",
		"message":    map[string]string{"role": "user", "content": IncomingText(msgs)},
		"priority":   "next",
		"session_id": strings.TrimPrefix(s.Delivery.NativeID, "sid:"),
	}
	if err := enc.Encode(frame); err != nil {
		return fmt.Errorf("write Claude inbox: %w", err)
	}
	if unix, ok := conn.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	return nil
}

func messageUUID(msgs []bus.Msg) string {
	var key strings.Builder
	for _, m := range msgs {
		key.WriteString(m.ID)
		key.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(key.String()))
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}
