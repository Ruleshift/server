# Ruleshift Developer SDK for Unity

This is an Editor-only Unity Package Manager package for the Ruleshift developer API. It provisions game-module databases and inspects their safe data API without exposing PostgreSQL to the developer's game.

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

var module = await ruleshift.CreateModuleAsync(new CreateModuleRequest
{
    Key = "my_game",
    DisplayName = "My Game",
    Schema = new ModuleSchema
    {
        Tables =
        {
            new TableDefinition
            {
                Name = "profiles",
                Columns =
                {
                    new ColumnDefinition { Name = "player_id", Type = RuleshiftColumnType.String, PrimaryKey = true },
                    new ColumnDefinition { Name = "rating", Type = RuleshiftColumnType.Int64 }
                }
            }
        }
    }
});
```

Keep the developer API key in an environment variable or secret store. Because the assembly is Editor-only, it is excluded from player builds.
