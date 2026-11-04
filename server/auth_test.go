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
