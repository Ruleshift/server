namespace Ruleshift.Network
{
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


