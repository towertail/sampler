package version

var (
	Version = "0.0.0"
	SHA     = "dev"
)

func String() string {
	return Version + "+" + SHA
}
