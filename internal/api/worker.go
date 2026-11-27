package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nloj-go/internal/judge"
)

const judgeDeadline = 20 * time.Minute

type workerRequest struct {
	Submission submission
	Problem    diskProblem
}

type workerResponse struct {
	Status string
	Runs   []caseRun
}

func workerHandler(token string, run runner) http.Handler {
	mux := gin.New()
	slot := make(chan struct{}, 1)
	want := sha256.Sum256([]byte("Bearer " + token))
	mux.POST("/judge", gin.WrapF(func(w http.ResponseWriter, r *http.Request) {
		got := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		select {
		case slot <- struct{}{}:
			defer func() { <-slot }()
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var request workerRequest
		body := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
		if body.Decode(&request) != nil || body.Decode(new(any)) != io.EOF {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p := request.Problem.problem
		p.Extra = request.Problem.ExtraCode
		if p.Time < 1 || p.Time > 60000 || p.Memory < 1 || p.Memory > 1<<20 || len(p.Cases) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(executeRequest(r.Context(), run, request.Submission, p))
	}))
	return mux
}

func executeRequest(parent context.Context, run runner, sub submission, p problem) workerResponse {
	if p.Time < 1 || p.Time > 60000 || p.Memory < 1 || p.Memory > 1<<20 || len(p.Cases) == 0 {
		return workerResponse{Status: "SYSTEM_ERROR"}
	}
	ctx, cancel := context.WithTimeout(parent, judgeDeadline)
	defer cancel()
	runs, status := run(ctx, sub, p)
	for i := range runs {
		if i < len(p.Cases) && runs[i].Status == "OK" && p.Type == "STANDARD" && p.Mode == "EXACT" &&
			judge.NormalizeOutput(runs[i].Stdout) != judge.NormalizeOutput(p.Cases[i].Output) {
			runs[i].Status = "WA"
		}
		runs[i].Stdout = ""
	}
	return workerResponse{status, runs}
}

func dockerRunner(image string) (runner, error) {
	if image == "" || strings.HasPrefix(image, "-") {
		return nil, errors.New("NLOJ_JUDGE_IMAGE must be a Docker image reference")
	}
	binary, err := exec.LookPath("docker")
	if err != nil {
		return nil, err
	}
	sandbox := judge.DockerSandbox{Binary: binary, Image: image}
	return func(ctx context.Context, sub submission, p problem) ([]caseRun, string) {
		return sandbox.Run(ctx, judge.Submission{Language: sub.Language, Code: sub.Code}, judge.Problem{
			Type: p.Type, Mode: p.Mode, Extra: p.Extra, Time: p.Time, Memory: p.Memory, Cases: p.Cases,
		})
	}, nil
}

func remoteRunner(endpoint, token string) (runner, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) {
		return nil, errors.New("worker URL must be HTTPS or loopback HTTP")
	}
	if len(token) < 32 {
		return nil, errors.New("NLOJ_WORKER_TOKEN must be at least 32 bytes")
	}
	client := http.Client{Timeout: judgeDeadline, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, sub submission, p problem) ([]caseRun, string) {
		body, err := json.Marshal(workerRequest{sub, diskProblem{p, p.Extra}})
		if err != nil {
			return nil, "SYSTEM_ERROR"
		}
		req, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(body))
		if err != nil {
			return nil, "SYSTEM_ERROR"
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := client.Do(req)
		if err != nil {
			log.Print("judge worker request: ", err)
			return nil, "SYSTEM_ERROR"
		}
		defer res.Body.Close()
		var out workerResponse
		if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&out) != nil || len(out.Runs) > len(p.Cases) {
			return nil, "SYSTEM_ERROR"
		}
		for i := range out.Runs {
			if out.Runs[i].Status == "OK" {
				out.Runs[i].Stdout = p.Cases[i].Output
			}
		}
		return out.Runs, out.Status
	}, nil
}
