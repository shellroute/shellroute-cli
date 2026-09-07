//go:build !windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// exitAsChild and exitFromSignal end the process, so each case runs in a
// re-executed test binary driven by TestHelperProcess.

func TestHelperProcess(t *testing.T) {
	switch os.Getenv("SR_TEST_HELPER") {
	case "":
		return
	case "exit-as-child":
		var cmd *exec.Cmd
		if code := os.Getenv("SR_TEST_CHILD_EXIT"); code != "" {
			cmd = exec.Command("bash", "-c", "exit "+code)
		} else {
			cmd = exec.Command("sleep", "30")
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			fmt.Println(err)
			os.Exit(99)
		}
		if name := os.Getenv("SR_TEST_CHILD_SIG"); name != "" {
			time.Sleep(200 * time.Millisecond)
			syscall.Kill(-cmd.Process.Pid, signalByName(name))
		}
		err := cmd.Wait()
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitAsChild(ee)
		}
		if err != nil {
			fmt.Println(err)
			os.Exit(99)
		}
		os.Exit(0)
	case "exit-from-signal":
		exitFromSignal(signalByName(os.Getenv("SR_TEST_SIG")))
	}
}

func signalByName(name string) syscall.Signal {
	switch name {
	case "INT":
		return syscall.SIGINT
	case "TERM":
		return syscall.SIGTERM
	case "HUP":
		return syscall.SIGHUP
	case "KILL":
		return syscall.SIGKILL
	case "USR1":
		return syscall.SIGUSR1
	}
	panic("unknown signal " + name)
}

type helperResult struct {
	code int // exit status, or -1 when killed by a signal
	sig  syscall.Signal
	out  string
}

// runHelper re-executes the test binary running only TestHelperProcess.
// With hupIgnored, SIGHUP is ignored on entry, as under nohup.
func runHelper(t *testing.T, hupIgnored bool, env ...string) helperResult {
	t.Helper()
	var cmd *exec.Cmd
	if hupIgnored {
		cmd = exec.Command("bash", "-c", `trap '' HUP; exec "$0" -test.run='^TestHelperProcess$'`, os.Args[0])
	} else {
		cmd = exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	}
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	res := helperResult{out: string(out)}
	if err == nil {
		return res
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("helper: %v", err)
	}
	ws := ee.Sys().(syscall.WaitStatus)
	if ws.Signaled() {
		res.code, res.sig = -1, ws.Signal()
	} else {
		res.code = ws.ExitStatus()
	}
	return res
}

func TestExitAsChild(t *testing.T) {
	cases := []struct {
		name     string
		env      []string
		wantCode int
		wantSig  syscall.Signal
	}{
		{"exit code kept", []string{"SR_TEST_CHILD_EXIT=7"}, 7, 0},
		{"SIGTERM re-raised", []string{"SR_TEST_CHILD_SIG=TERM"}, -1, syscall.SIGTERM},
		{"SIGINT re-raised", []string{"SR_TEST_CHILD_SIG=INT"}, -1, syscall.SIGINT},
		{"SIGHUP re-raised", []string{"SR_TEST_CHILD_SIG=HUP"}, -1, syscall.SIGHUP},
		{"SIGKILL re-raised", []string{"SR_TEST_CHILD_SIG=KILL"}, -1, syscall.SIGKILL},
		{"other signal uses 128+n", []string{"SR_TEST_CHILD_SIG=USR1"}, 128 + int(syscall.SIGUSR1), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := runHelper(t, false, append(c.env, "SR_TEST_HELPER=exit-as-child")...)
			if res.code != c.wantCode || res.sig != c.wantSig {
				t.Errorf("got code=%d sig=%v, want code=%d sig=%v\n%s", res.code, res.sig, c.wantCode, c.wantSig, res.out)
			}
		})
	}
}

// exitFromSignal must not re-enable a signal that is ignored on entry: with
// SIGHUP ignored (nohup), it falls back to exit status 129.
func TestExitFromSignal_IgnoredFallsBack(t *testing.T) {
	res := runHelper(t, true, "SR_TEST_HELPER=exit-from-signal", "SR_TEST_SIG=HUP")
	if res.code != 129 || res.sig != 0 {
		t.Errorf("got code=%d sig=%v, want code=129\n%s", res.code, res.sig, res.out)
	}

	res = runHelper(t, false, "SR_TEST_HELPER=exit-from-signal", "SR_TEST_SIG=HUP")
	if res.sig != syscall.SIGHUP {
		t.Errorf("got code=%d sig=%v, want killed by SIGHUP\n%s", res.code, res.sig, res.out)
	}
}
