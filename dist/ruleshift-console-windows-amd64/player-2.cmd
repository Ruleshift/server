@echo off
setlocal
cd /d "%~dp0"
ruleshift-console.exe -addr ws://147.45.211.122:8080/ws -ticket mock:player-2 -room demo
