package version

var (
	Name      = "dmdul"
	Version   = "v0.1.2"
	Commit    = "unknown"
	BuildTime = "unknown"
)

func String() string {
	if BuildTime != "" && BuildTime != "unknown" {
		return Name + " " + Version + " (" + Commit + ", built " + BuildTime + ")"
	}
	return Name + " " + Version + " (" + Commit + ")"
}
