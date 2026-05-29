package version

var (
	// Version is the version number. Filled in at linking time (via Makefile).
	Version = "<unknown>"

	// Revision is the VCS (e.g. git) revision. Filled in at linking time (via Makefile).
	Revision = "<unknown>"
)
