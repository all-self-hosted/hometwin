// Package ha connects HomeTwin to a Home Assistant: its WebSocket API for states, events and
// service calls, its registries for areas and devices, and a mapping from HA entities to the
// twin's devices. The access token is read from the Mac Keychain (or HA_TOKEN elsewhere) when
// it is needed and is never logged, printed or written anywhere.
package ha

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// TokenSource hands out the long-lived access token.
type TokenSource func() (string, error)

// NeedToken is what the owner does to give HomeTwin a token.
const NeedToken = "在 Home Assistant 里：头像（个人资料）→ 安全 → 长期访问令牌 → 创建令牌，复制后在终端运行 " +
	"security add-generic-password -a hometwin -s hometwin-ha -w ，按提示粘贴令牌（不要把令牌写进命令行、聊天或文件）"

// ErrNoToken says no token has been stored yet.
var ErrNoToken = errors.New("还没有 Home Assistant 的访问令牌。" + NeedToken)

// KeychainToken reads the token from the Mac Keychain (account hometwin, service hometwin-ha).
func KeychainToken() TokenSource {
	return func() (string, error) {
		if runtime.GOOS != "darwin" {
			return "", ErrNoToken
		}
		var out, errb bytes.Buffer
		cmd := exec.Command("security", "find-generic-password", "-a", "hometwin", "-s", "hometwin-ha", "-w")
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", ErrNoToken
		}
		t := strings.TrimSpace(out.String())
		if t == "" {
			return "", ErrNoToken
		}
		return t, nil
	}
}

// EnvToken reads HA_TOKEN, or HA_TOKEN= from the env-style file HA_TOKEN_FILE names.
func EnvToken() TokenSource {
	return func() (string, error) {
		if t := strings.TrimSpace(os.Getenv("HA_TOKEN")); t != "" {
			return t, nil
		}
		path := os.Getenv("HA_TOKEN_FILE")
		if path == "" {
			return "", ErrNoToken
		}
		f, err := os.Open(path)
		if err != nil {
			return "", ErrNoToken
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			line = strings.TrimPrefix(line, "export ")
			if v, ok := strings.CutPrefix(line, "HA_TOKEN="); ok {
				v = strings.Trim(strings.TrimSpace(v), `"'`)
				if v != "" {
					return v, nil
				}
			}
		}
		return "", ErrNoToken
	}
}

// DefaultToken is the Keychain on a Mac, then the environment.
func DefaultToken() TokenSource {
	kc, env := KeychainToken(), EnvToken()
	return func() (string, error) {
		if t, err := kc(); err == nil {
			return t, nil
		}
		return env()
	}
}

// StaticToken is a fixed token, for tests.
func StaticToken(t string) TokenSource { return func() (string, error) { return t, nil } }

// scrub keeps a token out of any text that may be shown or logged.
func scrub(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "***")
}
