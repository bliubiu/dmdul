package version

var (
	Name    = "dmdul"
	Version = "0.1.0-dev"
	Commit  = "unknown"
)

func String() string {
	return Name + " " + Version + " (" + Commit + ")"
}
