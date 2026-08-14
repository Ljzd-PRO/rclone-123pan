//go:build 123pantest

package _123pan

import "os"

var (
	productionLoginRoot = endpointFromEnvironment("RCLONE_123_TEST_LOGIN_ROOT", "https://login.123pan.com")
	productionAPIRoot   = endpointFromEnvironment("RCLONE_123_TEST_API_ROOT", "https://api.123278.com/b/api")
)

func endpointFromEnvironment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
