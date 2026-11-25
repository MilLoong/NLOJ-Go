package judge

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestOutputLimit(t *testing.T) {
	var buf limitedOutput
	data := strings.Repeat("x", outputLimit+15)
	n, err := buf.Write([]byte(data))
	if err != nil || n != len(data) || !buf.overflow || buf.buf.Len() != outputLimit {
		t.Fatalf("n=%d, err=%v, overflow=%v, retained=%d", n, err, buf.overflow, buf.buf.Len())
	}
}

func TestDockerSandbox(t *testing.T) {
	image := os.Getenv("NLOJ_TEST_JUDGE_IMAGE")
	if image == "" {
		t.Skip("set NLOJ_TEST_JUDGE_IMAGE to an existing local judge image")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	d := DockerSandbox{Binary: docker, Image: image}
	p := problem{Type: "STANDARD", Mode: "EXACT", Time: 10000, Memory: 256 << 10, Cases: []testCase{{Input: "10 32\n", Output: "42\n"}}}
	sub := submission{Language: "PYTHON", Code: "print(sum(map(int,input().split())))"}
	runs, status := d.Run(context.Background(), sub, p)
	if status != "OK" || exactVerdict(p.Cases, runs).Status != "AC" {
		t.Fatalf("EXACT: status=%s runs=%+v", status, runs)
	}
	p.Mode = "SPJ"
	p.Cases[0].Output = "not an exact match"
	p.Extra = `#include <fstream>
int main(int argc, char** argv) {
    int x = 0;
    if (argc != 4 || !(std::ifstream(argv[2]) >> x)) return 1;
    return x == 42 ? 0 : 1;
}`
	runs, status = d.Run(context.Background(), sub, p)
	if status != "OK" || Verdict(p.Cases, runs, false).Status != "AC" {
		t.Fatalf("SPJ: status=%s runs=%+v", status, runs)
	}

	p.Type, p.Mode = "INTERACTIVE", "EXACT"
	p.Extra = `#include <fstream>
#include <iostream>
int main(int argc, char** argv) {
    int x, y;
    if (argc != 2 || !(std::ifstream(argv[1]) >> x)) return 1;
    std::cout << x << std::endl;
    return std::cin >> y && y == x + 1 ? 0 : 1;
}`
	sub = submission{Language: "CPP", Code: `#include <iostream>
int main() { int x; if (!(std::cin >> x)) return 1; std::cout << x + 1 << std::endl; }`}
	runs, status = d.Run(context.Background(), sub, p)
	if status != "OK" || Verdict(p.Cases, runs, false).Status != "AC" {
		t.Fatalf("INTERACTIVE: status=%s runs=%+v", status, runs)
	}

	p.Type = "COMMUNICATION"
	p.Extra = `#include <fstream>
int main(int argc, char** argv) {
    if (argc != 6) return 1;
    std::ofstream ai(argv[2]), bi(argv[4]);
    std::ifstream ao(argv[3]), bo(argv[5]);
    ai << 10 << std::endl; bi << 20 << std::endl;
    int a, b;
    return ao >> a && bo >> b && a == 11 && b == 21 ? 0 : 1;
}`
	sub.Code = "===NLOJ_FILE:alice===\n" + sub.Code + "\n===NLOJ_FILE:bob===\n" + sub.Code
	runs, status = d.Run(context.Background(), sub, p)
	if status != "OK" || Verdict(p.Cases, runs, false).Status != "AC" {
		t.Fatalf("COMMUNICATION: status=%s runs=%+v", status, runs)
	}
}
