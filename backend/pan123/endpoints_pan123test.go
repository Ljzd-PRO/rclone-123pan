//go:build pan123test

package pan123

import "os"

var (
	productionLoginRoot = endpointFromEnvironment("RCLONE_123_TEST_LOGIN_ROOT", "https://login.123pan.com")
	productionAPIRoot   = endpointFromEnvironment("RCLONE_123_TEST_API_ROOT", "https://yun.123pan.com/b/api")
)

func endpointFromEnvironment(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
