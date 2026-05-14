using System;
using Google.Protobuf;
using Ruleshift.Protocol.V1;

namespace Ruleshift.Network
{
    public static class ProtocolCodec
    {
        public const uint ProtocolVersion = 1;
        public const int DefaultMaxMessageBytes = 65536;
        public const int LengthPrefixBytes = 4;

        // NativeWebSocket preserves message boundaries, so the Go gateway expects this raw protobuf payload.
        public static byte[] SerializeClientEnvelope(ClientEnvelope envelope, ulong clientSequence, int maxMessageBytes = DefaultMaxMessageBytes)
        {
            if (envelope == null) throw new ArgumentNullException(nameof(envelope));
            if (clientSequence == 0) throw new ArgumentOutOfRangeException(nameof(clientSequence), "Client sequence must be positive.");
            if (maxMessageBytes <= 0) throw new ArgumentOutOfRangeException(nameof(maxMessageBytes));

            envelope.ProtocolVersion = ProtocolVersion;
            envelope.ClientSequence = clientSequence;

            var payload = envelope.ToByteArray();
            if (payload.Length > maxMessageBytes)
            {
                throw new InvalidOperationException("Client envelope exceeds max message size.");
            }

            return payload;
        }

        public static ServerEnvelope DeserializeServerEnvelope(byte[] payload, int maxMessageBytes = DefaultMaxMessageBytes)
        {
            if (payload == null) throw new ArgumentNullException(nameof(payload));
            if (maxMessageBytes <= 0) throw new ArgumentOutOfRangeException(nameof(maxMessageBytes));
            if (payload.Length > maxMessageBytes)
            {
                throw new InvalidOperationException("Server envelope exceeds max message size.");
            }

            return ServerEnvelope.Parser.ParseFrom(payload);
        }

        // Use this only for transports that do not preserve message boundaries. WebSocket does not need it.
        public static byte[] SerializeLengthPrefixedClientEnvelope(ClientEnvelope envelope, ulong clientSequence, int maxMessageBytes = DefaultMaxMessageBytes)
        {
            return AddLengthPrefix(SerializeClientEnvelope(envelope, clientSequence, maxMessageBytes));
        }

        public static byte[] AddLengthPrefix(byte[] payload)
        {
            if (payload == null) throw new ArgumentNullException(nameof(payload));

            var frame = new byte[LengthPrefixBytes + payload.Length];
            frame[0] = (byte)((payload.Length >> 24) & 0xff);
            frame[1] = (byte)((payload.Length >> 16) & 0xff);
            frame[2] = (byte)((payload.Length >> 8) & 0xff);
            frame[3] = (byte)(payload.Length & 0xff);
            Buffer.BlockCopy(payload, 0, frame, LengthPrefixBytes, payload.Length);
            return frame;
        }

        public static bool TryReadLengthPrefixedFrame(
            byte[] buffer,
            int offset,
            int count,
            out byte[] payload,
            out int bytesRead,
            int maxMessageBytes = DefaultMaxMessageBytes)
        {
            payload = null;
            bytesRead = 0;

            if (buffer == null) throw new ArgumentNullException(nameof(buffer));
            if (maxMessageBytes <= 0) throw new ArgumentOutOfRangeException(nameof(maxMessageBytes));
            if (offset < 0 || count < 0 || offset + count > buffer.Length)
            {
                throw new ArgumentOutOfRangeException(nameof(count));
            }
            if (count < LengthPrefixBytes)
            {
                return false;
            }

            var length =
                (buffer[offset] << 24) |
                (buffer[offset + 1] << 16) |
                (buffer[offset + 2] << 8) |
                buffer[offset + 3];

            if (length < 0)
            {
                throw new InvalidOperationException("Negative frame length.");
            }
            if (length > maxMessageBytes)
            {
                throw new InvalidOperationException("Length-prefixed frame exceeds max message size.");
            }
            if (count - LengthPrefixBytes < length)
            {
                return false;
            }

            payload = new byte[length];
            Buffer.BlockCopy(buffer, offset + LengthPrefixBytes, payload, 0, length);
            bytesRead = LengthPrefixBytes + length;
            return true;
        }

        public static bool TryDeserializeLengthPrefixedServerEnvelope(
            byte[] buffer,
            int offset,
            int count,
            out ServerEnvelope envelope,
            out int bytesRead,
            int maxMessageBytes = DefaultMaxMessageBytes)
        {
            envelope = null;
            if (!TryReadLengthPrefixedFrame(buffer, offset, count, out var payload, out bytesRead, maxMessageBytes))
            {
                return false;
            }

            envelope = DeserializeServerEnvelope(payload, maxMessageBytes);
            return true;
        }
    }
}
