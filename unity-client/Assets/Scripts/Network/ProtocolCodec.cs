using System;
using System.Buffers.Binary;

namespace Ruleshift.Network
{
    public sealed class ProtocolCodec
    {
        private readonly int _maxMessageBytes;

        public ProtocolCodec(int maxMessageBytes = 65536)
        {
            if (maxMessageBytes <= 0) throw new ArgumentOutOfRangeException(nameof(maxMessageBytes));
            _maxMessageBytes = maxMessageBytes;
        }

        public byte[] EncodeFrame(byte[] protobufPayload)
        {
            if (protobufPayload == null) throw new ArgumentNullException(nameof(protobufPayload));
            if (protobufPayload.Length == 0) throw new ArgumentException("Payload must not be empty.", nameof(protobufPayload));
            if (protobufPayload.Length > _maxMessageBytes) throw new ArgumentException("Payload exceeds max message size.", nameof(protobufPayload));

            var frame = new byte[4 + protobufPayload.Length];
            BinaryPrimitives.WriteUInt32BigEndian(frame.AsSpan(0, 4), (uint)protobufPayload.Length);
            Buffer.BlockCopy(protobufPayload, 0, frame, 4, protobufPayload.Length);
            return frame;
        }

        public byte[] DecodeFrame(byte[] frame)
        {
            if (frame == null) throw new ArgumentNullException(nameof(frame));
            if (frame.Length < 4) throw new ArgumentException("Frame is missing length prefix.", nameof(frame));

            var size = BinaryPrimitives.ReadUInt32BigEndian(frame.AsSpan(0, 4));
            if (size == 0) throw new ArgumentException("Frame payload must not be empty.", nameof(frame));
            if (size > _maxMessageBytes) throw new ArgumentException("Frame exceeds max message size.", nameof(frame));
            if ((uint)(frame.Length - 4) != size) throw new ArgumentException("Frame length does not match prefix.", nameof(frame));

            var payload = new byte[(int)size];
            Buffer.BlockCopy(frame, 4, payload, 0, (int)size);
            return payload;
        }
    }
}


