module github.com/farazhassan/gantry/examples/assistant

go 1.25.0

require (
	github.com/farazhassan/gantry v0.0.0-00010101000000-000000000000
	github.com/farazhassan/gantry/mcp v0.0.0-00010101000000-000000000000
	github.com/modelcontextprotocol/go-sdk v1.6.1
)

replace github.com/farazhassan/gantry => ../../

replace github.com/farazhassan/gantry/mcp => ../../gantry/mcp
