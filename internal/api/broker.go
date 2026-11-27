package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type broker struct {
	url, queue string
	db         *sql.DB
}

type brokerJob struct {
	Key     string
	Request workerRequest
}

func (b broker) open() (*amqp.Connection, *amqp.Channel, error) {
	conn, err := amqp.DialConfig(b.url, amqp.Config{Dial: amqp.DefaultDial(5 * time.Second), Heartbeat: 10 * time.Second})
	if err != nil {
		return nil, nil, err
	}
	ch, err := conn.Channel()
	if err == nil {
		_, err = ch.QueueDeclare(b.queue, true, false, false, false, nil)
	}
	if err != nil {
		conn.Close()
		return nil, nil, err
	}
	return conn, ch, nil
}

func (b broker) publish(ctx context.Context, payload []byte) error {
	conn, ch, err := b.open()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = ch.Confirm(false); err != nil {
		return err
	}
	confirmation, err := ch.PublishWithDeferredConfirmWithContext(ctx, "", b.queue, false, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: payload,
	})
	if err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ack, err := confirmation.WaitContext(wait)
	if err != nil {
		return err
	}
	if !ack {
		return errors.New("RabbitMQ rejected publication")
	}
	return nil
}

func (b broker) load(ctx context.Context, key string) (workerResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var data []byte
	var res workerResponse
	if err := b.db.QueryRowContext(ctx, "SELECT payload FROM nloj_go_judge_results WHERE job_key = ?", key).Scan(&data); err != nil {
		return res, err
	}
	return res, json.Unmarshal(data, &res)
}

func (b broker) run(ctx context.Context, sub submission, p problem) ([]caseRun, string) {
	request := workerRequest{sub, diskProblem{p, p.Extra}}
	data, err := json.Marshal(request)
	if err != nil {
		return nil, "SYSTEM_ERROR"
	}
	hash := sha256.Sum256(data)
	key := hex.EncodeToString(hash[:])
	payload, err := json.Marshal(brokerJob{key, request})
	if err != nil {
		return nil, "SYSTEM_ERROR"
	}
	nextPublish := time.Time{}
	for ctx.Err() == nil {
		res, err := b.load(ctx, key)
		if err == nil {
			if len(res.Runs) > len(p.Cases) {
				return nil, "SYSTEM_ERROR"
			}
			for i := range res.Runs {
				if res.Runs[i].Status == "OK" {
					res.Runs[i].Stdout = p.Cases[i].Output
				}
			}
			return res.Runs, res.Status
		}
		if errors.Is(err, sql.ErrNoRows) && time.Now().After(nextPublish) {
			nextPublish = time.Now().Add(5 * time.Second)
			if err := b.publish(ctx, payload); err != nil {
				log.Printf("judge queue publication: %v", err)
			} else {
				nextPublish = time.Now().Add(judgeDeadline + time.Minute)
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
	return nil, "SYSTEM_ERROR"
}

func (b broker) consume(ctx context.Context, run runner) error {
	conn, ch, err := b.open()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}
	deliveries, err := ch.Consume(b.queue, "", false, true, false, false, nil)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-deliveries:
			if !ok {
				return errors.New("RabbitMQ consumer disconnected")
			}
			var job brokerJob
			if json.Unmarshal(msg.Body, &job) != nil || len(job.Key) != 64 {
				if err := msg.Reject(false); err != nil {
					return err
				}
				continue
			}
			_, err := b.load(ctx, job.Key)
			if errors.Is(err, sql.ErrNoRows) {
				p := job.Request.Problem.problem
				p.Extra = job.Request.Problem.ExtraCode
				out := executeRequest(ctx, run, job.Request.Submission, p)
				payload, encodeErr := json.Marshal(out)
				if encodeErr != nil {
					return encodeErr
				}
				write, cancel := context.WithTimeout(ctx, 5*time.Second)
				_, err = b.db.ExecContext(write, "INSERT INTO nloj_go_judge_results (job_key,payload) VALUES (?,?) ON DUPLICATE KEY UPDATE job_key=job_key", job.Key, payload)
				cancel()
			}
			if err != nil {
				return err
			}
			if err := msg.Ack(false); err != nil {
				return err
			}
		}
	}
}
