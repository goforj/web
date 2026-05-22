package echoweb

import (
	"github.com/goforj/web"
	"github.com/gorilla/websocket"
)

type websocketConnAdapter struct {
	conn *websocket.Conn
}

var _ web.WebSocketConn = (*websocketConnAdapter)(nil)

// newWebSocketConn wraps a Gorilla websocket connection with the web abstraction.
func newWebSocketConn(conn *websocket.Conn) *websocketConnAdapter {
	return &websocketConnAdapter{conn: conn}
}

// ReadJSON reads one JSON message into target.
func (c *websocketConnAdapter) ReadJSON(target any) error {
	return c.conn.ReadJSON(target)
}

// WriteJSON writes one JSON message.
func (c *websocketConnAdapter) WriteJSON(payload any) error {
	return c.conn.WriteJSON(payload)
}

// Close closes the websocket connection.
func (c *websocketConnAdapter) Close() error {
	return c.conn.Close()
}

// Native returns the underlying Gorilla websocket connection.
func (c *websocketConnAdapter) Native() any {
	return c.conn
}

// UnwrapWebSocketConn returns the underlying gorilla websocket connection.
// @group Adapter
// Example:
// _, ok := echoweb.UnwrapWebSocketConn(nil)
// fmt.Println(ok)
//	// false
func UnwrapWebSocketConn(conn web.WebSocketConn) (*websocket.Conn, bool) {
	adapted, ok := conn.(*websocketConnAdapter)
	if !ok || adapted == nil || adapted.conn == nil {
		return nil, false
	}
	return adapted.conn, true
}
