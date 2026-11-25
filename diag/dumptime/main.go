// Command dumptime is a throwaway diagnostic: it times when headless Chrome's --dump-dom output arrives and when the process exits.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

func main() {
	const chrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	n := 120
	var dumps, exits []float64
	var bad, noDOM, killed int
	for i := 0; i < n; i++ {
		dir, _ := os.MkdirTemp("", "dt")
		file := filepath.Join(dir, "p.html")
		os.WriteFile(file, []byte(`<!doctype html><pre id="probe">x</pre>`), 0o600)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		cmd := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--allow-file-access-from-files", "--user-data-dir="+filepath.Join(dir, "profile"), "--window-size=800,800", "--dump-dom", "file://"+file)
		out, _ := cmd.StdoutPipe()
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		start := time.Now()
		cmd.Start()
		var buf bytes.Buffer
		dump := -1.0
		chunk := make([]byte, 4096)
		for {
			k, err := out.Read(chunk)
			buf.Write(chunk[:k])
			if dump < 0 && bytes.Contains(buf.Bytes(), []byte("</html>")) {
				dump = time.Since(start).Seconds()
			}
			if err == io.EOF || err != nil {
				break
			}
		}
		err := cmd.Wait()
		exit := time.Since(start).Seconds()
		cancel()
		exits = append(exits, exit)
		if dump < 0 {
			noDOM++
		} else {
			dumps = append(dumps, dump)
		}
		if ctx.Err() != nil {
			killed++
		}
		if err != nil || exit > 8 {
			bad++
			tail := stderr.String()
			if len(tail) > 300 {
				tail = tail[len(tail)-300:]
			}
			fmt.Printf("call %d: dump at %.2fs, exit at %.2fs, err %v, stderr tail %q\n", i, dump, exit, err, tail)
		}
		os.RemoveAll(dir)
	}
	q := func(s []float64, p float64) float64 {
		if len(s) == 0 {
			return -1
		}
		sort.Float64s(s)
		return s[int(float64(len(s)-1)*p)]
	}
	fmt.Printf("n=%d dump p50=%.2f p90=%.2f p99=%.2f max=%.2f | exit p50=%.2f p90=%.2f p99=%.2f max=%.2f | errors-or-slow=%d noDOM=%d killed=%d\n",
		n, q(dumps, .5), q(dumps, .9), q(dumps, .99), q(dumps, 1), q(exits, .5), q(exits, .9), q(exits, .99), q(exits, 1), bad, noDOM, killed)
}
