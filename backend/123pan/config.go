package _123pan

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ljzd/rclone-123pan/backend/123pan/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/config/obscure"
	"github.com/rclone/rclone/fs/fshttp"
)

// Config validates a new configuration and implements `rclone config
// reconnect`. It deliberately performs no interactive challenge handling: the
// user completes any SMS or WeChat verification on the official website, then
// reruns reconnect to obtain and validate a fresh token.
func Config(ctx context.Context, name string, m configmap.Mapper, in fs.ConfigIn) (*fs.ConfigOut, error) {
	if in.State != "" {
		return nil, fmt.Errorf("unsupported 123Pan config state %q", in.State)
	}
	err := reconnectAuthentication(ctx, m, productionLoginRoot, productionAPIRoot, fshttp.NewClient(ctx))
	if err != nil {
		return nil, fmt.Errorf("reconnect 123Pan remote %q: %w", name, reconnectChallengeError(name, err))
	}
	return nil, nil
}

// reconnectAuthentication is transactional with respect to the persisted
// token. Login and account validation use a staging config; m is updated only
// after the fresh token has successfully read a valid user identity.
func reconnectAuthentication(ctx context.Context, m configmap.Mapper, loginRoot, apiRoot string, httpClient *http.Client) error {
	var opt Options
	if err := configstruct.Set(m, &opt); err != nil {
		return fmt.Errorf("parse 123Pan configuration: %w", err)
	}
	if opt.User == "" {
		return errorsNewConfig("user is required")
	}
	if opt.Pass == "" {
		return errorsNewConfig("pass is required")
	}
	if time.Duration(opt.APIMinInterval) < 0 {
		return errorsNewConfig("api_min_interval must not be negative")
	}
	if opt.Platform == "" {
		opt.Platform = protocolPlatform
	}
	password, err := obscure.Reveal(opt.Pass)
	if err != nil {
		return fmt.Errorf("reveal 123Pan password: %w", err)
	}

	staging := configmap.Simple{}
	client := newAPIClientWithHTTP(ctx, loginRoot, apiRoot, staging, opt.User, password, opt.Platform, opt.AccessToken, time.Duration(opt.APIMinInterval), httpClient)
	if err := client.login(ctx, opt.AccessToken); err != nil {
		return fmt.Errorf("authenticate 123Pan account: %w", err)
	}
	token := client.getToken()
	var user api.UserInfoData
	if err := client.call(ctx, client.apiSrv, http.MethodGet, api.UserInfoPath, nil, 0, token, true, true, &user); err != nil {
		return fmt.Errorf("validate 123Pan account: %w", err)
	}
	if user.UID <= 0 {
		return fmt.Errorf("validate 123Pan account: invalid UID %d", user.UID)
	}
	m.Set("access_token", token)
	return nil
}
