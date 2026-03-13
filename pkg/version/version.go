package version

import "fmt"

var (
	Current = "0.1.0"
	Commit  = "dev"
	Date    = "unknown"
)

func Summary() string {
	if Commit == "" || Commit == "dev" {
		return Current
	}

	shortCommit := Commit
	if len(shortCommit) > 7 {
		shortCommit = shortCommit[:7]
	}

	if Date == "" || Date == "unknown" {
		return fmt.Sprintf("%s (%s)", Current, shortCommit)
	}

	return fmt.Sprintf("%s (%s, %s)", Current, shortCommit, Date)
}
