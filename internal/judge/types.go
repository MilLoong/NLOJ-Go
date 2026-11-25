package judge

type TestCase struct {
	ID     int    `json:"id"`
	Input  string `json:"input"`
	Output string `json:"output"`
	Sample bool   `json:"sample,omitempty"`
}

type Result struct {
	Status string  `json:"status"`
	Time   *int    `json:"timeUsed"`
	Memory *int    `json:"memoryUsed"`
	Info   *string `json:"judgeInfo"`
}

type Submission struct {
	Language, Code string
}

type Problem struct {
	Type, Mode, Extra string
	Time, Memory      int
	Cases             []TestCase
}

type testCase = TestCase
type caseRun = CaseRun
type result = Result
type submission = Submission
type problem = Problem
