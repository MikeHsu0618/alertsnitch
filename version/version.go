package version

import "fmt"

// Version is the application Version
var Version string

// Date is the built date and time
var Date string

// Commit is the commit in which the package is based
var Commit string

// GetVersion returns the version as a string
func GetVersion() string {
	return fmt.Sprintf("alertsnitch Version: %s, Commit: %s, Date: %s", Version, Commit, Date)
}
