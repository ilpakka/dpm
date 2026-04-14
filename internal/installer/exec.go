package installer

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
)

// runWithLogger ajaa cmd:n niin että stdout/stderr menee logger.Printf:lle
// rivi kerrallaan. Ei kirjoita os.Stdoutiin → ei riko TUI:ta.
func runWithLogger(cmd *exec.Cmd, logger Logger) error {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("runWithLogger: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("runWithLogger: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("runWithLogger: start %q: %w", cmd.Path, err)
	}

	done := make(chan struct{}, 2)
	go scanToLogger(stdoutPipe, logger, done)
	go scanToLogger(stderrPipe, logger, done)
	<-done
	<-done

	return cmd.Wait()
}

func scanToLogger(r io.Reader, logger Logger, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		logger.Printf("%s", sc.Text())
	}
}
