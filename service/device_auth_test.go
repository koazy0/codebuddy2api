package service

import "testing"

func TestParseAuthState(t *testing.T) {
	raw := []byte(`{"code":0,"msg":"OK","data":{"state":"abc","authUrl":"https://www.codebuddy.cn/login?x=1"}}`)
	session, err := parseAuthState(raw)
	if err != nil {
		t.Fatal(err)
	}
	if session.State != "abc" || session.AuthURL == "" {
		t.Fatalf("%+v", session)
	}
}

func TestParseAuthTokenPendingAndOK(t *testing.T) {
	pending, token, err := parseAuthToken([]byte(`{"code":10008,"msg":"pending"}`))
	if err != nil || !pending || token != nil {
		t.Fatalf("pending=%v token=%v err=%v", pending, token, err)
	}
	pending, token, err = parseAuthToken([]byte(`{"code":0,"data":{"accessToken":"jwt-1","refreshToken":"rt-1","expiresIn":10}}`))
	if err != nil || pending || token == nil || token.AccessToken != "jwt-1" || token.RefreshToken != "rt-1" {
		t.Fatalf("pending=%v token=%+v err=%v", pending, token, err)
	}
}

func TestParseAuthTokenError(t *testing.T) {
	_, _, err := parseAuthToken([]byte(`{"code":1,"msg":"bad state"}`))
	if err == nil {
		t.Fatal("expected error")
	}
}
