package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

// step sends keys after a pause; marker records the output offset reached
// just before the keys were sent (a point where screens are compared).
type step struct {
	pause time.Duration
	keys  string
}

type recording struct {
	name    string
	data    []byte
	markers []int
}

// record runs argv in an 80x24 PTY, plays the steps and captures output.
func record(name string, argv []string, steps []step) recording {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LANG=en_US.UTF-8")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		panic(err)
	}
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	done := make(chan struct{})
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := ptmx.Read(b)
			mu.Lock()
			buf.Write(b[:n])
			mu.Unlock()
			if err != nil {
				close(done)
				return
			}
		}
	}()
	rec := recording{name: name}
	for _, s := range steps {
		time.Sleep(s.pause)
		mu.Lock()
		rec.markers = append(rec.markers, buf.Len())
		mu.Unlock()
		_, _ = io.WriteString(ptmx, s.keys)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	_ = cmd.Wait()
	rec.data = buf.Bytes()
	return rec
}

func recordAll(dir string) []recording {
	sample := dir + "/sample.txt"
	var b bytes.Buffer
	for i := range 300 {
		b.WriteString("line ")
		b.WriteString(time.Duration(i * int(time.Second)).String())
		b.WriteString(" — the quick brown fox jumps over the lazy dog 日本語 🐝\n")
	}
	_ = os.WriteFile(sample, b.Bytes(), 0o644)
	p := 400 * time.Millisecond
	return []recording{
		record("vim", []string{"vim", "-u", "NONE", "-N", "-i", "NONE", "-n", sample}, []step{
			{p, "50%"}, {p, ":set number\r"}, {p, "Ohello from hive\x1b"}, {p, "G"}, {p, "gg"}, {p, ":%s/fox/cat/g\r"}, {p, "\x06\x06"}, {p, ":q!\r"},
		}),
		record("less", []string{"less", "-R", sample}, []step{
			{p, " "}, {p, " "}, {p, "/lazy\r"}, {p, "n"}, {p, "G"}, {p, "g"}, {p, "q"},
		}),
		// top is recorded for the spike comparison but not committed to the
		// corpus: it shows the processes of the machine that recorded it.
		record("top", []string{"top"}, []step{
			{1500 * time.Millisecond, ""}, {1200 * time.Millisecond, "o"}, {p, "cpu\r"}, {1200 * time.Millisecond, "q"},
		}),
		record("shell-colors", []string{"sh", "-c", `for i in $(seq 1 60); do printf '\033[38;5;%dm%03d \033[1mbold\033[0m \033[48;2;20;20;80m truecolor \033[0m 日本 %s\n' $((i%256)) $i "$(printf '%*s' $((i%30)) '' | tr ' ' '#')"; done; printf '\033[?1049h\033[2J\033[5;10Hin alt\033[?1049l'; sleep 1`}, []step{
			{1500 * time.Millisecond, ""},
		}),
	}
}
