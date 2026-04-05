package web

// WebSocketConn is the app-facing websocket connection contract.
type WebSocketConn interface {
	ReadJSON(target any) error
	WriteJSON(payload any) error
	Close() error
	Native() any
}

// WebSocketHandler handles an upgraded websocket connection.
type WebSocketHandler func(Context, WebSocketConn) error
