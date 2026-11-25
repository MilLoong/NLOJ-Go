package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nloj-go/internal/judge"
)

func TestWorkerHTTP(t *testing.T) {
	token := strings.Repeat("x", 32)
	p := problem{Type: "STANDARD", Mode: "EXACT", Time: 1000, Memory: 65536, Cases: []testCase{{Input: "20 22", Output: "42"}}}
	srv := httptest.NewServer(workerHandler(token, func(_ context.Context, sub submission, got problem) ([]caseRun, string) {
		if got.Cases[0].Input != "20 22" || got.Cases[0].Output != "42" {
			return nil, "SYSTEM_ERROR"
		}
		return []caseRun{{Status: "OK", Stdout: sub.Code, Time: 7, Memory: -1}}, "OK"
	}))
	defer srv.Close()

	run, err := remoteRunner(srv.URL+"/judge", token)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ source, want string }{{"42 \r\n", "AC"}, {"41", "WA"}} {
		runs, status := run(context.Background(), submission{Code: tc.source}, p)
		if status != "OK" || judge.Verdict(p.Cases, runs, true).Status != tc.want {
			t.Fatalf("source=%q worker=%s runs=%+v want=%s", tc.source, status, runs, tc.want)
		}
	}
	res, err := http.Post(srv.URL+"/judge", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request: HTTP %d", res.StatusCode)
	}
	t.Log("loopback worker: normalized output AC, mismatched output WA, missing token HTTP 401")
}
