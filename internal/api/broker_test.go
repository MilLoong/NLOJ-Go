package api

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestMySQLRabbitMQRoundTrip(t *testing.T) {
	dsn, url := os.Getenv("NLOJ_TEST_MYSQL_DSN"), os.Getenv("NLOJ_TEST_RABBITMQ_URL")
	if dsn == "" || url == "" {
		t.Skip("set NLOJ_TEST_MYSQL_DSN to a fresh test database and NLOJ_TEST_RABBITMQ_URL")
	}
	s, err := mysqlService(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.backends.db.Close()
	if len(s.users)+len(s.problems)+len(s.submissions) != 0 {
		t.Fatal("test database is not empty")
	}
	if s.addUser("fixture", "fixture-password", "user") != 1 {
		t.Fatal("failed to persist user")
	}
	p := problem{ID: 1, Title: "A+B", Type: "STANDARD", Mode: "EXACT", Time: 1000, Memory: 65536, Visible: 1,
		Cases: []testCase{{Input: "20 22", Output: "42"}}}
	sub := submission{ID: 1, User: 1, Problem: 1, Language: "CPP", Code: "fixture"}
	if err := s.save(journalRecord{Problem: &diskProblem{p, ""}}); err != nil {
		t.Fatal(err)
	}
	if err := s.save(journalRecord{Submit: &sub, JudgeProblem: &diskProblem{p, ""}}); err != nil {
		t.Fatal(err)
	}
	restarted, err := mysqlService(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.backends.db.Close()
	if len(restarted.jobs) != 1 || restarted.submissions[0].Status != "PENDING" {
		t.Fatal("pending submission not restored from MySQL")
	}
	b := broker{url, fmt.Sprintf("nloj-go.integration.%d", time.Now().UnixNano()), restarted.backends.db}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	consumerDone := make(chan error, 1)
	go func() {
		consumerDone <- b.consume(ctx, func(_ context.Context, in submission, input problem) ([]caseRun, string) {
			if in.Code != "fixture" || len(input.Cases) != 1 || input.Cases[0].Input != "20 22" {
				return nil, "SYSTEM_ERROR"
			}
			return []caseRun{{Status: "OK", Stdout: "42\n", Time: 1, Memory: -1}}, "OK"
		})
	}()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		restarted.work(ctx, b.run)
	}()
	defer func() {
		cancel()
		<-workerDone
		<-consumerDone
		conn, channel, err := b.open()
		if err == nil {
			defer conn.Close()
			channel.QueueDelete(b.queue, false, false, false)
		}
	}()
	for {
		restarted.mu.Lock()
		got := restarted.submissions[0].Status
		restarted.mu.Unlock()
		if got == "AC" {
			break
		}
		if got == "SYSTEM_ERROR" {
			t.Fatal("unexpected SYSTEM_ERROR")
		}
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for RabbitMQ worker result")
		case <-time.After(50 * time.Millisecond):
		}
	}
	finalState, err := mysqlService(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer finalState.backends.db.Close()
	if len(finalState.jobs) != 0 || finalState.submissions[0].Status != "AC" {
		t.Fatal("finished submission not restored from MySQL")
	}
	t.Log("MySQL replay: PENDING before RabbitMQ delivery, AC after worker result and restart")
}
