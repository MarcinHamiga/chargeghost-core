package v16

import wsapi "github.com/chargeghost/engine/internal/api/ws"

func (b *Bridge16) broadcastWS(msg wsapi.Message) {
	if b.hub != nil {
		b.hub.BroadcastMessage(msg)
	}
}
