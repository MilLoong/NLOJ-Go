package api

import (
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Persistence models mirror the existing schema, not the public JSON API.
type userRow struct {
	ID                      int `gorm:"primaryKey;autoIncrement:false"`
	Username, Role, Created string
	Salt, PasswordHash      []byte
}

func (userRow) TableName() string { return "nloj_go_user" }

type problemRow struct {
	ID                                         int `gorm:"primaryKey;autoIncrement:false"`
	Title, Difficulty, Description             string
	TimeLimit, MemoryLimit, Visible            int
	Created, ProblemType, JudgeMode, ExtraCode string
}

func (problemRow) TableName() string { return "nloj_go_problem" }

type caseRow struct {
	ProblemID                 int `gorm:"primaryKey;autoIncrement:false"`
	CaseNo                    int `gorm:"primaryKey;autoIncrement:false"`
	InputData, ExpectedOutput string
	IsSample                  bool
}

func (caseRow) TableName() string { return "nloj_go_problem_case" }

type submissionRow struct {
	ID                                    int `gorm:"primaryKey;autoIncrement:false"`
	UserID, ProblemID                     int
	Language, SourceCode, Created, Status string
	ProblemSnapshot                       string
}

func (submissionRow) TableName() string { return "nloj_go_submission" }

func persist(tx *gorm.DB, r journalRecord) error {
	switch {
	case r.User != nil:
		u := r.User
		return tx.Create(&userRow{ID: u.ID, Username: u.Name, Role: u.Role, Created: u.Created, Salt: u.Salt[:], PasswordHash: u.Hash}).Error
	case r.Problem != nil:
		p := r.Problem
		row := problemRow{ID: p.ID, Title: p.Title, Difficulty: p.Difficulty, Description: p.Description, TimeLimit: p.Time, MemoryLimit: p.Memory, Visible: p.Visible, Created: p.Created, ProblemType: p.Type, JudgeMode: p.Mode, ExtraCode: p.ExtraCode}
		if err := tx.Clauses(clause.OnConflict{DoUpdates: clause.AssignmentColumns([]string{"title", "difficulty", "description", "time_limit", "memory_limit", "visible", "problem_type", "judge_mode", "extra_code"})}).Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Where("problem_id = ?", p.ID).Delete(&caseRow{}).Error; err != nil {
			return err
		}
		cases := make([]caseRow, 0, len(p.Cases))
		for _, c := range p.Cases {
			cases = append(cases, caseRow{ProblemID: p.ID, CaseNo: c.ID, InputData: c.Input, ExpectedOutput: c.Output, IsSample: c.Sample})
		}
		if len(cases) == 0 {
			return nil
		}
		return tx.CreateInBatches(&cases, 100).Error
	case r.Submit != nil && r.JudgeProblem != nil:
		s := r.Submit
		snapshot, err := json.Marshal(r.JudgeProblem)
		if err != nil {
			return err
		}
		return tx.Create(&submissionRow{ID: s.ID, UserID: s.User, ProblemID: s.Problem, Language: s.Language, SourceCode: s.Code, Created: s.Created, Status: "PENDING", ProblemSnapshot: string(snapshot)}).Error
	case r.Finish != nil:
		v := r.Finish
		return tx.Model(&submissionRow{}).Where("id = ?", r.ID).Updates(map[string]any{"status": v.Status, "time_used": v.Time, "memory_used": v.Memory, "judge_info": v.Info}).Error
	default:
		return fmt.Errorf("invalid persistence record")
	}
}
