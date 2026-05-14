namespace Ruleshift.Network
{
    // Local-only provider for the Go mock auth backend.
    // Production code should replace this with Steamworks.NET, Facepunch.Steamworks,
    // or another Steamworks bridge that returns a Steam session ticket for AuthRequest.
    public sealed class MockAuthProvider
    {
        private readonly string _playerId;

        public MockAuthProvider(string playerId)
        {
            _playerId = string.IsNullOrWhiteSpace(playerId) ? "player-1" : playerId;
        }

        public string GetTicket()
        {
            return "mock:" + _playerId;
        }
    }
}


