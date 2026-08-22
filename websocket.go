package webpprof

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	webSocketWriteTimeout  = 10 * time.Second
	webSocketPongTimeout   = 60 * time.Second
	webSocketPingInterval  = 25 * time.Second
	webSocketStatsInterval = 2 * time.Second
)

func (p *Profiler) serveWebSocket(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: p.checkOrigin, HandshakeTimeout: 5 * time.Second}
	connection, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	updates, unsubscribe := p.store.subscribe()
	defer unsubscribe()
	connection.SetReadLimit(4 << 10)
	_ = connection.SetReadDeadline(time.Now().Add(webSocketPongTimeout))
	connection.SetPongHandler(func(string) error { return connection.SetReadDeadline(time.Now().Add(webSocketPongTimeout)) })
	readDone := make(chan struct{}, 1)
	go func() {
		defer func() { readDone <- struct{}{} }()
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}()
	if err := writeWebSocketJSON(connection, p.streamSnapshot(r, "connected")); err != nil {
		return
	}
	pingTicker := time.NewTicker(webSocketPingInterval)
	defer pingTicker.Stop()
	statsTicker := time.NewTicker(webSocketStatsInterval)
	defer statsTicker.Stop()
	for {
		select {
		case message, ok := <-updates:
			if !ok || writeWebSocketJSON(connection, message) != nil {
				return
			}
		case <-statsTicker.C:
			if err := writeWebSocketJSON(connection, p.streamSnapshot(r, "stats.updated")); err != nil {
				return
			}
		case <-pingTicker.C:
			if err := connection.SetWriteDeadline(time.Now().Add(webSocketWriteTimeout)); err != nil {
				return
			}
			if err := connection.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-readDone:
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (p *Profiler) streamSnapshot(r *http.Request, messageType string) streamMessage {
	runtimeStats := p.RuntimeStats()
	queueStats := p.QueueStats(r.Context())
	return streamMessage{
		Type:    messageType,
		Cursor:  p.store.stats().Cursor,
		Runtime: &runtimeStats,
		Queues:  &queueStats,
	}
}

func writeWebSocketJSON(connection *websocket.Conn, value any) error {
	if err := connection.SetWriteDeadline(time.Now().Add(webSocketWriteTimeout)); err != nil {
		return err
	}
	return connection.WriteJSON(value)
}
