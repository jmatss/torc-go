package cons

// TODO: move variables elsewhere
var (
	PeerId       string
	DownloadPath string
	Logging      = Low
)

type LogLevel int

const (
	High LogLevel = iota
	Low
	None
)
