package bench

import "testing"

func TestRedactAccount(t *testing.T) {
	in := `HTTP 429: {"error":{"message":"Provider returned error","code":429},"user_id":"user_2abcDEF123"}`
	want := `HTTP 429: {"error":{"message":"Provider returned error","code":429},"user_id":"redacted"}`
	if got := redactAccount(in); got != want {
		t.Fatalf("got %s", got)
	}
	if got := redactAccount("plain error"); got != "plain error" {
		t.Fatalf("changed text without an account id: %s", got)
	}
}
