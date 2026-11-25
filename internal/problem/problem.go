package problem

import (
	"nloj-go/internal/judge"
	"strings"
)

type Problem struct {
	ID          int              `json:"id"`
	Title       string           `json:"title"`
	Difficulty  string           `json:"difficulty"`
	Description string           `json:"description,omitempty"`
	Time        int              `json:"timeLimit"`
	Memory      int              `json:"memoryLimit"`
	Visible     int              `json:"visible"`
	Created     string           `json:"createTime"`
	Type        string           `json:"problemType"`
	Mode        string           `json:"judgeMode"`
	Cases       []judge.TestCase `json:"samples,omitempty"`
	Extra       string           `json:"-"`
}

func ValidDifficulty(d string) bool { return d == "EASY" || d == "MEDIUM" || d == "HARD" }

func (p *Problem) Prepare() bool {
	if p.Type == "" {
		p.Type = "STANDARD"
	}
	if p.Mode == "" {
		p.Mode = "EXACT"
	}
	extra := strings.TrimSpace(p.Extra) != ""
	var validJudge bool
	switch p.Type {
	case "STANDARD":
		validJudge = p.Mode == "EXACT" || (p.Mode == "SPJ" && extra)
	case "INTERACTIVE", "COMMUNICATION":
		validJudge = p.Mode == "EXACT" && extra
	}
	if !validJudge || strings.TrimSpace(p.Title) == "" || !ValidDifficulty(p.Difficulty) || p.Time < 1 || p.Time > 60000 || p.Memory < 1 || p.Memory > 1<<20 || (p.Visible != 0 && p.Visible != 1) {
		return false
	}
	for i := range p.Cases {
		p.Cases[i].ID = i + 1
	}
	return true
}

func (p Problem) PublicDetail() Problem {
	all := p.Cases
	p.Cases = nil
	for _, c := range all {
		if c.Sample {
			c.Sample = false
			p.Cases = append(p.Cases, c)
		}
	}
	return p
}
