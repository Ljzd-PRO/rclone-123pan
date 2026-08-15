package _123pan

import (
	"errors"
	"fmt"
	"strings"

	"github.com/rclone/rclone/fs/fserrors"
)

// AuthenticationChallengeError reports a server-side account challenge which
// must be completed on the official 123Pan website. Runtime filesystem
// operations never prompt for an SMS code or other interactive input.
type AuthenticationChallengeError struct {
	Cause *APIError
}

// Error returns an actionable message without exposing response secrets.
func (e *AuthenticationChallengeError) Error() string {
	message := "123 网盘要求先在官方网页完成安全验证（短信验证码或微信登录）"
	if e != nil && e.Cause != nil {
		if serverMessage := strings.TrimSpace(scrubSecrets(e.Cause.Message)); serverMessage != "" {
			message += "；服务端信息：" + serverMessage
		}
	}
	return message + "。请访问 https://www.123pan.com/ 完成验证，然后运行 rclone config reconnect <remote>:；后台传输不会等待验证码输入"
}

// Unwrap preserves the typed API error, including its business code and
// request ID, for callers which need structured diagnostics.
func (e *AuthenticationChallengeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

var authenticationChallengeMarkers = []string{
	"短信验证码",
	"短信验证",
	"微信进行登录",
	"微信登录",
	"安全风险",
	"账号风险",
	"帐号风险",
	"设备风险",
	"陌生设备",
	"验证码",
	"verification code",
	"sms verification",
	"wechat login",
	"security risk",
}

func isAuthenticationChallengeMessage(message string) bool {
	message = strings.ToLower(message)
	for _, marker := range authenticationChallengeMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func markAuthenticationChallenge(err error) error {
	if err == nil {
		return nil
	}
	var challenge *AuthenticationChallengeError
	if errors.As(err, &challenge) {
		return err
	}
	var apiErr *APIError
	if !errorsAsAPI(err, &apiErr) || !isAuthenticationChallengeMessage(apiErr.Message) {
		return err
	}
	return fserrors.FatalError(&AuthenticationChallengeError{Cause: apiErr})
}

func isAuthenticationChallengeError(err error) bool {
	var challenge *AuthenticationChallengeError
	return errors.As(err, &challenge)
}

func reconnectChallengeError(name string, err error) error {
	if !isAuthenticationChallengeError(err) {
		return err
	}
	return fmt.Errorf("%w；完成官方验证后请重新运行 rclone config reconnect %q", err, name+":")
}
