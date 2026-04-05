package echoweb

import (
	"github.com/goforj/web"
	"github.com/gorilla/websocket"
)

type websocketConnAdapter struct {
	conn *websocket.Conn
}

var _ web.WebSocketConn = (*websocketConnAdapter)(nil)

func newWebSocketConn(conn *websocket.Conn) *websocketConnAdapter {
	return &websocketConnAdapter{conn: conn}
}

func (c *websocketConnAdapter) ReadJSON(target any) error {
	return c.conn.ReadJSON(target)
}

func (c *websocketConnAdapter) WriteJSON(payload any) error {
	return c.conn.WriteJSON(payload)
}

func (c *websocketConnAdapter) Close() error {
	return c.conn.Close()
}

func (c *websocketConnAdapter) Native() any {
	return c.conn
}

// UnwrapWebSocketConn returns the underlying gorilla websocket connection.
func UnwrapWebSocketConn(conn web.WebSocketConn) (*websocket.Conn, bool) {
	adapted, ok := conn.(*websocketConnAdapter)
	if !ok || adapted == nil || adapted.conn == nil {
		return nil, false
	}
	return adapted.conn, true
}
