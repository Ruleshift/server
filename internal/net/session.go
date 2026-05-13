package netx

type SessionConfig struct {
	SendQueueSize  int
	MaxMessageSize int
}

type SessionID string

type CloseReason string

const (
	CloseReasonAuthFailed   CloseReason = "auth_failed"
	CloseReasonSlowConsumer CloseReason = "slow_consumer"
	CloseReasonShutdown     CloseReason = "shutdown"
)
