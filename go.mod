module github.com/Ruleshift/server

go 1.26

require (
	github.com/gorilla/websocket v1.5.3
	github.com/hmgle/godogpaw v0.0.0-20260531114907-5ce8e53519aa
	github.com/jackc/pgx/v5 v5.7.5
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.37.0 // indirect
	golang.org/x/sync v0.13.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)

replace github.com/hmgle/godogpaw => github.com/laines-it/xiangqi-go v0.0.0-20260531114907-5ce8e53519aa
