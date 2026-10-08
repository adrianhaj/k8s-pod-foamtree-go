package kube

import (
	"strings"
	"testing"
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
