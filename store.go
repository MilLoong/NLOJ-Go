package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type diskAccount struct {
	account
	Salt [16]byte
	Hash []byte
}

type diskProblem struct {
	problem
	ExtraCode string
}

type journalRecord struct {
	User         *diskAccount `json:",omitempty"`
	Problem      *diskProblem `json:",omitempty"`
	Submit       *submission  `json:",omitempty"`
	JudgeProblem *diskProblem `json:",omitempty"`
	Finish       *result      `json:",omitempty"`
	ID           int          `json:",omitempty"`
}

type journal struct {
	file   *os.File
	failed error
}

// Call under the service lock, before publishing an in-memory change.
func (s *service) save(record journalRecord) error {
	if s.disk == nil {
		return nil
	}
	j := s.disk
	if j.failed != nil {
		return j.failed
	}
	j.failed = json.NewEncoder(j.file).Encode(record)
	if j.failed == nil {
		j.failed = j.file.Sync()
	}
	if j.failed != nil {
		return fmt.Errorf("journal write: %w", j.failed)
	}
	return nil
}

func loadService(path string) (*service, error) {
	s := newService()
	if path == "" {
		return s, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			file.Close()
		}
	}()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	// A crash can leave the final append incomplete. A complete malformed
	// record is corruption, not a reason to silently discard accepted work.
	end := bytes.LastIndexByte(data, '\n') + 1
	if end != len(data) {
		if err := file.Truncate(int64(end)); err != nil {
			return nil, err
		}
		if err := file.Sync(); err != nil {
			return nil, err
		}
	}
	pending := make(map[int]job)
	for _, line := range bytes.Split(data[:end], []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var r journalRecord
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("decode journal: %w", err)
		}
		switch {
		case r.User != nil && r.User.ID == len(s.users)+1:
			a := r.User.account
			a.salt, a.hash = r.User.Salt, r.User.Hash
			s.users = append(s.users, a)
			s.names[strings.ToLower(a.Name)] = a.ID
		case r.Problem != nil && r.Problem.ID > 0 && r.Problem.ID <= len(s.problems)+1:
			p := r.Problem.problem
			p.Extra = r.Problem.ExtraCode
			if p.ID == len(s.problems)+1 {
				s.problems = append(s.problems, p)
			} else {
				s.problems[p.ID-1] = p
			}
		case r.Submit != nil && r.JudgeProblem != nil && r.Submit.ID == len(s.submissions)+1:
			p := r.JudgeProblem.problem
			p.Extra = r.JudgeProblem.ExtraCode
			sub := *r.Submit
			sub.result = result{Status: "PENDING"}
			s.submissions = append(s.submissions, sub)
			pending[sub.ID] = job{sub, p}
		case r.Finish != nil && pending[r.ID].submission.ID != 0:
			s.submissions[r.ID-1].result = *r.Finish
			delete(pending, r.ID)
		default:
			return nil, errors.New("unexpected journal record")
		}
	}
	if _, err := file.Seek(int64(end), io.SeekStart); err != nil {
		return nil, err
	}
	if len(pending) > cap(s.jobs) {
		s.jobs = make(chan job, len(pending))
	}
	for _, sub := range s.submissions {
		if j, ok := pending[sub.ID]; ok {
			s.jobs <- j
		}
	}
	s.disk = &journal{file: file}
	ok = true
	return s, nil
}
