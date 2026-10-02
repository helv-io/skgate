package app

import (
	"os"
	"testing"

	"github.com/helv-io/skgate/internal/managed/fakemcp"
)

// The test binary doubles as the fake stdio MCP server used by managed upstream tests.
func TestMain(m *testing.M) {
	if fakemcp.IsChild() {
		fakemcp.Run(os.Stdin, os.Stdout)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
