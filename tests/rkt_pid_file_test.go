// Copyright 2015 The rkt Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coreos/rkt/Godeps/_workspace/src/github.com/ThomasRooney/gexpect"
)

func preparePidFileRace(t *testing.T, ctx *rktRunCtx) (*gexpect.ExpectSubprocess, *gexpect.ExpectSubprocess, string, string) {
	// Start the pod
	runCmd := fmt.Sprintf("%s --debug --insecure-skip-verify run -interactive ./rkt-inspect-sleep.aci", ctx.cmd())
	t.Logf("%s", runCmd)
	runChild, err := gexpect.Spawn(runCmd)
	if err != nil {
		t.Fatalf("Cannot exec rkt")
	}

	err = expectWithOutput(runChild, "Enter text:")
	if err != nil {
		t.Fatalf("Waited for the prompt but not found: %v", err)
	}

	// Check the ppid file is really created
	cmd := fmt.Sprintf(`%s list --full|grep running`, ctx.cmd())
	output, err := exec.Command("/bin/sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("Couldn't list the pods: %v", err)
	}
	UUID := strings.Split(string(output), "\t")[0]

	pidFileName := filepath.Join(ctx.dataDir(), "pods/run", UUID, "ppid")
	if _, err := os.Stat(pidFileName); err != nil {
		t.Fatalf("Pid file missing: %v", err)
	}

	// Temporarily move the ppid file away
	pidFileNameBackup := pidFileName + ".backup"
	if err := os.Rename(pidFileName, pidFileNameBackup); err != nil {
		t.Fatalf("Cannot move ppid file away: %v", err)
	}

	// Start the "enter" command without the pidfile
	enterCmd := fmt.Sprintf("%s --debug enter %s /inspect --print-msg=RktEnterWorksFine", ctx.cmd(), UUID)
	t.Logf("%s", enterCmd)
	enterChild, err := gexpect.Spawn(enterCmd)
	if err != nil {
		t.Fatalf("Cannot exec rkt enter")
	}
	// Enter should be able to wait until the ppid file appears
	time.Sleep(1 * time.Second)

	return runChild, enterChild, pidFileName, pidFileNameBackup
}

// Check that "enter" is able to wait for the ppid file to be created
func TestPidFileDelayedStart(t *testing.T) {
	patchTestACI("rkt-inspect-sleep.aci", "--exec=/inspect --read-stdin")
	defer os.Remove("rkt-inspect-sleep.aci")

	ctx := newRktRunCtx()
	defer ctx.cleanup()

	runChild, enterChild, pidFileName, pidFileNameBackup := preparePidFileRace(t, ctx)

	// Restore ppid file so the "enter" command can find it
	if err := os.Rename(pidFileNameBackup, pidFileName); err != nil {
		t.Fatalf("Cannot restore ppid file: %v", err)
	}

	// Now the "enter" command works and can complete
	if err := expectWithOutput(enterChild, "RktEnterWorksFine"); err != nil {
		t.Fatalf("Waited for enter to works but failed: %v", err)
	}
	if err := enterChild.Wait(); err != nil {
		t.Fatalf("rkt enter didn't terminate correctly: %v", err)
	}

	// Terminate the pod
	if err := runChild.SendLine("Bye"); err != nil {
		t.Fatalf("rkt couldn't write to the container: %v", err)
	}
	if err := expectWithOutput(runChild, "Received text: Bye"); err != nil {
		t.Fatalf("Expected Bye but not found: %v", err)
	}
	if err := runChild.Wait(); err != nil {
		t.Fatalf("rkt didn't terminate correctly: %v", err)
	}
}

// Check that "enter" doesn't wait forever for the ppid file when the pod is terminated
func TestPidFileAbortedStart(t *testing.T) {
	patchTestACI("rkt-inspect-sleep.aci", "--exec=/inspect --read-stdin")
	defer os.Remove("rkt-inspect-sleep.aci")

	ctx := newRktRunCtx()
	defer ctx.cleanup()

	runChild, enterChild, _, _ := preparePidFileRace(t, ctx)

	// Terminate the pod with the escape sequence: ^]^]^]
	if err := runChild.SendLine("\035\035\035"); err != nil {
		t.Fatalf("Failed to terminate the pod: %v", err)
	}
	if err := runChild.Wait(); err.Error() != "exit status 1" {
		t.Fatalf("rkt didn't terminate as expected: %v", err)
	}

	// Now the "enter" command terminates quickly
	before := time.Now()
	if err := enterChild.Wait(); err.Error() != "exit status 1" {
		t.Fatalf("rkt enter didn't terminate as expected: %v", err)
	}
	delay := time.Now().Sub(before)
	t.Logf("rkt enter terminated %v after the pod was terminated", delay)
	if delay > time.Second { // 1 second shall be enough: it takes less than 50ms on my computer
		t.Fatalf("rkt enter didn't terminate quickly enough: %v", delay)
	}

}
