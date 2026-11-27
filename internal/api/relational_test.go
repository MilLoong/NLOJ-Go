package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestMySQLLegacyMigration(t *testing.T) {
	dsn := os.Getenv("NLOJ_TEST_MIGRATION_DSN")
	if dsn == "" {
		t.Skip("requires fresh migration test database")
	}
	db, err := openMySQL(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE nloj_go_events(seq BIGINT AUTO_INCREMENT PRIMARY KEY,payload JSON NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	p := problem{ID: 1, Title: "A+B", Difficulty: "EASY", Visible: 1, Cases: []testCase{{ID: 1, Input: "hidden"}}}
	sub := submission{ID: 1, User: 1, Problem: 1, Code: "private"}
	for _, r := range []journalRecord{
		{User: &diskAccount{account: account{ID: 1, Name: "fixture"}, Hash: make([]byte, 32)}},
		{Problem: &diskProblem{p, "checker"}},
		{Submit: &sub, JudgeProblem: &diskProblem{p, "checker"}},
		{ID: 1, Finish: &result{Status: "AC"}},
	} {
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO nloj_go_events(payload) VALUES (?)", raw); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		s, err := mysqlService(dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer s.backends.db.Close()
		if len(s.users) != 1 || len(s.problems) != 1 || len(s.problems[0].Cases) != 1 || s.problems[0].Extra != "checker" || len(s.submissions) != 1 || s.submissions[0].Status != "AC" {
			t.Fatal("migration lost business state")
		}
		code, ps := s.backends.problemPage(context.Background(), "EASY", "a+b", 1, 20, 0)
		if code != 0 || ps.(page[problem]).Total != 1 || len(ps.(page[problem]).Records) != 1 {
			t.Fatalf("problem list: %v", ps)
		}
		code, ss := s.backends.submissionPage(context.Background(), 1, 1, "AC", 1, 20, 0)
		if code != 0 || ss.(page[submission]).Total != 1 || len(ss.(page[submission]).Records) != 1 || ss.(page[submission]).Records[0].Code != "" {
			t.Fatalf("submission list: %v", ss)
		}
		req := httptest.NewRequest("GET", "/api/v1/submissions/1", nil)
		req.Header.Set("Authorization", "Bearer fixture")
		s.sessions[sha256.Sum256([]byte("fixture"))] = session{1, time.Now().Add(time.Hour)}
		code, data := s.dispatch(req, "submissions", true, 1, 1, 20, 0, problem{}, submission{})
		if code != 0 || data.(submission).Code != "private" {
			t.Fatalf("owner could not read persisted source: %d", code)
		}
	}
	if _, err := db.Exec("UPDATE nloj_go_submission SET status='JUDGING' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	recovered, err := mysqlService(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.backends.db.Close()
	var status string
	if err := db.QueryRow("SELECT status FROM nloj_go_submission WHERE id=1").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "PENDING" || len(recovered.jobs) != 1 || recovered.submissions[0].Code != "" {
		t.Fatal("inconsistent recovered submission")
	}
	if j := <-recovered.jobs; j.submission.Code != "private" {
		t.Fatal("lost queued source")
	}
}
