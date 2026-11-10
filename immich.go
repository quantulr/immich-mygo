package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"immich-mygo/immichapi"
)

// requestTimeout is how long a check waits for the server.
const requestTimeout = 10 * time.Second

// serverInfo is what checking a server found.
type serverInfo struct {
	// version is the Immich release the server runs, e.g. "1.119.1"; it is
	// empty when the server did not tell.
	version string
}

// account is a signed-in user: who they are, and the token that proves
// it to the server.
type account struct {
	ID    string
	Name  string
	Email string
	Admin bool
	Token string
}

// errSessionExpired is what a token the server refuses fails with.
var errSessionExpired = errors.New("登录已过期，请重新登录")

// normalizeServerURL turns what the user typed into the server's base
// address: an http or https URL, without /api and without a trailing
// slash. A missing scheme is https, as in a browser's address bar.
func normalizeServerURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("请输入服务器地址")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("地址格式不正确，请检查后重试")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("仅支持 http:// 或 https:// 地址")
	}
	if u.Hostname() == "" {
		return "", errors.New("地址缺少服务器名称")
	}
	if u.User != nil {
		return "", errors.New("地址中不能包含用户名或密码")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("地址不能带参数或锚点")
	}
	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/api")
	path = strings.TrimRight(path, "/")
	u.Path, u.RawPath = path, ""
	return u.String(), nil
}

// pingImmich asks the server at base, an address normalizeServerURL
// returned, whether it is Immich, and which version it runs. Servers from
// before Immich 2.x serve the same endpoints under /server-info instead of
// /server, and are asked there.
func pingImmich(base string) (serverInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	hc := &http.Client{Timeout: requestTimeout}
	client, err := immichapi.NewClientWithResponses(base+"/api", immichapi.WithHTTPClient(hc))
	if err != nil {
		return serverInfo{}, errors.New("地址格式不正确，请检查后重试")
	}

	resp, err := client.PingServerWithResponse(ctx)
	if err != nil {
		if isJSONError(err) {
			return serverInfo{}, errors.New("服务器响应无法识别，可能不是 Immich")
		}
		return serverInfo{}, requestError(err)
	}
	switch {
	case resp.StatusCode() == http.StatusOK && resp.JSON200 != nil && resp.JSON200.Res == "pong":
		// The current endpoint answered.
	case resp.StatusCode() == http.StatusOK:
		return serverInfo{}, errors.New("这个地址看起来不是 Immich 服务器")
	case resp.StatusCode() == http.StatusNotFound:
		// Probably a server from before the /server-info rename.
		if err := legacyPing(ctx, hc, base); err != nil {
			return serverInfo{}, err
		}
	default:
		return serverInfo{}, responseError(resp.StatusCode(), resp.Status())
	}

	return serverInfo{version: serverVersion(ctx, client, hc, base)}, nil
}

// legacyPing asks a server whose /server endpoints answer 404.
func legacyPing(ctx context.Context, hc *http.Client, base string) error {
	var ping struct {
		Res string `json:"res"`
	}
	if err := getJSON(ctx, hc, base+"/api/server-info/ping", &ping); err != nil {
		return err
	}
	if ping.Res != "pong" {
		return errors.New("这个地址看起来不是 Immich 服务器")
	}
	return nil
}

// serverVersion returns the release the server runs, or "" when it does
// not tell. The version is a nicety: the server is Immich either way.
func serverVersion(ctx context.Context, client *immichapi.ClientWithResponses, hc *http.Client, base string) string {
	if resp, err := client.GetServerVersionWithResponse(ctx); err == nil && resp.JSON200 != nil {
		v := resp.JSON200
		return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	}
	var legacy struct {
		Major int `json:"major"`
		Minor int `json:"minor"`
		Patch int `json:"patch"`
	}
	if err := getJSON(ctx, hc, base+"/api/server-info/version", &legacy); err != nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d", legacy.Major, legacy.Minor, legacy.Patch)
}

// loginImmich signs in at base with email and password, and returns the
// account the server granted.
func loginImmich(base, email, password string) (*account, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client, err := immichClient(base, "")
	if err != nil {
		return nil, errors.New("地址格式不正确，请检查后重试")
	}
	resp, err := client.LoginWithResponse(ctx, immichapi.LoginJSONRequestBody{
		Email:    openapi_types.Email(email),
		Password: password,
	})
	if err != nil {
		if isJSONError(err) {
			return nil, errors.New("服务器响应无法识别，可能不是 Immich")
		}
		return nil, requestError(err)
	}

	switch resp.StatusCode() {
	case http.StatusCreated:
		if resp.JSON201 == nil || resp.JSON201.AccessToken == "" {
			return nil, errors.New("服务器没有返回登录令牌")
		}
		u := resp.JSON201
		return &account{
			ID:    u.UserId.String(),
			Name:  u.Name,
			Email: string(u.UserEmail),
			Admin: u.IsAdmin,
			Token: u.AccessToken,
		}, nil
	case http.StatusUnauthorized:
		return nil, errors.New(loginMessage(resp.Body, "邮箱或密码不正确"))
	case http.StatusBadRequest:
		return nil, errors.New(loginMessage(resp.Body, "登录请求无效"))
	case http.StatusTooManyRequests:
		return nil, errors.New("尝试次数过多，请稍后再试")
	default:
		return nil, responseError(resp.StatusCode(), resp.Status())
	}
}

// fetchAccount asks the server at base who token belongs to. A token the
// server refuses is errSessionExpired; the account keeps it for the
// calls that follow.
func fetchAccount(base, token string) (*account, error) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client, err := immichClient(base, token)
	if err != nil {
		return nil, errors.New("地址格式不正确，请检查后重试")
	}
	resp, err := client.GetMyUserWithResponse(ctx)
	if err != nil {
		if isJSONError(err) {
			return nil, errors.New("服务器响应无法识别，可能不是 Immich")
		}
		return nil, requestError(err)
	}
	switch resp.StatusCode() {
	case http.StatusOK:
		if resp.JSON200 == nil {
			return nil, errors.New("服务器没有返回用户信息")
		}
		u := resp.JSON200
		return &account{
			ID:    u.Id.String(),
			Name:  u.Name,
			Email: string(u.Email),
			Admin: u.IsAdmin,
			Token: token,
		}, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, errSessionExpired
	default:
		return nil, responseError(resp.StatusCode(), resp.Status())
	}
}

// logoutImmich asks the server to invalidate the session token. It is
// best effort: the app forgets the token whether or not the server
// answers.
func logoutImmich(base, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client, err := immichClient(base, token)
	if err != nil {
		return
	}
	_, _ = client.LogoutWithResponse(ctx)
}

// immichClient returns a client for base, an address normalizeServerURL
// returned, with the request timeout. A token, when not empty, goes in
// the Authorization header of every request.
func immichClient(base, token string) (*immichapi.ClientWithResponses, error) {
	opts := []immichapi.ClientOption{
		immichapi.WithHTTPClient(&http.Client{Timeout: requestTimeout}),
	}
	if token != "" {
		opts = append(opts, immichapi.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		}))
	}
	return immichapi.NewClientWithResponses(base+"/api", opts...)
}

// loginMessage reads the message of a refused login, in the user's words
// for the server's common refusals.
func loginMessage(body []byte, fallback string) string {
	var resp struct {
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(body, &resp); err != nil || len(resp.Message) == 0 {
		return fallback
	}
	var msg string
	if json.Unmarshal(resp.Message, &msg) == nil && msg != "" {
		switch msg {
		case "Incorrect email or password":
			return "邮箱或密码不正确"
		case "Password login has been disabled":
			return "该服务器已禁用密码登录"
		}
		return msg
	}
	var list []string
	if json.Unmarshal(resp.Message, &list) == nil && len(list) > 0 {
		return list[0]
	}
	return fallback
}

// getJSON reads endpoint into dst, for the endpoints older servers serve
// under /server-info.
func getJSON(ctx context.Context, hc *http.Client, endpoint string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("地址格式不正确，请检查后重试")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return requestError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return responseError(resp.StatusCode, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return errors.New("读取服务器响应失败")
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return errors.New("服务器响应无法识别，可能不是 Immich")
	}
	return nil
}

// responseError names a response status in the user's words.
func responseError(code int, status string) error {
	switch {
	case code == http.StatusNotFound:
		return errors.New("没有找到 Immich 接口，请确认服务器地址")
	case code == http.StatusUnauthorized, code == http.StatusForbidden:
		return fmt.Errorf("服务器拒绝了访问（%s），请确认地址", status)
	default:
		return fmt.Errorf("服务器返回了 %s", status)
	}
}

// isJSONError reports whether err came from reading a response as JSON,
// rather than from the request itself.
func isJSONError(err error) bool {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	return errors.As(err, &syntaxErr) || errors.As(err, &typeErr)
}

// requestError names a failed request in the user's words where it can:
// a timeout, a name that does not resolve, a refused connection, a
// certificate that does not verify.
func requestError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("连接超时，请检查地址和网络")
	}
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return errors.New("找不到服务器，请检查地址是否拼写正确")
	}
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return errors.New("服务器证书验证失败，请确认地址安全可信")
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok && opErr.Err != nil &&
		strings.Contains(opErr.Err.Error(), "refused") {
		return errors.New("服务器拒绝连接，请检查端口")
	}
	if urlErr, ok := errors.AsType[*url.Error](err); ok {
		err = urlErr.Err
	}
	return fmt.Errorf("无法连接到服务器：%v", err)
}
