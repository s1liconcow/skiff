package buildinfo

type Info struct {
	Binary    string `json:"binary"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func Current(binary string) Info {
	return Info{
		Binary:    binary,
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
	}
}
