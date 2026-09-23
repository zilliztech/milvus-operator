package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLayeredConfig(t *testing.T) {
	root := t.TempDir()
	configs := filepath.Join(root, "configs")
	tools := filepath.Join(root, "tools")
	require.NoError(t, os.MkdirAll(filepath.Join(configs, "operator"), 0755))
	require.NoError(t, os.MkdirAll(tools, 0755))
	put := func(path, data string, mode os.FileMode) { require.NoError(t, os.WriteFile(path, []byte(data), mode)) }
	put(filepath.Join(configs, "milvus.yaml"), "log:\n  level: info\n", 0444)
	put(filepath.Join(configs, "hook.yaml"), "base: true\n", 0444)
	put(filepath.Join(configs, "operator/user.yaml"), "log:\n  level: debug\n", 0644)
	put(filepath.Join(tools, "iam-verify"), "#!/bin/sh\nexit 0\n", 0755)
	put(filepath.Join(tools, "merge"), "#!/bin/sh\nexit 99\n", 0755)
	script, err := os.ReadFile("run.sh")
	require.NoError(t, err)
	scriptPath := filepath.Join(root, "run.sh")
	put(scriptPath, strings.ReplaceAll(string(script), "/milvus", root), 0755)
	cmd := exec.Command("bash", scriptPath, "sh", "-c", `test -r "$MILVUSCONF/user.yaml" && test "$1" = 'with spaces'`, "sh", "with spaces")
	cmd.Env = append(os.Environ(), "MILVUS_OPERATOR_LAYERED_CONFIG=true")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	runtime := filepath.Join(tools, "runtime-config")
	target, err := os.Readlink(filepath.Join(runtime, "user.yaml"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(configs, "operator/user.yaml"), target)
	put(filepath.Join(configs, "operator/next.yaml"), "{}\n", 0644)
	require.NoError(t, os.Rename(filepath.Join(configs, "operator/next.yaml"), filepath.Join(configs, "operator/user.yaml")))
	data, err := os.ReadFile(filepath.Join(runtime, "user.yaml"))
	require.NoError(t, err)
	require.Equal(t, "{}\n", string(data))
	data, err = os.ReadFile(filepath.Join(configs, "milvus.yaml"))
	require.NoError(t, err)
	require.Equal(t, "log:\n  level: info\n", string(data))
	// Re-running startup must work and restore hook defaults without modifying the image.
	cmd = exec.Command("bash", scriptPath, "true")
	cmd.Env = append(os.Environ(), "MILVUS_OPERATOR_LAYERED_CONFIG=true")
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	// hook.yaml remains a startup merge, and failures must prevent Milvus startup.
	put(filepath.Join(configs, "operator/hook.yaml"), "override: true\n", 0644)
	cmd = exec.Command("bash", scriptPath, "true")
	cmd.Env = append(os.Environ(), "MILVUS_OPERATOR_LAYERED_CONFIG=true")
	output, err = cmd.CombinedOutput()
	require.Error(t, err, string(output))
	put(filepath.Join(tools, "merge"), "#!/bin/sh\nprintf 'override: true\\n' >> \"$4\"\n", 0755)
	cmd = exec.Command("bash", scriptPath, "true")
	cmd.Env = append(os.Environ(), "MILVUS_OPERATOR_LAYERED_CONFIG=true")
	output, err = cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	data, err = os.ReadFile(filepath.Join(runtime, "hook.yaml"))
	require.NoError(t, err)
	require.Equal(t, "base: true\noverride: true\n", string(data))
	data, err = os.ReadFile(filepath.Join(configs, "hook.yaml"))
	require.NoError(t, err)
	require.Equal(t, "base: true\n", string(data))
}
