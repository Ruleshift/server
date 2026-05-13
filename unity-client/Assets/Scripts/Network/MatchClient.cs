using System;
using System.Threading;
using System.Threading.Tasks;

namespace Ruleshift.Network
{
    public sealed class MatchClient
    {
        private readonly ProtocolCodec _codec;
        private ulong _lastSeenRevision;

        public MatchClient(ProtocolCodec codec)
        {
            _codec = codec ?? throw new ArgumentNullException(nameof(codec));
        }

        public ulong LastSeenRevision => _lastSeenRevision;

        public Task ConnectAsync(Uri gatewayUri, CancellationToken cancellationToken)
        {
            if (gatewayUri == null) throw new ArgumentNullException(nameof(gatewayUri));
            return Task.CompletedTask;
        }

        public Task SendAuthRequestAsync(string ticket, CancellationToken cancellationToken)
        {
            if (string.IsNullOrWhiteSpace(ticket)) throw new ArgumentException("Ticket must not be empty.", nameof(ticket));
            return Task.CompletedTask;
        }

        public Task JoinRoomAsync(string roomId, CancellationToken cancellationToken)
        {
            if (string.IsNullOrWhiteSpace(roomId)) throw new ArgumentException("Room id must not be empty.", nameof(roomId));
            return Task.CompletedTask;
        }

        public Task SendAddAsync(string roomId, long value, CancellationToken cancellationToken)
        {
            if (string.IsNullOrWhiteSpace(roomId)) throw new ArgumentException("Room id must not be empty.", nameof(roomId));
            return Task.CompletedTask;
        }

        public Task SendSetAsync(string roomId, long value, CancellationToken cancellationToken)
        {
            if (string.IsNullOrWhiteSpace(roomId)) throw new ArgumentException("Room id must not be empty.", nameof(roomId));
            return Task.CompletedTask;
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
    }
}


