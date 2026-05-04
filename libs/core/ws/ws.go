package ws

// WsService is a no-op stub for WebSocket broadcasting.
// Full WebSocket support requires gorilla/websocket wired into the host application.
// In the NTX Go stack, real-time events are emitted by the identity/notifications app;
// other services use this stub and publish domain events via the EventBus instead.
type WsService struct{}

// Emit is a no-op stub. Real implementations would broadcast to a socket.io room.
func (s *WsService) Emit(namespace, room, event string, payload any) error {
	return nil
}

// Bind is a no-op stub. Real implementations would start a WebSocket server on addr.
func (s *WsService) Bind(addr string) error {
	return nil
}
