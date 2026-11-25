package submit

import (
	"nloj-go/internal/judge"
	"nloj-go/internal/problem"
	"strings"
)

type Submission struct {
	ID       int    `json:"id"`
	User     int    `json:"userId"`
	Problem  int    `json:"problemId"`
	Language string `json:"language"`
	Code     string `json:"code,omitempty"`
	Created  string `json:"createTime"`
	judge.Result
}

func (s Submission) ValidFor(p problem.Problem) bool {
	if !judge.SupportsLanguage(s.Language) || strings.TrimSpace(s.Code) == "" || p.Visible != 1 {
		return false
	}
	if p.Type != "" && p.Type != "STANDARD" && s.Language != "C" && s.Language != "CPP" {
		return false
	}
	if p.Type == "COMMUNICATION" {
		_, _, ok := judge.SplitCommunication(s.Code)
		return ok
	}
	return true
}

func (s Submission) CanView(userID int, role string) bool {
	return s.User == userID || role == "admin"
}
