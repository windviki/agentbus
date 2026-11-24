// Package delivery renders incoming mail and pushes batches into harness-native
// inboxes. Acceptance means the harness acknowledged its local transport, not
// that the model has read or acted on the message.
package delivery

import (
	"context"
	"fmt"

	"github.com/windviki/agentbus/internal/bus"
)

func CanPush(s bus.Session) bool {
	if s.Delivery == nil {
		return false
	}
	switch s.Delivery.Driver {
	case "claude":
		return s.Delivery.Address != "" && s.Delivery.NativeID != "" && s.Delivery.Token != ""
	case "kimi":
		return s.Delivery.NativeID != ""
	default:
		return false
	}
}

func Push(ctx context.Context, s bus.Session, msgs []bus.Msg) error {
	if len(msgs) == 0 || s.Delivery == nil {
		return nil
	}
	switch s.Delivery.Driver {
	case "claude":
		return pushClaude(ctx, s, msgs)
	case "kimi":
		return pushKimi(ctx, s, msgs)
	default:
		return fmt.Errorf("unsupported native delivery driver %q", s.Delivery.Driver)
	}
}
