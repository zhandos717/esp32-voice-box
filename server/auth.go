package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- Telegram Mini App: the page is published on MIC_WEB_URL, only the paired chat's owner gets in ----------

const sessionCookie = "mic_session"

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// checkInitData verifies the Mini App launch data (core.telegram.org/bots/webapps#validating-data-received-via-the-mini-app)
// and returns the Telegram user id.
func checkInitData(token, initData string, maxAge time.Duration) (int64, bool) {
	q, err := url.ParseQuery(initData)
	if err != nil {
		return 0, false
	}
	hash := q.Get("hash")
	q.Del("hash")
	pairs := make([]string, 0, len(q))
	for k := range q {
		pairs = append(pairs, k+"="+q.Get(k))
	}
	sort.Strings(pairs)
	secret := hmacSHA256([]byte("WebAppData"), []byte(token))
	want := hex.EncodeToString(hmacSHA256(secret, []byte(strings.Join(pairs, "\n"))))
	if !hmac.Equal([]byte(want), []byte(hash)) {
		return 0, false
	}
	at, _ := strconv.ParseInt(q.Get("auth_date"), 10, 64)
	if time.Since(time.Unix(at, 0)) > maxAge {
		return 0, false
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal([]byte(q.Get("user")), &user) != nil || user.ID == 0 {
		return 0, false
	}
	return user.ID, true
}

// allowedUsers: MIC_WEB_USERS (comma separated ids), otherwise the paired private chat (its id is the user id).
func (t *telegram) allowedUsers() map[int64]bool {
	set := map[int64]bool{}
	for _, f := range strings.Split(env("MIC_WEB_USERS", ""), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64); err == nil {
			set[id] = true
		}
	}
	if len(set) == 0 && t.chatID != nil && *t.chatID > 0 {
		set[*t.chatID] = true
	}
	return set
}

const sessionTTL = 30 * 24 * time.Hour

// newSession: "<user id>.<expiry unix>.<hmac>", so a cookie carries who it was issued to and when it dies.
func (t *telegram) newSession(user int64) string {
	body := strconv.FormatInt(user, 10) + "." + strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10)
	return body + "." + hex.EncodeToString(hmacSHA256([]byte(t.token), []byte("mic-web-session:"+body)))
}

// sessionUser re-checks the signature, the expiry and that the user is still allowed.
func (t *telegram) sessionUser(v string) bool {
	i := strings.LastIndexByte(v, '.')
	if i < 0 {
		return false
	}
	body, sig := v[:i], v[i+1:]
	want := hex.EncodeToString(hmacSHA256([]byte(t.token), []byte("mic-web-session:"+body)))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return false
	}
	parts := strings.SplitN(body, ".", 2)
	if len(parts) != 2 {
		return false
	}
	user, err1 := strconv.ParseInt(parts[0], 10, 64)
	exp, err2 := strconv.ParseInt(parts[1], 10, 64)
	return err1 == nil && err2 == nil && time.Now().Unix() < exp && t.allowedUsers()[user]
}

// sameOrigin: state-changing requests must come from the page itself (CSRF).
func sameOrigin(r *http.Request, site string) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	o := r.Header.Get("Origin")
	if o == "" {
		o = r.Header.Get("Referer")
	}
	u, err := url.Parse(site)
	return err == nil && o != "" && strings.HasPrefix(o+"/", u.Scheme+"://"+u.Host+"/")
}

// withAuth guards everything except the page itself and /auth. Without MIC_WEB_URL the page stays local and open, as before.
func (s *server) withAuth(next http.Handler) http.Handler {
	site := env("MIC_WEB_URL", "")
	if site == "" {
		return next
	}
	if s.tg == nil {
		log.Println("web: MIC_WEB_URL is set but Telegram is not configured, the page is closed")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			next.ServeHTTP(w, r)
			return
		}
		if s.tg == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !sameOrigin(r, site) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/auth" {
			body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<10))
			id, ok := checkInitData(s.tg.token, string(body), 24*time.Hour)
			if !ok || !s.tg.allowedUsers()[id] {
				log.Printf("web: login refused (user %d)", id)
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: s.tg.newSession(id), Path: "/", MaxAge: int(sessionTTL.Seconds()),
				HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
			w.Write([]byte("ok"))
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || !s.tg.sessionUser(c.Value) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// setMenuButton puts an "open" button next to the bot's input field that launches the page as a Mini App.
func (t *telegram) setMenuButton() {
	u := env("MIC_WEB_URL", "")
	if u == "" {
		return
	}
	_, err := t.api("setChatMenuButton", map[string]any{
		"menu_button": map[string]any{"type": "web_app", "text": "Пульт", "web_app": map[string]any{"url": u}},
	}, nil, 15*time.Second)
	if err != nil {
		log.Println("telegram: menu button:", err)
	}
}
