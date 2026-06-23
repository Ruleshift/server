# Ruleshift.Developer

Build a NuGet package with:

```powershell
dotnet pack -c Release
```

The package exposes the same `RuleshiftDeveloperClient` as the Unity UPM package. Keep its bearer key in editor, CI, or trusted backend configuration; never include it in a player build.
