# Ruleshift Developer SDK for Unity

This is an Editor-only Unity Package Manager package for Developer API v2. It
publishes immutable OCI module versions, checks validation, creates pinned rooms,
and uses the safe data API without exposing PostgreSQL.

Install it from a local checkout with Unity Package Manager using:

```text
file:../server/sdk/unity/com.ruleshift.developer
```

Example editor code:

```csharp
using Ruleshift.Developer;

using var ruleshift = new RuleshiftDeveloperClient(
    "http://localhost:8080",
    Environment.GetEnvironmentVariable("RULESHIFT_DEVELOPER_API_KEY"));

var module = await ruleshift.CreateRuntimeModuleAsync("my_game", "My Game");
var version = await ruleshift.PublishModuleVersionAsync(publishRequest);
var room = await ruleshift.CreateRoomAsync(module.Key);
```

Keep the developer API key in an environment variable or secret store. Because the assembly is Editor-only, it is excluded from player builds.
