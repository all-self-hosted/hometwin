package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const sessionCookie = "hometwin_session"

// Guard puts a password in front of everything h serves. A right password at /login sets a
// cookie (an HMAC of the password under a key kept in keyFile, so it survives restarts and
// a new password signs everyone out); without it pages get the login form and /api gets 401.
// An empty password turns the guard off.
func Guard(h http.Handler, password, keyFile string) (http.Handler, error) {
	if password == "" {
		return h, nil
	}
	key, err := sessionKey(keyFile)
	if err != nil {
		return nil, err
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(password))
	token := hex.EncodeToString(m.Sum(nil))

	signedIn := func(r *http.Request) bool {
		c, err := r.Cookie(sessionCookie)
		return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) == 1
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login" && r.Method == http.MethodPost:
			if subtle.ConstantTimeCompare([]byte(r.PostFormValue("password")), []byte(password)) != 1 {
				time.Sleep(700 * time.Millisecond) // slows guessing down
				loginPage(w, http.StatusUnauthorized, "密码不对")
				return
			}
			http.SetCookie(w, &http.Cookie{
				Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
				SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: 180 * 24 * 3600,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
		case r.URL.Path == "/logout":
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1})
			http.Redirect(w, r, "/", http.StatusSeeOther)
		case signedIn(r):
			h.ServeHTTP(w, r)
		case strings.HasPrefix(r.URL.Path, "/api/"):
			fail(w, http.StatusUnauthorized, "需要先登录")
		default:
			loginPage(w, http.StatusOK, "")
		}
	}), nil
}

// sessionKey reads the cookie signing key, making one the first time.
func sessionKey(file string) ([]byte, error) {
	if b, err := os.ReadFile(file); err == nil && len(b) >= 32 {
		return b, nil
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return nil, err
	}
	return b, os.WriteFile(file, b, 0o600)
}

func loginPage(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	note := ""
	if msg != "" {
		note = `<p class="err">` + html.EscapeString(msg) + `</p>`
	}
	w.Write([]byte(`<!doctype html><html lang="zh"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>HomeTwin</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#f6f4f0;color:#1f2328;font:15px/1.5 system-ui,sans-serif}
form{width:min(320px,calc(100vw - 32px));display:grid;gap:12px;padding:28px;background:#fff;border-radius:18px;box-shadow:0 8px 30px #0000000f}
h1{margin:0 0 4px;font-size:20px}input,button{font:inherit;height:42px;border-radius:12px;padding:0 12px}
input{border:1px solid #d9d4cc}button{border:0;background:#d8692f;color:#fff;font-weight:700;cursor:pointer}.err{margin:0;color:#c62828;font-size:13px}</style></head>
<body><form method="post" action="/login"><h1>HomeTwin</h1>` + note + `<input type="password" name="password" placeholder="密码" autofocus required autocomplete="current-password"><button>进入</button></form></body></html>`))
}
