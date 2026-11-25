package judge

import (
	"fmt"
	"strings"
)

type CaseRun struct {
	Status       string
	Stdout       string
	Time, Memory int
}

func NormalizeOutput(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r", ""), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func exactVerdict(cases []testCase, runs []caseRun) result {
	return Verdict(cases, runs, true)
}

func Verdict(cases []TestCase, runs []CaseRun, compare bool) Result {
	if len(cases) == 0 || len(runs) > len(cases) {
		return result{Status: "SYSTEM_ERROR"}
	}
	peakTime, peakMemory := 0, -1
	for i, run := range runs {
		status := run.Status
		switch status {
		case "OK":
			if compare && NormalizeOutput(cases[i].Output) != NormalizeOutput(run.Stdout) {
				status = "WA"
			}
		case "WA", "TLE", "MLE", "RE":
		default:
			return result{Status: "SYSTEM_ERROR"}
		}
		if status != "OK" {
			info := fmt.Sprintf("%s on test case %d", status, i+1)
			res := result{Status: status, Time: &run.Time, Info: &info}
			if run.Memory >= 0 {
				res.Memory = &run.Memory
			}
			return res
		}
		peakTime, peakMemory = max(peakTime, run.Time), max(peakMemory, run.Memory)
	}
	if len(runs) != len(cases) {
		return result{Status: "SYSTEM_ERROR"}
	}
	info := fmt.Sprintf("All %d test cases passed", len(cases))
	res := result{Status: "AC", Time: &peakTime, Info: &info}
	if peakMemory >= 0 {
		res.Memory = &peakMemory
	}
	return res
}
