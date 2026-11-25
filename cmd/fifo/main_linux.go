package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func fifo(path string) (*os.File, error) {
	for {
		fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if errors.Is(err, syscall.ENOENT) {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if err != nil {
			return nil, err
		}
		var st syscall.Stat_t
		err = syscall.Fstat(fd, &st)
		if err == nil && st.Mode&syscall.S_IFMT != syscall.S_IFIFO {
			err = errors.New("not a FIFO")
		}
		if err == nil {
			err = syscall.SetNonblock(fd, false)
		}
		if err != nil {
			syscall.Close(fd)
			return nil, err
		}
		return os.NewFile(uintptr(fd), path), nil
	}
}

func main() {
	mode := os.Args[1]
	if mode != "solution" {
		paths := []string{"/alice/in", "/alice/out"}
		if mode == "manager" {
			paths = append(paths, "/bob/in", "/bob/out")
		}
		for _, p := range paths {
			if err := syscall.Mkfifo(p, 0666); err != nil && !errors.Is(err, syscall.EEXIST) {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(125)
			}
		}
	}
	args := []string{}
	switch mode {
	case "interactor":
		args = []string{"in.txt"}
	case "manager":
		args = []string{"in.txt", "/alice/in", "/alice/out", "/bob/in", "/bob/out"}
	}
	cmd := exec.Command("./program", args...)
	if mode == "solution" || mode == "interactor" {
		in, out := "/alice/in", "/alice/out"
		if mode == "interactor" {
			in, out = out, in
		}
		stdin, err := fifo(in)
		if err != nil {
			os.Exit(125)
		}
		defer stdin.Close()
		stdout, err := fifo(out)
		if err != nil {
			os.Exit(125)
		}
		defer stdout.Close()
		cmd.Stdin, cmd.Stdout = stdin, stdout
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			if exit.ExitCode() < 0 {
				os.Exit(124)
			}
			if exit.ExitCode() >= 125 && exit.ExitCode() <= 127 {
				os.Exit(124)
			}
			os.Exit(exit.ExitCode())
		}
		os.Exit(125)
	}
}
