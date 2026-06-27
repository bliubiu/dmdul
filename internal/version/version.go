package version

var (
	Name    = "dmdul"
	Version = "v0.1.2"
	Commit  = "unknown"
)

func String() string {
	return Name + " " + Version + " (" + Commit + ")"
}
