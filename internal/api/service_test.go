package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MilLoong/Nailong-OJ-Go/internal/judge"
)

func TestAPI(t *testing.T) {
	s := newService()
	s.addUser("admin", "fixture-password", "admin")
	h := s.handler()
	call := func(method, path, token, body string, want int) json.RawMessage {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out struct {
			Code int
			Data json.RawMessage
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Code != want {
			t.Fatalf("%s %s: HTTP %d %s; want code %d", method, path, w.Code, w.Body, want)
		}
		return out.Data
	}
	login := func(name string, register bool) string {
		payload := fmt.Sprintf(`{"username":%q,"password":"fixture-password"}`, name)
		if register {
			call("POST", "/auth/register", "", payload, 0)
		}
		var data struct{ Token string }
		json.Unmarshal(call("POST", "/auth/login", "", payload, 0), &data)
		return data.Token
	}
	admin, alice, bob := login("admin", false), login("alice", true), login("bob", true)
	problemJSON := `{"title":"A+B","difficulty":"EASY","description":"full statement","timeLimit":1000,"memoryLimit":65536,"visible":1,"samples":[{"sample":true,"input":"1 2","output":"3"},{"input":"hidden-1","output":"7"}]}`
	call("POST", "/problems", alice, problemJSON, 40101)
	for i := 0; i < 3; i++ {
		call("POST", "/problems", admin, problemJSON, 0)
	}
	detail := call("GET", "/problems/1", "", "", 0)
	if !strings.Contains(string(detail), "1 2") || strings.Contains(string(detail), "hidden-1") {
		t.Fatal(string(detail))
	}
	first := call("GET", "/problems?pageSize=2", "", "", 0)
	call("POST", "/problems", admin, problemJSON, 0)
	second := call("GET", "/problems?pageNum=999&pageSize=2&lastId=2", "", "", 0)
	var a, b page[problem]
	json.Unmarshal(first, &a)
	json.Unmarshal(second, &b)
	if len(a.Records) != 2 || a.Records[0].ID != 3 || a.Records[1].ID != 2 || a.Total != 3 ||
		len(b.Records) != 1 || b.Records[0].ID != 1 || b.Total != 4 || strings.Contains(string(first), "full statement") {
		t.Fatalf("first=%s second=%s", first, second)
	}
	t.Log("cursor: first=[3,2] total=3; insert ID=4; next=[1] total=4")
	call("POST", "/submissions", alice, `{"problemId":1,"language":"CPP","code":"fixture-source","status":"AC"}`, 0)
	call("GET", "/submissions/1", bob, "", 40101)
	check := func(want string) {
		t.Helper()
		var got submission
		json.Unmarshal(call("GET", "/submissions/1", alice, "", 0), &got)
		if got.Status != want {
			t.Fatalf("status=%s, want %s", got.Status, want)
		}
	}
	check("PENDING")
	ctx, cancel := context.WithCancel(context.Background())
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() { cancel(); <-done }()
	go func() {
		defer close(done)
		s.work(ctx, func(ctx context.Context, _ submission, _ problem) ([]caseRun, string) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, "SYSTEM_ERROR"
			}
			return []caseRun{{"OK", "3 \r\n", 2, 10}, {"OK", "8", 5, 12}}, "OK"
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	check("JUDGING")
	close(release)
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		status := s.submissions[0].Status
		s.mu.Unlock()
		if status == "WA" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not publish WA")
		}
		time.Sleep(time.Millisecond)
	}
	row := call("GET", "/submissions/1", admin, "", 0)
	if !strings.Contains(string(row), "WA on test case 2") || strings.Contains(string(row), "hidden-1") {
		t.Fatal(string(row))
	}
	list := call("GET", "/submissions?status=WA", alice, "", 0)
	if strings.Contains(string(list), "fixture-source") || !strings.Contains(string(list), `"total":1`) {
		t.Fatal(string(list))
	}
	t.Log("PENDING -> JUDGING -> WA on test case 2; cross-user code=40101; source omitted from list")
}

func TestExactVerdict(t *testing.T) {
	cases := []testCase{{Output: "1 2"}, {Output: "3"}}
	for _, tt := range []struct {
		name, want string
		runs       []caseRun
	}{
		{"line endings and trailing whitespace", "AC", []caseRun{{"OK", "1 2\t\r\n\r\n", 2, 20}, {"OK", "3", 8, 11}}},
		{"internal whitespace", "WA", []caseRun{{"OK", "1  2", 2, 20}}},
		{"short success", "SYSTEM_ERROR", []caseRun{{"OK", "1 2", 2, 20}}},
		{"timeout", "TLE", []caseRun{{"TLE", "", 1001, 20}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := judge.Verdict(cases, tt.runs, true)
			if r.Status != tt.want {
				t.Fatalf("status=%s, want %s", r.Status, tt.want)
			}
			if r.Status == "AC" && (*r.Time != 8 || *r.Memory != 20) {
				t.Fatalf("peak metrics: time=%d memory=%d", *r.Time, *r.Memory)
			}
		})
	}
}

func TestQueueBackpressure(t *testing.T) {
	s := newService()
	s.problems = []problem{{ID: 1, Visible: 1}}
	s.users = []account{{ID: 1}}
	token := "fixture-token"
	s.sessions[sha256.Sum256([]byte(token))] = session{1, time.Now().Add(time.Hour)}
	h := s.handler()
	for i := 0; i <= cap(s.jobs); i++ {
		r := httptest.NewRequest("POST", "/api/v1/submissions", strings.NewReader(`{"problemId":1,"language":"CPP","code":"fixture"}`))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 200
		if i == cap(s.jobs) {
			want = 503
		}
		if w.Code != want {
			t.Fatalf("request %d: HTTP %d, want %d", i+1, w.Code, want)
		}
	}
	if len(s.submissions) != cap(s.jobs) {
		t.Fatal("rejected submission left a row")
	}
	t.Log("64 queued; request 65 returned HTTP 503 without creating a submission")
}

func BenchmarkProblemPage(b *testing.B) {
	s := newService()
	for i := 1; i <= 100000; i++ {
		s.problems = append(s.problems, problem{ID: i, Title: "A+B", Visible: 1, Difficulty: "EASY"})
	}
	h := s.handler()
	for _, kind := range []struct{ name, query string }{{"offset", "pageNum=4501"}, {"cursor", "lastId=10001"}} {
		b.Run(kind.name, func(b *testing.B) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/problems?"+kind.query, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var response struct{ Data page[problem] }
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Data.Records) != 20 || response.Data.Records[0].ID != 10000 {
				b.Fatalf("unexpected page: %s", w.Body)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.ServeHTTP(httptest.NewRecorder(), r)
			}
		})
	}
}
