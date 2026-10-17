package update

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRestartReservesOperationUntilExit(t *testing.T) {
	m := linuxManager(t, "v1.2.0", writeExe(t, "bin"))
	if !m.tryBegin(stateInstalling, "installing") {
		t.Fatal("cannot start installation")
	}
	if rr := call(t, m, http.MethodPost, "/api/v1/system/restart", ""); rr.Code != http.StatusConflict {
		t.Fatalf("restart during install: %d %s", rr.Code, rr.Body)
	}
	m.finish(stateDone, "installed", true)
	exited := make(chan int, 1)
	m.exit = func(code int) { exited <- code }
	m.restartWait = 20 * time.Millisecond
	if rr := call(t, m, http.MethodPost, "/api/v1/system/restart", ""); rr.Code != http.StatusOK {
		t.Fatalf("restart: %d %s", rr.Code, rr.Body)
	}
	for _, path := range []string{"update", "rollback", "restart"} {
		if rr := call(t, m, http.MethodPost, "/api/v1/system/"+path, ""); rr.Code != http.StatusConflict {
			t.Fatalf("%s during restart: %d %s", path, rr.Code, rr.Body)
		}
	}
	if snap := m.snapshot(); !snap.busy || snap.state != stateRestarting {
		t.Fatalf("restart did not reserve operation: %+v", snap)
	}
	select {
	case code := <-exited:
		if code != 0 {
			t.Fatalf("exit code %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("restart did not exit")
	}
}

// Execute a copy of this test binary so Linux can really rename the running
// executable without modifying the parent test runner or the application.
func TestRunningExecutableKeepsInstallTarget(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux running-executable rename semantics")
	}
	if os.Getenv("NODEPOOL_UPDATE_CHILD") != "1" {
		current, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		binary, err := os.ReadFile(current)
		if err != nil {
			t.Fatal(err)
		}
		child := filepath.Join(t.TempDir(), "nodepool")
		if err := os.WriteFile(child, binary, 0o755); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(child, "-test.run=^TestRunningExecutableKeepsInstallTarget$", "-test.v")
		cmd.Env = append(os.Environ(), "NODEPOOL_UPDATE_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child failed: %v\n%s", err, output)
		}
		return
	}
	m, err := New(Options{Version: "v1.2.0"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if m.exePath != original || !filepath.IsAbs(m.exePath) {
		t.Fatalf("target not fixed at startup: %s, running %s", m.exePath, original)
	}
	if err := m.installBinary(m.exePath, writeExe(t, "first-install")); err != nil {
		t.Fatal(err)
	}
	moved, err := os.Executable()
	if err != nil || moved != original+".backup" {
		t.Fatalf("running path did not move: %q %v", moved, err)
	}
	if !m.backupExists() {
		t.Fatal("backup missing after moving running executable")
	}
	if err := m.installBinary(m.exePath, writeExe(t, "second-install")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, original); got != "second-install" {
		t.Fatalf("second install missed startup path: %q", got)
	}
	if rr := call(t, m, http.MethodPost, "/api/v1/system/rollback", ""); rr.Code != http.StatusOK {
		t.Fatalf("rollback: %d %s", rr.Code, rr.Body)
	}
	if snap := waitTerminal(t, m); snap.state != stateDone || readFile(t, original) != "first-install" {
		t.Fatalf("rollback did not restore startup path: %+v", snap)
	}
}
