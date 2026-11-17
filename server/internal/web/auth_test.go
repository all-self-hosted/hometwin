package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuard(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("home")) })
	key := filepath.Join(t.TempDir(), "session.key")
	h, err := Guard(inner, "s3cret", key)
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, form url.Values, c *http.Cookie) *httptest.ResponseRecorder {
		var body *strings.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		} else {
			body = strings.NewReader("")
		}
		r := httptest.NewRequest(method, path, body)
		if form != nil {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if w := do("GET", "/api/home", nil, nil); w.Code != 401 {
		t.Errorf("api without a cookie: %d", w.Code)
	}
	if w := do("GET", "/", nil, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `name="password"`) {
		t.Errorf("page without a cookie: %d %q", w.Code, w.Body.String())
	}
	if w := do("POST", "/login", url.Values{"password": {"wrong"}}, nil); w.Code != 401 || len(w.Result().Cookies()) != 0 {
		t.Errorf("wrong password: %d, cookies %v", w.Code, w.Result().Cookies())
	}
	w := do("POST", "/login", url.Values{"password": {"s3cret"}}, nil)
	cs := w.Result().Cookies()
	if w.Code != 303 || len(cs) != 1 || !cs[0].HttpOnly {
		t.Fatalf("right password: %d, cookies %v", w.Code, cs)
	}
	if w := do("GET", "/api/home", nil, cs[0]); w.Code != 200 || w.Body.String() != "home" {
		t.Errorf("api with the cookie: %d", w.Code)
	}
	if w := do("GET", "/api/home", nil, &http.Cookie{Name: sessionCookie, Value: "forged"}); w.Code != 401 {
		t.Errorf("forged cookie: %d", w.Code)
	}

	// the key is kept, so a restart keeps people signed in; a new password does not
	again, _ := Guard(inner, "s3cret", key)
	r := httptest.NewRequest("GET", "/api/home", nil)
	r.AddCookie(cs[0])
	rec := httptest.NewRecorder()
	again.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Errorf("after a restart: %d", rec.Code)
	}
	changed, _ := Guard(inner, "other", key)
	rec = httptest.NewRecorder()
	changed.ServeHTTP(rec, r)
	if rec.Code != 401 {
		t.Errorf("after a password change: %d", rec.Code)
	}

	open, _ := Guard(inner, "", key)
	rec = httptest.NewRecorder()
	open.ServeHTTP(rec, httptest.NewRequest("GET", "/api/home", nil))
	if rec.Code != 200 {
		t.Errorf("no password should let everyone in: %d", rec.Code)
	}
}
