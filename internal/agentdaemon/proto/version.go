package proto

// Protocol version. Bump on any existing-payload-shape change (additive
// optional fields don't need a bump). Daemon sends this in the WS
// upgrade query (`version=<semver>`) and in the bootstrap HTTP
// response; mismatches fail closed at WS upgrade.
const (
	Version = "0.1.0"
)

// VersionCompatible returns true when clientVersion's "X.Y" prefix
// exactly matches Version's. Inputs without a patch segment (e.g.
// "0.1", "1") are rejected so a half-set version string never
// spuriously matches.
func VersionCompatible(clientVersion string) bool {
	if clientVersion == "" {
		return false
	}
	clientMM, ok := majorMinor(clientVersion)
	if !ok {
		return false
	}
	serverMM, _ := majorMinor(Version)
	return clientMM == serverMM
}

// majorMinor returns the "X.Y" prefix of a semver-shaped version.
// Second return is false when the input has no patch segment.
func majorMinor(v string) (string, bool) {
	dots := 0
	for i := 0; i < len(v); i++ {
		if v[i] == '.' {
			dots++
			if dots == 2 {
				return v[:i], true
			}
		}
	}
	return "", false
}
