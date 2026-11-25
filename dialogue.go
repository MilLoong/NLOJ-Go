package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func splitCommunication(code string) (string, string, bool) {
	const alice = "===NLOJ_FILE:alice==="
	const bob = "===NLOJ_FILE:bob==="
	if strings.Count(code, alice) != 1 || strings.Count(code, bob) != 1 {
		return "", "", false
	}
	a, b, _ := strings.Cut(code, alice)
	p1, p2, ok := strings.Cut(b, bob)
	if ok && strings.TrimSpace(a) == "" {
		return p1, p2, strings.TrimSpace(p1) != "" && strings.TrimSpace(p2) != ""
	}
	first, b, _ := strings.Cut(code, bob)
	p2, p1, ok = strings.Cut(b, alice)
	return p1, p2, ok && strings.TrimSpace(first) == "" && strings.TrimSpace(p1) != "" && strings.TrimSpace(p2) != ""
}

func (d dockerSandbox) runDialogue(ctx context.Context, sub submission, p problem) ([]caseRun, string) {
	if sub.Language != "C" && sub.Language != "CPP" {
		return nil, "CE"
	}
	a, b := sub.Code, ""
	mode := "interactor"
	if p.Type == "COMMUNICATION" {
		var ok bool
		a, b, ok = splitCommunication(sub.Code)
		if !ok {
			return nil, "CE"
		}
		mode = "manager"
	}
	if strings.TrimSpace(p.Extra) == "" {
		return nil, "SYSTEM_ERROR"
	}
	dirs := make([]string, 0, 3)
	defer func() {
		for _, dir := range dirs {
			os.RemoveAll(dir)
		}
	}()
	compile := func(lang, source string) (string, string) {
		dir, err := os.MkdirTemp("", "nloj-dialogue-")
		if err != nil {
			return "", "SYSTEM_ERROR"
		}
		dirs = append(dirs, dir)
		spec := languages[lang]
		if strings.ContainsAny(dir, ",\r\n") || os.WriteFile(filepath.Join(dir, spec.source), []byte(source), 0600) != nil {
			return "", "SYSTEM_ERROR"
		}
		r := d.container(ctx, dir, "", spec.build, 60*time.Second, 1<<20, true)
		switch r.Status {
		case "OK":
			return dir, "OK"
		case "RE", "TLE":
			return "", "CE"
		default:
			return "", "SYSTEM_ERROR"
		}
	}
	alice, status := compile(sub.Language, a)
	if status != "OK" {
		return nil, status
	}
	var bob string
	if mode == "manager" {
		bob, status = compile(sub.Language, b)
		if status != "OK" {
			return nil, status
		}
	}
	judge, status := compile("CPP", p.Extra)
	if status != "OK" {
		return nil, "SYSTEM_ERROR"
	}
	runs := make([]caseRun, 0, len(p.Cases))
	for _, c := range p.Cases {
		if os.WriteFile(filepath.Join(judge, "in.txt"), []byte(c.Input), 0600) != nil {
			return nil, "SYSTEM_ERROR"
		}
		r := d.dialogueCase(ctx, p, mode, judge, alice, bob)
		runs = append(runs, r)
		if r.Status != "OK" {
			break
		}
	}
	return runs, "OK"
}

func (d dockerSandbox) dialogueCase(ctx context.Context, p problem, mode, judge, alice, bob string) caseRun {
	var names []string
	defer func() {
		for _, name := range names {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			exec.CommandContext(cleanup, d.binary, "volume", "rm", name).Run()
			cancel()
		}
	}()
	volume := func() string {
		var nonce [16]byte
		rand.Read(nonce[:])
		name := "nloj-channel-" + hex.EncodeToString(nonce[:])
		names = append(names, name)
		create, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if exec.CommandContext(create, d.binary, "volume", "create", name).Run() != nil {
			return ""
		}
		return name
	}
	mount := func(name, dir string, readonly bool) string {
		s := "type=volume,source=" + name + ",target=" + dir + ",volume-nocopy"
		if readonly {
			s += ",readonly"
		}
		return s
	}
	first := volume()
	if first == "" {
		return caseRun{Status: "SYSTEM_ERROR"}
	}
	judgeMounts := []string{mount(first, "/alice", false)}
	var second string
	if bob != "" {
		second = volume()
		if second == "" {
			return caseRun{Status: "SYSTEM_ERROR"}
		}
		judgeMounts = append(judgeMounts, mount(second, "/bob", false))
	}
	limit := time.Duration(p.Time) * time.Millisecond
	judgeDone := make(chan caseRun, 1)
	aliceDone := make(chan caseRun, 1)
	go func() {
		judgeDone <- d.containerWithMounts(ctx, judge, "", []string{"/usr/local/bin/nloj-fifo", mode}, limit, 256<<10, false, judgeMounts)
	}()
	go func() {
		aliceDone <- d.containerWithMounts(ctx, alice, "", []string{"/usr/local/bin/nloj-fifo", "solution"}, limit, p.Memory, false, []string{mount(first, "/alice", true)})
	}()
	var bobResult caseRun
	if bob != "" {
		bobResult = d.containerWithMounts(ctx, bob, "", []string{"/usr/local/bin/nloj-fifo", "solution"}, limit, p.Memory, false, []string{mount(second, "/alice", true)})
	}
	a, j := <-aliceDone, <-judgeDone
	if bob == "" {
		bobResult = a
	}
	r := caseRun{Status: "OK", Time: max(a.Time, bobResult.Time), Memory: -1}
	switch {
	case a.Status == "SYSTEM_ERROR" || bobResult.Status == "SYSTEM_ERROR" || j.Status == "SYSTEM_ERROR":
		r.Status = "SYSTEM_ERROR"
	case a.Status == "TLE" || bobResult.Status == "TLE" || j.Status == "TLE":
		r.Status = "TLE"
	case a.Status == "MLE" || bobResult.Status == "MLE":
		r.Status = "MLE"
	case j.Status == "MLE":
		r.Status = "SYSTEM_ERROR"
	case a.Status != "OK" || bobResult.Status != "OK":
		r.Status = "RE"
	case j.Status != "OK":
		r.Status = "WA"
	}
	return r
}
