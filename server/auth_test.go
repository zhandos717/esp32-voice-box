package main

import (
	"encoding/hex"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func signInitData(token string, v url.Values) string {
	secret := hmacSHA256([]byte("WebAppData"), []byte(token))
	check := "auth_date=" + v.Get("auth_date") + "\nquery_id=" + v.Get("query_id") + "\nuser=" + v.Get("user")
	v.Set("hash", hex.EncodeToString(hmacSHA256(secret, []byte(check))))
	return v.Encode()
}

func TestCheckInitData(t *testing.T) {
	tok := "123:abc"
	v := url.Values{"auth_date": {strconv.FormatInt(time.Now().Unix(), 10)}, "query_id": {"q"}, "user": {`{"id":42,"first_name":"A"}`}}
	data := signInitData(tok, v)
	if id, ok := checkInitData(tok, data, time.Hour); !ok || id != 42 {
		t.Fatalf("valid data refused: %d %v", id, ok)
	}
	if _, ok := checkInitData("123:other", data, time.Hour); ok {
		t.Fatal("wrong token accepted")
	}
	old := url.Values{"auth_date": {strconv.FormatInt(time.Now().Add(-2*time.Hour).Unix(), 10)}, "query_id": {"q"}, "user": {`{"id":42}`}}
	if _, ok := checkInitData(tok, signInitData(tok, old), time.Hour); ok {
		t.Fatal("stale data accepted")
	}
}

func TestSession(t *testing.T) {
	t.Setenv("MIC_WEB_USERS", "42")
	tg := &telegram{token: "123:abc"}
	v := tg.newSession(42)
	if !tg.sessionUser(v) {
		t.Fatal("own session refused")
	}
	if tg.sessionUser(tg.newSession(7)) {
		t.Fatal("session of a user outside MIC_WEB_USERS accepted")
	}
	if tg.sessionUser(v[:len(v)-1] + "0") {
		t.Fatal("tampered session accepted")
	}
	if (&telegram{token: "123:other"}).sessionUser(v) {
		t.Fatal("session signed with another token accepted")
	}
}
