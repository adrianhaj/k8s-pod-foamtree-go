package kube

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseThrottle(t *testing.T) {
	ok := `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"namespace":"ns","pod":"web-0"},"value":[1700000000,"0.4"]}]}}`
	got, err := parseThrottle(strings.NewReader(ok))
	if err != nil || got["ns/web-0"] != 0.4 {
		t.Fatalf("got %v, err %v", got, err)
	}
	if _, err := parseThrottle(strings.NewReader(`{"status":"error","error":"boom"}`)); err == nil {
		t.Fatal("an error status must fail")
	}
}

func TestSharesDropStaleOnFailure(t *testing.T) {
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"status":"success","data":{"result":[{"metric":{"namespace":"ns","pod":"p"},"value":[1,"0.4"]}]}}`)
	}))
	defer srv.Close()
	th := &Throttle{URL: srv.URL}
	if got, err := th.Shares(t.Context()); err != nil || got["ns/p"] != 0.4 {
		t.Fatalf("got %v, err %v", got, err)
	}
	fail, th.at = true, time.Time{}
	if _, err := th.Shares(t.Context()); err == nil {
		t.Fatal("a failed query must report its error")
	}
	if got, _ := th.Shares(t.Context()); got != nil {
		t.Fatalf("a failure must not leave the old shares cached: %v", got)
	}
}
