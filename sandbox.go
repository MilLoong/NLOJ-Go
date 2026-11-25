package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const outputLimit = 1 << 20

type limitedOutput struct {
	buf      bytes.Buffer
	overflow bool
}

func (o *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if n > outputLimit-o.buf.Len() {
		o.overflow = true
		p = p[:outputLimit-o.buf.Len()]
	}
	o.buf.Write(p)
	return n, nil
}

type languageSpec struct {
	source         string
	build, execute []string
}

var languages = map[string]languageSpec{
	"C":      {"main.c", []string{"gcc", "-O2", "-std=c17", "main.c", "-o", "program"}, []string{"./program"}},
	"CPP":    {"main.cpp", []string{"g++", "-O2", "-std=c++17", "main.cpp", "-o", "program"}, []string{"./program"}},
	"PYTHON": {"main.py", []string{"python3", "-m", "py_compile", "main.py"}, []string{"python3", "-B", "main.py"}},
	"JAVA":   {"Main.java", []string{"javac", "Main.java"}, []string{"java", "-XX:ActiveProcessorCount=1", "-cp", ".", "Main"}},
	"GO":     {"main.go", []string{"go", "build", "-o", "program", "main.go"}, []string{"./program"}},
}

type dockerSandbox struct {
	binary, image string
}

func (d dockerSandbox) run(ctx context.Context, sub submission, p problem) ([]caseRun, string) {
	ctx, cancel := context.WithTimeout(ctx, judgeDeadline)
	defer cancel()
	if p.Type == "INTERACTIVE" || p.Type == "COMMUNICATION" {
		return d.runDialogue(ctx, sub, p)
	}
	lang, ok := languages[sub.Language]
	if !ok || p.Type != "STANDARD" || (p.Mode != "EXACT" && p.Mode != "SPJ") {
		return nil, "SYSTEM_ERROR"
	}
	dir, err := os.MkdirTemp("", "nloj-job-")
	if err != nil {
		return nil, "SYSTEM_ERROR"
	}
	defer os.RemoveAll(dir)
	if strings.ContainsAny(dir, ",\r\n") {
		return nil, "SYSTEM_ERROR"
	}
	if err := os.WriteFile(filepath.Join(dir, lang.source), []byte(sub.Code), 0644); err != nil {
		return nil, "SYSTEM_ERROR"
	}
	build := d.container(ctx, dir, "", lang.build, 60*time.Second, 1<<20, true)
	switch build.Status {
	case "OK":
	case "RE", "TLE":
		return nil, "CE"
	default:
		return nil, "SYSTEM_ERROR"
	}
	var checker string
	if p.Mode == "SPJ" {
		checker, err = os.MkdirTemp("", "nloj-checker-")
		if err != nil {
			return nil, "SYSTEM_ERROR"
		}
		defer os.RemoveAll(checker)
		if strings.ContainsAny(checker, ",\r\n") ||
			os.WriteFile(filepath.Join(checker, "main.cpp"), []byte(p.Extra), 0600) != nil ||
			d.container(ctx, checker, "", languages["CPP"].build, 60*time.Second, 1<<20, true).Status != "OK" {
			return nil, "SYSTEM_ERROR"
		}
	}
	runs := make([]caseRun, 0, len(p.Cases))
	for _, c := range p.Cases {
		r := d.container(ctx, dir, c.Input, lang.execute, time.Duration(p.Time)*time.Millisecond, p.Memory, false)
		if r.Status == "OK" && checker != "" {
			r.Status = d.check(ctx, checker, c, r.Stdout)
		}
		runs = append(runs, r)
		if r.Status != "OK" || (checker == "" && normalizeOutput(r.Stdout) != normalizeOutput(c.Output)) {
			break
		}
	}
	return runs, "OK"
}

func (d dockerSandbox) check(ctx context.Context, dir string, c testCase, stdout string) string {
	for name, data := range map[string]string{"in.txt": c.Input, "out.txt": stdout, "ans.txt": c.Output} {
		if os.WriteFile(filepath.Join(dir, name), []byte(data), 0600) != nil {
			return "SYSTEM_ERROR"
		}
	}
	r := d.container(ctx, dir, "", []string{"./program", "in.txt", "out.txt", "ans.txt"}, 5*time.Second, 256<<10, false)
	if r.Status == "RE" {
		return "WA"
	}
	if r.Status != "OK" {
		return "SYSTEM_ERROR"
	}
	return "OK"
}

func (d dockerSandbox) container(parent context.Context, dir, input string, argv []string, limit time.Duration, memory int, build bool) caseRun {
	return d.containerWithMounts(parent, dir, input, argv, limit, memory, build, nil)
}

func (d dockerSandbox) containerWithMounts(parent context.Context, dir, input string, argv []string, limit time.Duration, memory int, build bool, mounts []string) caseRun {
	var nonce [16]byte
	rand.Read(nonce[:])
	name := "nloj-" + hex.EncodeToString(nonce[:])
	mount := "type=bind,source=" + dir + ",target=/work"
	if !build {
		mount += ",readonly"
	}
	args := []string{"run", "--pull=never", "--name", name, "--network=none",
		"--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=64",
		"--cpus=1", "--memory=" + strconv.Itoa(memory) + "k", "--memory-swap=" + strconv.Itoa(memory) + "k",
		"--tmpfs", "/tmp:rw,nosuid,nodev,size=128m", "--ulimit", "fsize=67108864:67108864",
		"--log-driver=none", "--mount", mount, "--workdir=/work", "-i",
		"--env", "HOME=/tmp", "--env", "GOCACHE=/tmp/go-build", "--env", "GOPROXY=off", "--env", "GOTOOLCHAIN=local",
	}
	for _, mount := range mounts {
		args = append(args, "--mount", mount)
	}
	args = append(args, "--entrypoint", argv[0], d.image)
	args = append(args, argv[1:]...)
	ctx, cancel := context.WithTimeout(parent, limit)
	defer cancel()
	started := time.Now()
	var out limitedOutput
	cmd := exec.CommandContext(ctx, d.binary, args...)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		exec.CommandContext(cleanup, d.binary, "rm", "-f", name).Run()
	}()
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout = &out
	err := cmd.Run()
	elapsed := int(time.Since(started).Milliseconds())
	res := caseRun{Status: "OK", Stdout: out.buf.String(), Time: elapsed, Memory: -1}
	if err == nil && !out.overflow {
		return res
	}
	if ctx.Err() != nil {
		if parent.Err() != nil {
			res.Status = "SYSTEM_ERROR"
		} else {
			res.Status = "TLE"
		}
		return res
	}
	res.Status = "SYSTEM_ERROR"
	var failure *exec.ExitError
	if errors.As(err, &failure) && failure.ExitCode() == 137 {
		probe, stop := context.WithTimeout(parent, 5*time.Second)
		state, inspectErr := exec.CommandContext(probe, d.binary, "inspect", "--format={{.State.OOMKilled}}", name).Output()
		stop()
		if inspectErr == nil && strings.TrimSpace(string(state)) == "true" {
			res.Status = "MLE"
			return res
		}
	}
	if out.overflow || (errors.As(err, &failure) && failure.ExitCode() != 125 && failure.ExitCode() != 126 && failure.ExitCode() != 127) {
		res.Status = "RE"
	}
	return res
}
