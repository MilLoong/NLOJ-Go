package api

import (
	"path/filepath"
	"testing"

	"nloj-go/internal/judge"
)

func TestJournalRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "judge.journal")
	s, err := loadService(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.addUser("alice", "alice-fixture", "user") != 1 {
		t.Fatal("failed to save user")
	}
	p := problem{ID: 1, Title: "original", Extra: "checker", Cases: []testCase{{Output: "expected"}}}
	if err := s.save(journalRecord{Problem: &diskProblem{p, p.Extra}}); err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 2; id++ {
		sub := submission{ID: id, User: 1, Problem: 1}
		if err := s.save(journalRecord{Submit: &sub, JudgeProblem: &diskProblem{p, p.Extra}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.save(journalRecord{ID: 1, Finish: &result{Status: "AC"}}); err != nil {
		t.Fatal(err)
	}
	p.Title = "updated"
	if err := s.save(journalRecord{Problem: &diskProblem{p, p.Extra}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.disk.file.WriteString(`{"Submit":`); err != nil {
		t.Fatal(err)
	}
	if err := s.disk.file.Close(); err != nil {
		t.Fatal(err)
	}
	recovered, err := loadService(path)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.disk.file.Close()
	if len(recovered.users) != 1 || len(recovered.users[0].Hash) != 32 ||
		len(recovered.submissions) != 2 || recovered.submissions[0].Status != "AC" ||
		recovered.submissions[1].Status != "PENDING" || len(recovered.jobs) != 1 ||
		recovered.problems[0].Title != "updated" {
		t.Fatal("incorrect recovered state")
	}
	j := <-recovered.jobs
	if j.submission.ID != 2 || j.problem.Title != "original" || j.problem.Extra != "checker" || j.problem.Cases[0].Output != "expected" {
		t.Fatal("recovered job lost its original problem snapshot")
	}
}

func TestSplitCommunication(t *testing.T) {
	a, b, ok := judge.SplitCommunication("===NLOJ_FILE:bob===B\n===NLOJ_FILE:alice===A")
	if !ok || a != "A" || b != "B\n" {
		t.Fatalf("unexpected split: %q %q %v", a, b, ok)
	}
	if _, _, ok := judge.SplitCommunication("===NLOJ_FILE:alice===A"); ok {
		t.Fatal("accepted missing bob source")
	}
}
