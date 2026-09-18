package test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperatorUpgradeScript(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "webhook-retry"
		if fail {
			name = "readiness-failure"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			mock := `#!/bin/bash
echo "$(basename "$0") $*" >> "$CALLS"
case "$(basename "$0")" in
sleep) exit 0 ;;
helm)
  if [[ "$*" == "list" ]]; then printf 'NAME NAMESPACE REVISION\nmy-release default 1\n'; fi
  if [[ "$*" == get\ metadata* ]]; then echo '{"version":"6.3.0"}'; fi
  ;;
jq) echo 6.3.0 ;;
kubectl)
  if [[ "$*" == *--dry-run=server* && ! -f "$CALLS.ready" ]]; then touch "$CALLS.ready"; exit 1; fi
  if [[ "$*" == "get pods" ]]; then printf 'NAME READY STATUS RESTARTS\nmy-release 1/1 Running 0\n'; fi
  if [[ "$*" == "--timeout 20m wait"* && "$FAIL_WAIT" == 1 ]]; then exit 7; fi
  ;;
esac
exit 0
`
			for _, binary := range []string{"kubectl", "helm", "sleep", "jq"} {
				if err := os.WriteFile(filepath.Join(dir, binary), []byte(mock), 0755); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("bash", "upgrade.sh")
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "CALLS="+filepath.Join(dir, "calls"), "FAIL_WAIT=0")
			if fail {
				cmd.Env = append(cmd.Env, "FAIL_WAIT=1")
			}
			output, err := cmd.CombinedOutput()
			calls, readErr := os.ReadFile(filepath.Join(dir, "calls"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			log := string(calls)
			if strings.Count(log, "kubectl apply --dry-run=server") != 2 {
				t.Fatalf("missing bounded webhook retry: %s", log)
			}
			if fail {
				if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
					t.Fatalf("lost original exit status: %v\n%s", err, output)
				}
				if !strings.Contains(log, "kubectl get events -A") || strings.Contains(log, "helm -n milvus-operator upgrade") {
					t.Fatalf("incorrect failure handling: %s", log)
				}
			} else if err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
		})
	}
}
