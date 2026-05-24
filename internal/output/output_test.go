package output

import "testing"

func TestErrorCodeFromStatus(t *testing.T) {
	if ErrorCodeFromStatus(401) != ErrAuth {
		t.Fatal()
	}
	if ErrorCodeFromStatus(500) != ErrServer {
		t.Fatal()
	}
}

func TestHintForErrorCode(t *testing.T) {
	if HintForErrorCode(ErrAuth) == "" {
		t.Fatal("expected hint")
	}
}
