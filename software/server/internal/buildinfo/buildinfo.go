package buildinfo

import "fmt"

var (
	Version      = "development"
	SourceCommit = "unknown"
	BuildDate    = "unknown"
)

func String(component string) string {
	return fmt.Sprintf("%s %s (commit %s, built %s)", component, Version, SourceCommit, BuildDate)
}
