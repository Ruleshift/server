package netx

const WebSocketPath = "/ws"

type WebSocketGatewayConfig struct {
	Path            string
	MaxMessageBytes int
}

func DefaultWebSocketGatewayConfig(maxMessageBytes int) WebSocketGatewayConfig {
	return WebSocketGatewayConfig{
		Path:            WebSocketPath,
		MaxMessageBytes: maxMessageBytes,
	}
}
