# Ruleshift.Developer

Build a NuGet package with:

```powershell
dotnet pack -c Release
```

The package exposes the same `RuleshiftDeveloperClient` as the Unity UPM
package, including `CreateRuntimeModuleAsync`, `PublishModuleVersionAsync`,
validation status, room creation, and bounded row APIs. Keep its bearer key in
Editor, CI, or trusted backend configuration; never include it in a player build.
