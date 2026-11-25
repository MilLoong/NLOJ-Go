package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type account struct {
	ID      int    `json:"id"`
	Name    string `json:"username"`
	Role    string `json:"role"`
	Created string `json:"createTime"`
	salt    [16]byte
	hash    []byte
}

type testCase struct {
	ID     int    `json:"id"`
	Input  string `json:"input"`
	Output string `json:"output"`
	Sample bool   `json:"sample,omitempty"`
}

type problem struct {
	ID          int        `json:"id"`
	Title       string     `json:"title"`
	Difficulty  string     `json:"difficulty"`
	Description string     `json:"description,omitempty"`
	Time        int        `json:"timeLimit"`
	Memory      int        `json:"memoryLimit"`
	Visible     int        `json:"visible"`
	Created     string     `json:"createTime"`
	Type        string     `json:"problemType"`
	Mode        string     `json:"judgeMode"`
	Cases       []testCase `json:"samples,omitempty"`
	Extra       string     `json:"-"`
}

type result struct {
	Status string  `json:"status"`
	Time   *int    `json:"timeUsed"`
	Memory *int    `json:"memoryUsed"`
	Info   *string `json:"judgeInfo"`
}

type submission struct {
	ID       int    `json:"id"`
	User     int    `json:"userId"`
	Problem  int    `json:"problemId"`
	Language string `json:"language"`
	Code     string `json:"code,omitempty"`
	Created  string `json:"createTime"`
	result
}

type session struct {
	user    int
	expires time.Time
}

// Runner must enforce isolation, resource limits, and cancellation outside the API process.
type runner func(context.Context, submission, problem) ([]caseRun, string)

type service struct {
	mu          sync.Mutex
	users       []account
	names       map[string]int
	sessions    map[[32]byte]session
	problems    []problem
	submissions []submission
	jobs        chan job
	disk        *journal
}

type job struct {
	submission submission
	problem    problem
}

func newService() *service {
	return &service{names: make(map[string]int), sessions: make(map[[32]byte]session), jobs: make(chan job, 64)}
}

func timestamp() string { return time.Now().UTC().Format("2006-01-02T15:04:05") }

func passwordHash(password string, salt [16]byte) []byte {
	hash, err := pbkdf2.Key(sha256.New, password, salt[:], 600000, 32)
	if err != nil {
		panic(err)
	}
	return hash
}

func (s *service) addUser(name, password, role string) int {
	a := account{Name: name, Role: role, Created: timestamp()}
	rand.Read(a.salt[:])
	a.hash = passwordHash(password, a.salt)
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.ToLower(name)
	if s.names[key] != 0 {
		return 0
	}
	a.ID = len(s.users) + 1
	if err := s.save(journalRecord{User: &diskAccount{a, a.salt, a.hash}}); err != nil {
		log.Print(err)
		return -1
	}
	s.users = append(s.users, a)
	s.names[key] = a.ID
	return a.ID
}

func (s *service) user(r *http.Request) account {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return account{}
	}
	key := sha256.Sum256([]byte(token))
	auth := s.sessions[key]
	if auth.user == 0 || !time.Now().Before(auth.expires) {
		return account{}
	}
	return s.users[auth.user-1]
}

func respond(w http.ResponseWriter, code int, data any) {
	messages := map[int]string{0: "ok", 40000: "invalid request", 40100: "authentication failed", 40101: "permission denied", 40400: "not found", 50000: "storage failure", 50001: "judge queue full"}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if code != 0 {
		status := code / 100
		if code == 40101 {
			status = 403
		}
		if code == 50001 {
			status = 503
		}
		w.WriteHeader(status)
	}
	json.NewEncoder(w).Encode(map[string]any{"code": code, "message": messages[code], "data": data})
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	return d.Decode(dst) == nil && d.Decode(new(any)) == io.EOF
}

var usernamePattern = regexp.MustCompile("^[A-Za-z0-9_]{3,32}$")

func (s *service) auth(w http.ResponseWriter, r *http.Request) {
	var p struct{ Username, Password string }
	if !decode(w, r, &p) || !usernamePattern.MatchString(p.Username) || len(p.Password) < 6 || len(p.Password) > 64 {
		respond(w, 40000, nil)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/register") {
		id := s.addUser(p.Username, p.Password, "user")
		if id < 0 {
			respond(w, 50000, nil)
			return
		}
		if id == 0 {
			respond(w, 40000, nil)
			return
		}
		respond(w, 0, id)
		return
	}
	s.mu.Lock()
	id := s.names[strings.ToLower(p.Username)]
	var a account
	if id != 0 {
		a = s.users[id-1]
	}
	s.mu.Unlock()
	hash := passwordHash(p.Password, a.salt)
	if subtle.ConstantTimeCompare(hash, a.hash) != 1 {
		respond(w, 40100, nil)
		return
	}
	var raw [32]byte
	rand.Read(raw[:])
	token := hex.EncodeToString(raw[:])
	s.mu.Lock()
	now := time.Now()
	for key, entry := range s.sessions {
		if !now.Before(entry.expires) {
			delete(s.sessions, key)
		}
	}
	s.sessions[sha256.Sum256([]byte(token))] = session{id, now.Add(24 * time.Hour)}
	s.mu.Unlock()
	respond(w, 0, map[string]any{"token": token, "userId": id, "username": a.Name, "role": a.Role})
}

func number(r *http.Request, name string, fallback int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return -1
	}
	return n
}

type page[T any] struct {
	Num     int `json:"pageNum"`
	Size    int `json:"pageSize"`
	Total   int `json:"total"`
	Records []T `json:"records"`
}

func paginate[T any](items []T, n, size, cursor int, match func(T) (int, bool)) page[T] {
	p := page[T]{Num: n, Size: size, Records: make([]T, 0, size)}
	for i := len(items) - 1; i >= 0; i-- {
		id, ok := match(items[i])
		if !ok {
			continue
		}
		rank := p.Total
		p.Total++
		if len(p.Records) == size || (cursor > 0 && id >= cursor) || (cursor == 0 && rank/size < n-1) {
			continue
		}
		p.Records = append(p.Records, items[i])
	}
	return p
}

func difficulty(d string) bool { return d == "EASY" || d == "MEDIUM" || d == "HARD" }

func (s *service) api(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodHead {
		copy := *r
		copy.Method = http.MethodGet
		r = &copy
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/")
	resource, tail, detail := strings.Cut(path, "/")
	id, _ := strconv.Atoi(tail)
	n, size, cursor := number(r, "pageNum", 1), number(r, "pageSize", 20), number(r, "lastId", 0)
	if r.Method == "GET" && !detail && (resource == "problems" || resource == "submissions") && (n < 1 || size < 1 || size > 100 || cursor < 0) {
		respond(w, 40000, nil)
		return
	}
	// Decode before taking the state lock so a slow client cannot block the judge.
	var request struct {
		problem
		ExtraCode string `json:"extraCode"`
	}
	var sub submission
	if r.Method != "GET" {
		var dst any
		switch resource {
		case "problems":
			dst = &request
		case "submissions":
			dst = &sub
		}
		if dst != nil && !decode(w, r, dst) {
			respond(w, 40000, nil)
			return
		}
	}
	p := request.problem
	p.Extra = request.ExtraCode
	s.mu.Lock()
	code, data := s.dispatch(r, resource, detail, id, n, size, cursor, p, sub)
	s.mu.Unlock()
	respond(w, code, data)
}

func (s *service) dispatch(r *http.Request, resource string, detail bool, id, n, size, cursor int, p problem, sub submission) (int, any) {
	a := s.user(r)
	switch resource {
	case "health":
		storage := "memory"
		if s.disk != nil {
			storage = "journal"
			if s.disk.failed != nil {
				return 50000, nil
			}
		}
		return 0, map[string]any{"status": "UP", "storage": storage, "judgeQueueDepth": len(s.jobs)}
	case "users":
		if a.ID == 0 {
			return 40100, nil
		}
		return 0, a
	case "problems":
		if r.Method == "GET" {
			if detail {
				if id < 1 || id > len(s.problems) || s.problems[id-1].Visible == 0 {
					return 40400, nil
				}
				item := s.problems[id-1]
				item.Cases = nil
				for _, c := range s.problems[id-1].Cases {
					if c.Sample {
						c.Sample = false
						item.Cases = append(item.Cases, c)
					}
				}
				return 0, item
			}
			d, keyword := r.URL.Query().Get("difficulty"), strings.ToLower(r.URL.Query().Get("keyword"))
			if d != "" && !difficulty(d) {
				return 40000, nil
			}
			out := paginate(s.problems, n, size, cursor, func(p problem) (int, bool) {
				return p.ID, p.Visible == 1 && (d == "" || d == p.Difficulty) && (keyword == "" || strings.Contains(strings.ToLower(p.Title), keyword))
			})
			for i := range out.Records {
				out.Records[i].Description = ""
				out.Records[i].Cases = nil
			}
			return 0, out
		}
		if a.ID == 0 {
			return 40100, nil
		}
		if a.Role != "admin" {
			return 40101, nil
		}
		if p.Type == "" {
			p.Type = "STANDARD"
		}
		if p.Mode == "" {
			p.Mode = "EXACT"
		}
		validJudge := false
		switch p.Type {
		case "STANDARD":
			validJudge = p.Mode == "EXACT" || (p.Mode == "SPJ" && strings.TrimSpace(p.Extra) != "")
		case "INTERACTIVE", "COMMUNICATION":
			validJudge = p.Mode == "EXACT" && strings.TrimSpace(p.Extra) != ""
		}
		if !validJudge || strings.TrimSpace(p.Title) == "" || !difficulty(p.Difficulty) || p.Time < 1 || p.Time > 60000 || p.Memory < 1 || p.Memory > 1<<20 || (p.Visible != 0 && p.Visible != 1) {
			return 40000, nil
		}
		for i := range p.Cases {
			p.Cases[i].ID = i + 1
		}
		if detail {
			if id < 1 || id > len(s.problems) {
				return 40400, nil
			}
			old := s.problems[id-1]
			p.ID, p.Created = old.ID, old.Created
			if p.Cases == nil {
				p.Cases = old.Cases
			}
			if err := s.save(journalRecord{Problem: &diskProblem{p, p.Extra}}); err != nil {
				log.Print(err)
				return 50000, nil
			}
			s.problems[id-1] = p
			return 0, nil
		}
		p.ID, p.Created = len(s.problems)+1, timestamp()
		if err := s.save(journalRecord{Problem: &diskProblem{p, p.Extra}}); err != nil {
			log.Print(err)
			return 50000, nil
		}
		s.problems = append(s.problems, p)
		return 0, p.ID
	case "submissions":
		if a.ID == 0 {
			return 40100, nil
		}
		if r.Method == "POST" {
			if _, ok := languages[sub.Language]; !ok {
				return 40000, nil
			}
			if strings.TrimSpace(sub.Code) == "" || sub.Problem < 1 || sub.Problem > len(s.problems) || s.problems[sub.Problem-1].Visible != 1 {
				return 40000, nil
			}
			p := s.problems[sub.Problem-1]
			if p.Type != "" && p.Type != "STANDARD" && sub.Language != "C" && sub.Language != "CPP" {
				return 40000, nil
			}
			if p.Type == "COMMUNICATION" {
				if _, _, ok := splitCommunication(sub.Code); !ok {
					return 40000, nil
				}
			}
			sub.ID, sub.User, sub.Created = len(s.submissions)+1, a.ID, timestamp()
			sub.result = result{Status: "PENDING"}
			if len(s.jobs) >= cap(s.jobs) {
				return 50001, nil
			}
			if err := s.save(journalRecord{Submit: &sub, JudgeProblem: &diskProblem{p, p.Extra}}); err != nil {
				log.Print(err)
				return 50000, nil
			}
			s.submissions = append(s.submissions, sub)
			s.jobs <- job{sub, p}
			return 0, map[string]int{"submissionId": sub.ID}
		}
		if detail {
			if id < 1 || id > len(s.submissions) {
				return 40400, nil
			}
			item := s.submissions[id-1]
			if item.User != a.ID && a.Role != "admin" {
				return 40101, nil
			}
			return 0, item
		}
		problemID, status := number(r, "problemId", 0), r.URL.Query().Get("status")
		if problemID < 0 {
			return 40000, nil
		}
		out := paginate(s.submissions, n, size, cursor, func(item submission) (int, bool) {
			return item.ID, item.User == a.ID && (problemID == 0 || problemID == item.Problem) && (status == "" || status == item.Status)
		})
		for i := range out.Records {
			out.Records[i].Code = ""
		}
		return 0, out
	}
	return 40400, nil
}

func (s *service) work(ctx context.Context, run runner) {
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case j := <-s.jobs:
			s.mu.Lock()
			s.submissions[j.submission.ID-1].Status = "JUDGING"
			s.mu.Unlock()
			res := result{Status: "SYSTEM_ERROR"}
			if run != nil && len(j.problem.Cases) > 0 {
				runs, status := run(ctx, j.submission, j.problem)
				switch status {
				case "OK":
					res = caseVerdict(j.problem.Cases, runs, j.problem.Type == "STANDARD" && j.problem.Mode == "EXACT")
				case "CE":
					res.Status = "CE"
				}
			}
			s.mu.Lock()
			if ctx.Err() != nil {
				s.mu.Unlock()
				return
			}
			if err := s.save(journalRecord{ID: j.submission.ID, Finish: &res}); err != nil {
				log.Print(err)
				s.mu.Unlock()
				return
			}
			s.submissions[j.submission.ID-1].result = res
			s.mu.Unlock()
		}
	}
}

func (s *service) handler() http.Handler {
	mux := http.NewServeMux()
	for _, route := range []string{"POST /auth/register", "POST /auth/login"} {
		method, path, _ := strings.Cut(route, " ")
		mux.HandleFunc(method+" /api/v1"+path, s.auth)
	}
	for _, route := range []string{"GET /health", "GET /users/me", "GET /problems", "GET /problems/{id}", "POST /problems", "PUT /problems/{id}", "GET /submissions", "GET /submissions/{id}", "POST /submissions"} {
		method, path, _ := strings.Cut(route, " ")
		mux.HandleFunc(method+" /api/v1"+path, s.api)
	}
	return mux
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "worker" {
		token := os.Getenv("NLOJ_WORKER_TOKEN")
		if len(token) < 32 {
			log.Fatal("NLOJ_WORKER_TOKEN must be at least 32 bytes")
		}
		run, err := dockerRunner(os.Getenv("NLOJ_JUDGE_IMAGE"))
		if err != nil {
			log.Fatal(err)
		}
		server := http.Server{Addr: "127.0.0.1:8081", Handler: workerHandler(token, run), ReadTimeout: 10 * time.Second, WriteTimeout: judgeDeadline + time.Minute, IdleTimeout: 30 * time.Second}
		log.Fatal(server.ListenAndServe())
	}
	path := os.Getenv("NLOJ_JOURNAL")
	if path == "" {
		path = "nloj.journal"
	} else if path == "-" {
		path = ""
	}
	s, err := loadService(path)
	if err != nil {
		log.Fatal(err)
	}
	if password := os.Getenv("NLOJ_ADMIN_PASSWORD"); password != "" {
		if len(password) < 12 || len(password) > 64 {
			log.Fatal("NLOJ_ADMIN_PASSWORD must be 12-64 bytes")
		}
		if s.addUser("admin", password, "admin") < 0 {
			log.Fatal("could not save administrator")
		}
	}
	var run runner
	if endpoint := os.Getenv("NLOJ_WORKER_URL"); endpoint != "" {
		run, err = remoteRunner(endpoint, os.Getenv("NLOJ_WORKER_TOKEN"))
	} else if image := os.Getenv("NLOJ_JUDGE_IMAGE"); image != "" {
		run, err = dockerRunner(image)
	}
	if err != nil {
		log.Fatal(err)
	}
	if run != nil {
		go s.work(context.Background(), run)
	}
	server := http.Server{Addr: "127.0.0.1:8080", Handler: s.handler(), ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
	log.Fatal(server.ListenAndServe())
}
