module github.com/onlinealarmkur/timer-cli

go 1.26.0

require (
	github.com/creack/pty v1.1.24
	github.com/mattn/go-runewidth v0.0.28
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
)

require (
	github.com/BurntSushi/toml v1.6.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/google/renameio v1.0.1 // indirect
	golang.org/x/exp/typeparams v0.0.0-20260820142414-ca536658362e // indirect
	golang.org/x/mod v0.40.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/telemetry v0.0.0-20260820143203-7221e139e8d6 // indirect
	golang.org/x/tools v0.49.0 // indirect
	golang.org/x/vuln v1.7.0 // indirect
	honnef.co/go/tools v0.8.1 // indirect
)

tool (
	golang.org/x/vuln/cmd/govulncheck
	honnef.co/go/tools/cmd/staticcheck
)
