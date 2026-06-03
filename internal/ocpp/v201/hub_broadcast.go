package v201

import wsapi "github.com/chargeghost/engine/internal/api/ws"

func (b *Bridge201) broadcastWS(msg wsapi.Message) {
	if b.hub != nil {
		b.hub.BroadcastMessage(msg)
	}
}
