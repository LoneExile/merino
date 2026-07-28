package app

import "os"

// Env returns MERINO_<key> if set, else HERDR_TUNNEL_<key> for one release of
// backward compatibility after the Merino rebrand.
func Env(key string) string {
	if v := os.Getenv("MERINO_" + key); v != "" {
		return v
	}
	return os.Getenv("HERDR_TUNNEL_" + key)
}
