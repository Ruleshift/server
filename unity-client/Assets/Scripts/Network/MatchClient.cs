using System;
using System.Threading;
using System.Threading.Tasks;
using Google.Protobuf;
using NativeWebSocket;
using Ruleshift.Protocol.V1;

namespace Ruleshift.Network
{
    public sealed class MatchClient
    {
        public const uint ProtocolVersion = 1;

        private readonly int _maxMessageBytes;
        private WebSocket _socket;
        private ulong _clientSequence;
        private ulong _lastSeenRevision;

        public MatchClient(int maxMessageBytes = 65536)
        {
            if (maxMessageBytes <= 0) throw new ArgumentOutOfRangeException(nameof(maxMessageBytes));
            _maxMessageBytes = maxMessageBytes;
        }

        public event Action Connected;
        public event Action<WebSocketCloseCode> Closed;
        public event Action<string> TransportError;
        public event Action<ServerEnvelope> ServerEnvelopeReceived;
        public event Action<AuthOk> Authenticated;
        public event Action<AuthFailed> AuthenticationFailed;
        public event Action<JoinRoomOk> JoinedRoom;
        public event Action<StateSnapshot> SnapshotReceived;
        public event Action<StateDelta> DeltaReceived;
        public event Action<ErrorMessage> ServerError;
        public event Action<Pong> PongReceived;

        public ulong LastSeenRevision => _lastSeenRevision;

        public WebSocketState State => _socket?.State ?? WebSocketState.Closed;

        public async Task ConnectAsync(Uri gatewayUri, CancellationToken cancellationToken)
        {
            if (gatewayUri == null) throw new ArgumentNullException(nameof(gatewayUri));
            cancellationToken.ThrowIfCancellationRequested();

            if (_socket != null && _socket.State != WebSocketState.Closed)
            {
                await CloseAsync(cancellationToken);
            }

            _socket = new WebSocket(gatewayUri.ToString());
            _socket.OnOpen += () => Connected?.Invoke();
            _socket.OnClose += code => Closed?.Invoke(code);
            _socket.OnError += error => TransportError?.Invoke(error);
            _socket.OnMessage += HandleMessage;

            await WaitForCancellation(_socket.Connect(), cancellationToken);
        }

        public Task SendAuthRequestAsync(string ticket, CancellationToken cancellationToken)
        {
            if (string.IsNullOrWhiteSpace(ticket)) throw new ArgumentException("Ticket must not be empty.", nameof(ticket));

            return SendAsync(new ClientEnvelope
            {
                AuthRequest = new AuthRequest { Ticket = ticket },
            }, cancellationToken);
        }

        public Task JoinRoomAsync(string roomId, CancellationToken cancellationToken)
        {
            if (string.IsNullOrWhiteSpace(roomId)) throw new ArgumentException("Room id must not be empty.", nameof(roomId));

            return SendAsync(new ClientEnvelope
            {
                JoinRoom = new JoinRoomRequest
                {
                    RoomId = roomId,
                    LastSeenRevision = _lastSeenRevision,
                },
            }, cancellationToken);
        }

        public Task SendAddAsync(string roomId, long value, CancellationToken cancellationToken)
        {
            return SendCommandAsync(roomId, IntOperation.Add, value, cancellationToken);
        }

        public Task SendSetAsync(string roomId, long value, CancellationToken cancellationToken)
        {
            return SendCommandAsync(roomId, IntOperation.Set, value, cancellationToken);
        }

        public Task RequestSnapshotAsync(string roomId, CancellationToken cancellationToken)
        {
            if (string.IsNullOrWhiteSpace(roomId)) throw new ArgumentException("Room id must not be empty.", nameof(roomId));

            return SendAsync(new ClientEnvelope
            {
                SnapshotRequest = new SnapshotRequest
                {
                    RoomId = roomId,
                    LastSeenRevision = _lastSeenRevision,
                },
            }, cancellationToken);
        }

        public Task SendPingAsync(long clientTimeUnixMs, CancellationToken cancellationToken)
        {
            return SendAsync(new ClientEnvelope
            {
                Ping = new Ping { ClientTimeUnixMs = clientTimeUnixMs },
            }, cancellationToken);
        }

        public void DispatchMessageQueue()
        {
#if !UNITY_WEBGL || UNITY_EDITOR
            _socket?.DispatchMessageQueue();
#endif
        }

        public async Task CloseAsync(CancellationToken cancellationToken)
        {
            if (_socket == null || _socket.State == WebSocketState.Closed)
            {
                return;
            }

            await WaitForCancellation(_socket.Close(), cancellationToken);
        }

        public void ApplySnapshot(string roomId, long value, ulong revision)
        {
            _lastSeenRevision = revision;
        }

        public void ApplyDelta(string roomId, long newValue, ulong newRevision)
        {
            if (newRevision > _lastSeenRevision)
            {
                _lastSeenRevision = newRevision;
            }
        }

        private Task SendCommandAsync(string roomId, IntOperation operation, long value, CancellationToken cancellationToken)
        {
            if (string.IsNullOrWhiteSpace(roomId)) throw new ArgumentException("Room id must not be empty.", nameof(roomId));

            return SendAsync(new ClientEnvelope
            {
                IntCommand = new IntCommand
                {
                    RoomId = roomId,
                    Operation = operation,
                    Value = value,
                },
            }, cancellationToken);
        }

        private async Task SendAsync(ClientEnvelope envelope, CancellationToken cancellationToken)
        {
            if (_socket == null || _socket.State != WebSocketState.Open)
            {
                throw new InvalidOperationException("WebSocket is not connected.");
            }

            cancellationToken.ThrowIfCancellationRequested();
            envelope.ProtocolVersion = ProtocolVersion;
            envelope.ClientSequence = ++_clientSequence;

            var payload = envelope.ToByteArray();
            if (payload.Length > _maxMessageBytes) throw new InvalidOperationException("Client envelope exceeds max message size.");

            await WaitForCancellation(_socket.Send(payload), cancellationToken);
        }

        private void HandleMessage(byte[] payload)
        {
            if (payload.Length > _maxMessageBytes)
            {
                TransportError?.Invoke("Server envelope exceeds max message size.");
                return;
            }

            ServerEnvelope envelope;
            try
            {
                envelope = ServerEnvelope.Parser.ParseFrom(payload);
            }
            catch (Exception ex)
            {
                TransportError?.Invoke("Failed to decode server envelope: " + ex.Message);
                return;
            }

            ServerEnvelopeReceived?.Invoke(envelope);

            switch (envelope.PayloadCase)
            {
                case ServerEnvelope.PayloadOneofCase.AuthOk:
                    Authenticated?.Invoke(envelope.AuthOk);
                    break;
                case ServerEnvelope.PayloadOneofCase.AuthFailed:
                    AuthenticationFailed?.Invoke(envelope.AuthFailed);
                    break;
                case ServerEnvelope.PayloadOneofCase.JoinRoomOk:
                    JoinedRoom?.Invoke(envelope.JoinRoomOk);
                    break;
                case ServerEnvelope.PayloadOneofCase.StateSnapshot:
                    ApplySnapshot(envelope.StateSnapshot.RoomId, envelope.StateSnapshot.Value, envelope.StateSnapshot.Revision);
                    SnapshotReceived?.Invoke(envelope.StateSnapshot);
                    break;
                case ServerEnvelope.PayloadOneofCase.StateDelta:
                    ApplyDelta(envelope.StateDelta.RoomId, envelope.StateDelta.NewValue, envelope.StateDelta.NewRevision);
                    DeltaReceived?.Invoke(envelope.StateDelta);
                    break;
                case ServerEnvelope.PayloadOneofCase.Error:
                    ServerError?.Invoke(envelope.Error);
                    break;
                case ServerEnvelope.PayloadOneofCase.Pong:
                    PongReceived?.Invoke(envelope.Pong);
                    break;
            }
        }

        private static async Task WaitForCancellation(Task task, CancellationToken cancellationToken)
        {
            if (!cancellationToken.CanBeCanceled)
            {
                await task;
                return;
            }

            var canceled = new TaskCompletionSource<bool>();
            using (cancellationToken.Register(() => canceled.TrySetResult(true)))
            {
                if (task != await Task.WhenAny(task, canceled.Task))
                {
                    throw new OperationCanceledException(cancellationToken);
                }
            }

            await task;
        }
    }
}
