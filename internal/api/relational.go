package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nloj-go/internal/common"
)

var schema = []string{
	`CREATE TABLE IF NOT EXISTS nloj_go_user (
	id BIGINT PRIMARY KEY, username VARCHAR(32) NOT NULL UNIQUE, role VARCHAR(16) NOT NULL,
	created VARCHAR(32) NOT NULL, salt BINARY(16) NOT NULL, password_hash BINARY(32) NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS nloj_go_problem (
	id BIGINT PRIMARY KEY, title TEXT NOT NULL, difficulty VARCHAR(16) NOT NULL, description MEDIUMTEXT NOT NULL,
	time_limit INT NOT NULL, memory_limit INT NOT NULL, visible TINYINT NOT NULL, created VARCHAR(32) NOT NULL,
	problem_type VARCHAR(32) NOT NULL, judge_mode VARCHAR(16) NOT NULL, extra_code MEDIUMTEXT NOT NULL
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS nloj_go_problem_case (
	problem_id BIGINT NOT NULL, case_no INT NOT NULL, input_data MEDIUMTEXT NOT NULL,
	expected_output MEDIUMTEXT NOT NULL, is_sample BOOLEAN NOT NULL, PRIMARY KEY(problem_id,case_no),
	FOREIGN KEY(problem_id) REFERENCES nloj_go_problem(id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	`CREATE TABLE IF NOT EXISTS nloj_go_submission (
	id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, problem_id BIGINT NOT NULL, language VARCHAR(16) NOT NULL,
	source_code MEDIUMTEXT NOT NULL, created VARCHAR(32) NOT NULL, status VARCHAR(20) NOT NULL,
	time_used INT NULL, memory_used INT NULL, judge_info TEXT NULL, problem_snapshot JSON NOT NULL,
	INDEX(user_id,id), INDEX(problem_id,status), FOREIGN KEY(user_id) REFERENCES nloj_go_user(id),
	FOREIGN KEY(problem_id) REFERENCES nloj_go_problem(id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	"CREATE TABLE IF NOT EXISTS nloj_go_judge_results (job_key CHAR(64) NOT NULL PRIMARY KEY, payload JSON NOT NULL)",
	"CREATE TABLE IF NOT EXISTS nloj_go_migrations (version INT PRIMARY KEY) ENGINE=InnoDB",
}

// table, columns and where are built exclusively from constants below.
func sqlPage[T any](ctx context.Context, db *sql.DB, table, columns, where string, args []any, n, size, cursor int, scan func(*sql.Rows, *T) error) (int, any) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p := common.Page[T]{Num: n, Size: size, Records: make([]T, 0, size)}
	from := " FROM " + table + " WHERE " + where
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*)"+from, args...).Scan(&p.Total); err != nil {
		return 50000, nil
	}
	q := "SELECT " + columns + from
	offset := 0
	if cursor > 0 {
		q += " AND id<?"
		args = append(args, cursor)
	} else {
		if n-1 > p.Total/size {
			return 0, p
		}
		offset = (n - 1) * size
	}
	rows, err := db.QueryContext(ctx, q+" ORDER BY id DESC LIMIT ? OFFSET ?", append(args, size, offset)...)
	if err != nil {
		return 50000, nil
	}
	defer rows.Close()
	for rows.Next() {
		var v T
		if err := scan(rows, &v); err != nil {
			return 50000, nil
		}
		p.Records = append(p.Records, v)
	}
	if rows.Err() != nil {
		return 50000, nil
	}
	return 0, p
}

func (b *backends) problemPage(ctx context.Context, d, keyword string, n, size, cursor int) (int, any) {
	where := "visible=1"
	var args []any
	if d != "" {
		where += " AND difficulty=?"
		args = append(args, d)
	}
	if keyword != "" {
		where += " AND LOCATE(BINARY ?,BINARY LOWER(title))>0"
		args = append(args, keyword)
	}
	return sqlPage(ctx, b.db, "nloj_go_problem", "id,title,difficulty,time_limit,memory_limit,visible,created,problem_type,judge_mode", where, args, n, size, cursor, func(rows *sql.Rows, p *problem) error {
		return rows.Scan(&p.ID, &p.Title, &p.Difficulty, &p.Time, &p.Memory, &p.Visible, &p.Created, &p.Type, &p.Mode)
	})
}

func (b *backends) submissionPage(ctx context.Context, uid, pid int, status string, n, size, cursor int) (int, any) {
	where := "user_id=?"
	args := []any{uid}
	if pid > 0 {
		where += " AND problem_id=?"
		args = append(args, pid)
	}
	if status != "" {
		where += " AND BINARY status=?"
		args = append(args, status)
	}
	return sqlPage(ctx, b.db, "nloj_go_submission", "id,user_id,problem_id,language,created,status,time_used,memory_used,judge_info", where, args, n, size, cursor, func(rows *sql.Rows, s *submission) error {
		return rows.Scan(&s.ID, &s.User, &s.Problem, &s.Language, &s.Created, &s.Status, &s.Time, &s.Memory, &s.Info)
	})
}

// Kept independent of the current ORM models for the version-1 migration.
func writeLegacyRow(ctx context.Context, tx *sql.Tx, r journalRecord) error {
	exec := func(q string, args ...any) error { _, err := tx.ExecContext(ctx, q, args...); return err }
	switch {
	case r.User != nil:
		u := r.User
		return exec("INSERT INTO nloj_go_user (id,username,role,created,salt,password_hash) VALUES (?,?,?,?,?,?)", u.ID, u.Name, u.Role, u.Created, u.Salt[:], u.Hash)
	case r.Problem != nil:
		p := r.Problem
		if err := exec(`INSERT INTO nloj_go_problem (id,title,difficulty,description,time_limit,memory_limit,visible,created,problem_type,judge_mode,extra_code)
		VALUES (?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE title=VALUES(title), difficulty=VALUES(difficulty),description=VALUES(description),
		time_limit=VALUES(time_limit),memory_limit=VALUES(memory_limit),visible=VALUES(visible),problem_type=VALUES(problem_type),judge_mode=VALUES(judge_mode),extra_code=VALUES(extra_code)`,
			p.ID, p.Title, p.Difficulty, p.Description, p.Time, p.Memory, p.Visible, p.Created, p.Type, p.Mode, p.ExtraCode); err != nil {
			return err
		}
		if err := exec("DELETE FROM nloj_go_problem_case WHERE problem_id=?", p.ID); err != nil {
			return err
		}
		for _, c := range p.Cases {
			if err := exec("INSERT INTO nloj_go_problem_case (problem_id,case_no,input_data,expected_output,is_sample) VALUES (?,?,?,?,?)", p.ID, c.ID, c.Input, c.Output, c.Sample); err != nil {
				return err
			}
		}
		return nil
	case r.Submit != nil && r.JudgeProblem != nil:
		s := r.Submit
		snap, err := json.Marshal(r.JudgeProblem)
		if err != nil {
			return err
		}
		return exec("INSERT INTO nloj_go_submission (id,user_id,problem_id,language,source_code,created,status,problem_snapshot) VALUES (?,?,?,?,?,?,'PENDING',?)", s.ID, s.User, s.Problem, s.Language, s.Code, s.Created, snap)
	case r.Finish != nil:
		v := r.Finish
		return exec("UPDATE nloj_go_submission SET status=?,time_used=?,memory_used=?,judge_info=? WHERE id=?", v.Status, v.Time, v.Memory, v.Info, r.ID)
	default:
		return fmt.Errorf("invalid persistence record")
	}
}

func migrateLegacy(db *sql.DB) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	mark, err := tx.Exec("INSERT IGNORE INTO nloj_go_migrations(version) VALUES (1)")
	if err != nil {
		return err
	}
	n, err := mark.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return tx.Commit()
	}
	var exists int
	if err := tx.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='nloj_go_events'").Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		var seq uint64
		for {
			var raw []byte
			err := tx.QueryRow("SELECT seq,payload FROM nloj_go_events WHERE seq>? ORDER BY seq LIMIT 1", seq).Scan(&seq, &raw)
			if err == sql.ErrNoRows {
				break
			}
			if err != nil {
				return err
			}
			var rec journalRecord
			if err := json.Unmarshal(raw, &rec); err != nil {
				return fmt.Errorf("legacy event %d: %w", seq, err)
			}
			if err := writeLegacyRow(ctx, tx, rec); err != nil {
				return fmt.Errorf("legacy event %d: %w", seq, err)
			}
		}
	}
	return tx.Commit()
}

func (s *service) loadTables(db *sql.DB) error {
	read := func(q string, visit func(*sql.Rows) error) error {
		rows, err := db.Query(q)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := visit(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	if err := read("SELECT id,username,role,created,salt,password_hash FROM nloj_go_user ORDER BY id", func(row *sql.Rows) error {
		var u account
		var salt []byte
		if err := row.Scan(&u.ID, &u.Name, &u.Role, &u.Created, &salt, &u.Hash); err != nil {
			return err
		}
		if u.ID != len(s.users)+1 {
			return fmt.Errorf("non-contiguous user id %d", u.ID)
		}
		copy(u.Salt[:], salt)
		s.users = append(s.users, u)
		s.names[strings.ToLower(u.Name)] = u.ID
		return nil
	}); err != nil {
		return err
	}
	if err := read("SELECT id,title,difficulty,description,time_limit,memory_limit,visible,created,problem_type,judge_mode,extra_code FROM nloj_go_problem ORDER BY id", func(row *sql.Rows) error {
		var p problem
		if err := row.Scan(&p.ID, &p.Title, &p.Difficulty, &p.Description, &p.Time, &p.Memory, &p.Visible, &p.Created, &p.Type, &p.Mode, &p.Extra); err != nil {
			return err
		}
		if p.ID != len(s.problems)+1 {
			return fmt.Errorf("non-contiguous problem id %d", p.ID)
		}
		s.problems = append(s.problems, p)
		return nil
	}); err != nil {
		return err
	}
	if err := read("SELECT problem_id,case_no,input_data,expected_output,is_sample FROM nloj_go_problem_case ORDER BY problem_id,case_no", func(row *sql.Rows) error {
		var id int
		var c testCase
		if err := row.Scan(&id, &c.ID, &c.Input, &c.Output, &c.Sample); err != nil {
			return err
		}
		if id < 1 || id > len(s.problems) {
			return fmt.Errorf("invalid case parent %d", id)
		}
		p := &s.problems[id-1]
		p.Cases = append(p.Cases, c)
		return nil
	}); err != nil {
		return err
	}
	var pending []job
	if err := read(`SELECT id,user_id,problem_id,language,CASE WHEN status IN ('PENDING','JUDGING') THEN source_code ELSE '' END,created,status,time_used,memory_used,judge_info,
	CASE WHEN status IN ('PENDING','JUDGING') THEN problem_snapshot END FROM nloj_go_submission ORDER BY id`, func(row *sql.Rows) error {
		var sub submission
		var raw []byte
		if err := row.Scan(&sub.ID, &sub.User, &sub.Problem, &sub.Language, &sub.Code, &sub.Created, &sub.Status, &sub.Time, &sub.Memory, &sub.Info, &raw); err != nil {
			return err
		}
		if sub.ID != len(s.submissions)+1 {
			return fmt.Errorf("non-contiguous submission id %d", sub.ID)
		}
		if raw != nil {
			var p diskProblem
			if err := json.Unmarshal(raw, &p); err != nil {
				return err
			}
			p.problem.Extra = p.ExtraCode
			sub.Status = "PENDING"
			pending = append(pending, job{sub, p.problem})
		}
		sub.Code = ""
		s.submissions = append(s.submissions, sub)
		return nil
	}); err != nil {
		return err
	}
	// SQL-backed lists and the recovered queue must agree after an interrupted judge run.
	if _, err := db.Exec("UPDATE nloj_go_submission SET status='PENDING' WHERE status='JUDGING'"); err != nil {
		return err
	}
	if len(pending) > cap(s.jobs) {
		s.jobs = make(chan job, len(pending))
	}
	for _, j := range pending {
		s.jobs <- j
	}
	return nil
}
