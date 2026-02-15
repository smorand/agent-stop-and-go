//go:build e2e

package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agent-stop-and-go/internal/mcp"
)

// mcpFilesystemBin returns the platform-specific mcp-filesystem binary path.
func mcpFilesystemBin() string {
	return fmt.Sprintf("./bin/mcp-filesystem-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// fsTestEnv holds the test environment for filesystem E2E tests.
type fsTestEnv struct {
	client    *mcp.HTTPClient
	rootDir   string
	sourceDir string
	destDir   string
}

// setupFSTest creates a test environment with mcp-filesystem server.
func setupFSTest(t *testing.T, port int, maxFullReadSize int64, extraRoots ...string) *fsTestEnv {
	t.Helper()

	// Create temp directories for roots
	tmpDir := t.TempDir()
	rootDir := filepath.Join(tmpDir, "workspace")
	os.MkdirAll(rootDir, 0755)

	sourceDir := filepath.Join(tmpDir, "source")
	os.MkdirAll(sourceDir, 0755)

	destDir := filepath.Join(tmpDir, "dest")
	os.MkdirAll(destDir, 0755)

	readonlyDir := filepath.Join(tmpDir, "readonly")
	os.MkdirAll(readonlyDir, 0755)

	restrictedDir := filepath.Join(tmpDir, "restricted")
	os.MkdirAll(restrictedDir, 0755)

	if maxFullReadSize == 0 {
		maxFullReadSize = 1048576
	}

	// Build config
	cfgContent := fmt.Sprintf(`host: 0.0.0.0
port: %d
max_full_read_size: %d
roots:
  - name: workspace
    path: %s
    allowed_tools:
      - "*"
  - name: source
    path: %s
    allowed_tools:
      - "*"
  - name: dest
    path: %s
    allowed_tools:
      - "*"
  - name: readonly
    path: %s
    allowed_tools:
      - "list_folder"
      - "read_file"
      - "stat_file"
      - "grep"
      - "glob"
  - name: restricted
    path: %s
    allowed_tools:
      - "list_folder"
`, port, maxFullReadSize, rootDir, sourceDir, destDir, readonlyDir, restrictedDir)

	cfgFile := filepath.Join(tmpDir, "config.yaml")
	os.WriteFile(cfgFile, []byte(cfgContent), 0644)

	// Start server
	cmd := exec.Command(mcpFilesystemBin(), "--config", cfgFile)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start mcp-filesystem: %v", err)
	}

	mcpURL := fmt.Sprintf("http://localhost:%d/mcp", port)
	ready := waitForHTTP(fmt.Sprintf("http://localhost:%d/health", port), 30)
	if !ready {
		cmd.Process.Kill()
		t.Fatalf("mcp-filesystem failed to start on port %d", port)
	}

	client := mcp.NewHTTPClient(mcpURL)
	if err := client.Start(); err != nil {
		cmd.Process.Kill()
		t.Fatalf("Failed to connect to mcp-filesystem: %v", err)
	}

	t.Cleanup(func() {
		client.Stop()
		cmd.Process.Kill()
		cmd.Wait()
	})

	return &fsTestEnv{
		client:    client,
		rootDir:   rootDir,
		sourceDir: sourceDir,
		destDir:   destDir,
	}
}

// callTool calls an MCP tool and returns the parsed JSON result.
func callTool(t *testing.T, client *mcp.HTTPClient, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	result, err := client.CallTool(name, args)
	if err != nil {
		t.Fatalf("Tool call %s failed: %v", name, err)
	}
	if len(result.Content) == 0 {
		t.Fatalf("Tool call %s returned empty content", name)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &parsed); err != nil {
		t.Fatalf("Failed to parse %s result: %v (text: %s)", name, err, result.Content[0].Text)
	}

	return parsed, result.IsError
}

// callToolExpectError calls a tool and expects an error result.
func callToolExpectError(t *testing.T, client *mcp.HTTPClient, name string, args map[string]any) string {
	t.Helper()
	result, err := client.CallTool(name, args)
	if err != nil {
		t.Fatalf("Tool call %s failed: %v", name, err)
	}
	if !result.IsError {
		t.Fatalf("Expected error from %s, got success: %s", name, result.Content[0].Text)
	}
	return result.Content[0].Text
}

// callToolExpectSuccess calls a tool and expects success.
func callToolExpectSuccess(t *testing.T, client *mcp.HTTPClient, name string, args map[string]any) map[string]any {
	t.Helper()
	parsed, isErr := callTool(t, client, name, args)
	if isErr {
		t.Fatalf("Expected success from %s, got error: %v", name, parsed)
	}
	return parsed
}

// --- E2E-001: Read, modify, verify file workflow ---
func TestFS_E2E001_ReadModifyVerify(t *testing.T) {
	env := setupFSTest(t, 9190, 0)

	// Create test file
	os.WriteFile(filepath.Join(env.rootDir, "hello.txt"), []byte("Hello World\n"), 0644)

	// Read file
	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "hello.txt",
	})
	content := result["content"].(string)
	if !strings.Contains(content, "Hello World") {
		t.Fatalf("Expected 'Hello World', got %q", content)
	}

	// Patch file
	patch := `--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-Hello World
+Hello Agent`
	result = callToolExpectSuccess(t, env.client, "patch_file", map[string]any{
		"root": "workspace", "path": "hello.txt", "patch": patch,
	})
	hunks := result["hunks_applied"].(float64)
	if hunks != 1 {
		t.Fatalf("Expected 1 hunk, got %v", hunks)
	}

	// Read again
	result = callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "hello.txt",
	})
	content = result["content"].(string)
	if !strings.Contains(content, "Hello Agent") {
		t.Fatalf("Expected 'Hello Agent', got %q", content)
	}

	// Hash
	result = callToolExpectSuccess(t, env.client, "hash_file", map[string]any{
		"root": "workspace", "path": "hello.txt", "algorithm": "sha256",
	})
	hashVal := result["hash"].(string)
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte("Hello Agent\n")))
	if hashVal != expected {
		t.Fatalf("Expected hash %s, got %s", expected, hashVal)
	}
}

// --- E2E-002: Multi-root discovery and cross-root copy ---
func TestFS_E2E002_MultiRootCrossRootCopy(t *testing.T) {
	env := setupFSTest(t, 9191, 0)

	// List roots (returns JSON array, not object)
	raw, err := env.client.CallTool("list_roots", map[string]any{})
	if err != nil {
		t.Fatalf("list_roots failed: %v", err)
	}
	rootsStr := raw.Content[0].Text
	if !strings.Contains(rootsStr, "workspace") || !strings.Contains(rootsStr, "source") || !strings.Contains(rootsStr, "dest") {
		t.Fatalf("Expected all roots listed, got: %s", rootsStr)
	}
	// Should not contain actual paths
	if strings.Contains(rootsStr, env.rootDir) {
		t.Fatal("list_roots should not expose host paths")
	}

	// Write file in source
	callToolExpectSuccess(t, env.client, "write_file", map[string]any{
		"root": "source", "path": "data.txt", "content": "important data",
	})

	// Cross-root copy
	callToolExpectSuccess(t, env.client, "copy", map[string]any{
		"source_root": "source", "source_path": "data.txt",
		"dest_root": "dest", "dest_path": "data.txt",
	})

	// Read from dest
	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "dest", "path": "data.txt",
	})
	if result["content"].(string) != "important data" {
		t.Fatalf("Expected 'important data', got %q", result["content"])
	}
}

// --- E2E-003: Search codebase and patch file ---
func TestFS_E2E003_SearchAndPatch(t *testing.T) {
	env := setupFSTest(t, 9192, 0)

	// Create Go files with TODOs
	os.MkdirAll(filepath.Join(env.rootDir, "src"), 0755)
	os.WriteFile(filepath.Join(env.rootDir, "src", "main.go"), []byte("package main\n\n// TODO: implement\nfunc main() {}\n"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "src", "util.go"), []byte("package main\n\n// TODO: refactor\nfunc helper() {}\n"), 0644)

	// Grep for TODOs
	result := callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "TODO", "glob_filter": "*.go", "context_lines": 1,
	})
	totalMatches := int(result["total_matches"].(float64))
	if totalMatches != 2 {
		t.Fatalf("Expected 2 TODO matches, got %d", totalMatches)
	}

	// Glob for Go files
	result = callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "workspace", "pattern": "**/*.go",
	})
	globMatches := int(result["total_matches"].(float64))
	if globMatches != 2 {
		t.Fatalf("Expected 2 .go files, got %d", globMatches)
	}
}

// --- E2E-004: list_folder happy path ---
func TestFS_E2E004_ListFolderHappy(t *testing.T) {
	env := setupFSTest(t, 9193, 0)

	os.WriteFile(filepath.Join(env.rootDir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "b.txt"), []byte("b"), 0644)
	os.MkdirAll(filepath.Join(env.rootDir, "sub"), 0755)

	result := callToolExpectSuccess(t, env.client, "list_folder", map[string]any{
		"root": "workspace", "path": ".",
	})
	count := int(result["count"].(float64))
	if count != 3 {
		t.Fatalf("Expected 3 entries, got %d", count)
	}
}

// --- E2E-005: list_folder empty directory ---
func TestFS_E2E005_ListFolderEmpty(t *testing.T) {
	env := setupFSTest(t, 9194, 0)

	os.MkdirAll(filepath.Join(env.rootDir, "empty"), 0755)

	result := callToolExpectSuccess(t, env.client, "list_folder", map[string]any{
		"root": "workspace", "path": "empty",
	})
	count := int(result["count"].(float64))
	if count != 0 {
		t.Fatalf("Expected 0 entries, got %d", count)
	}
}

// --- E2E-006: list_folder path is a file ---
func TestFS_E2E006_ListFolderFile(t *testing.T) {
	env := setupFSTest(t, 9195, 0)

	os.WriteFile(filepath.Join(env.rootDir, "data.txt"), []byte("data"), 0644)

	errMsg := callToolExpectError(t, env.client, "list_folder", map[string]any{
		"root": "workspace", "path": "data.txt",
	})
	if !strings.Contains(errMsg, "not a directory") {
		t.Fatalf("Expected 'not a directory' error, got: %s", errMsg)
	}
}

// --- E2E-007: read_file full read under threshold ---
func TestFS_E2E007_ReadFileFullSmall(t *testing.T) {
	env := setupFSTest(t, 9196, 0)

	content := "hello world 100 bytes padding padding padding padding padding padding padding padding padding pad"
	os.WriteFile(filepath.Join(env.rootDir, "small.txt"), []byte(content), 0644)

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "small.txt",
	})
	if result["content"].(string) != content {
		t.Fatalf("Content mismatch")
	}
}

// --- E2E-008: read_file full read exceeds threshold ---
func TestFS_E2E008_ReadFileExceedsThreshold(t *testing.T) {
	env := setupFSTest(t, 9197, 100) // 100 byte threshold

	content := strings.Repeat("x", 500)
	os.WriteFile(filepath.Join(env.rootDir, "large.txt"), []byte(content), 0644)

	errMsg := callToolExpectError(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "large.txt",
	})
	if !strings.Contains(errMsg, "too large") {
		t.Fatalf("Expected 'too large' error, got: %s", errMsg)
	}
}

// --- E2E-009: read_file byte-based partial read ---
func TestFS_E2E009_ReadFileBytePartial(t *testing.T) {
	env := setupFSTest(t, 9198, 0)

	content := "0123456789abcdefghijklmnopqrstuvwxyz"
	os.WriteFile(filepath.Join(env.rootDir, "data.bin"), []byte(content), 0644)

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "data.bin", "offset_bytes": 10, "limit_bytes": 20,
	})
	got := result["content"].(string)
	expected := "abcdefghijklmnopqrst"
	if got != expected {
		t.Fatalf("Expected %q, got %q", expected, got)
	}
}

// --- E2E-010: read_file line-based partial read ---
func TestFS_E2E010_ReadFileLinePartial(t *testing.T) {
	env := setupFSTest(t, 9199, 0)

	var lines []string
	for i := 1; i <= 100; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	os.WriteFile(filepath.Join(env.rootDir, "lines.txt"), []byte(strings.Join(lines, "\n")+"\n"), 0644)

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "lines.txt", "offset_lines": 50, "limit_lines": 5,
	})
	got := result["content"].(string)
	if !strings.Contains(got, "line 50") || !strings.Contains(got, "line 54") {
		t.Fatalf("Expected lines 50-54, got %q", got)
	}
}

// --- E2E-011: read_file mixed byte+line parameters rejected ---
func TestFS_E2E011_ReadFileMixedParams(t *testing.T) {
	env := setupFSTest(t, 9200, 0)

	os.WriteFile(filepath.Join(env.rootDir, "file.txt"), []byte("data"), 0644)

	errMsg := callToolExpectError(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "file.txt", "offset_bytes": 0, "offset_lines": 1,
	})
	if !strings.Contains(errMsg, "mutually exclusive") {
		t.Fatalf("Expected 'mutually exclusive' error, got: %s", errMsg)
	}
}

// --- E2E-012: read_file offset beyond file end ---
func TestFS_E2E012_ReadFileOffsetBeyondEnd(t *testing.T) {
	env := setupFSTest(t, 9201, 0)

	os.WriteFile(filepath.Join(env.rootDir, "short.txt"), []byte("line 1\nline 2\nline 3\nline 4\nline 5\n"), 0644)

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "short.txt", "offset_lines": 100, "limit_lines": 10,
	})
	if result["content"].(string) != "" {
		t.Fatalf("Expected empty content, got %q", result["content"])
	}
	if result["lines_total"].(float64) != 5 {
		t.Fatalf("Expected lines_total=5, got %v", result["lines_total"])
	}
}

// --- E2E-013: read_file binary file detection ---
func TestFS_E2E013_ReadFileBinaryDetection(t *testing.T) {
	env := setupFSTest(t, 9202, 0)

	content := []byte("hello\x00world\x00binary")
	os.WriteFile(filepath.Join(env.rootDir, "image.bin"), content, 0644)

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "image.bin", "offset_bytes": 0, "limit_bytes": 100,
	})
	if result["binary"] != true {
		t.Fatal("Expected binary=true")
	}
}

// --- E2E-014: write_file overwrite mode ---
func TestFS_E2E014_WriteFileOverwrite(t *testing.T) {
	env := setupFSTest(t, 9203, 0)

	os.WriteFile(filepath.Join(env.rootDir, "existing.txt"), []byte("old content"), 0644)

	callToolExpectSuccess(t, env.client, "write_file", map[string]any{
		"root": "workspace", "path": "existing.txt", "content": "new content", "mode": "overwrite",
	})

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "existing.txt",
	})
	if result["content"].(string) != "new content" {
		t.Fatalf("Expected 'new content', got %q", result["content"])
	}
}

// --- E2E-015: write_file append mode ---
func TestFS_E2E015_WriteFileAppend(t *testing.T) {
	env := setupFSTest(t, 9204, 0)

	os.WriteFile(filepath.Join(env.rootDir, "log.txt"), []byte("line1\n"), 0644)

	callToolExpectSuccess(t, env.client, "write_file", map[string]any{
		"root": "workspace", "path": "log.txt", "content": "line2\n", "mode": "append",
	})

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "log.txt",
	})
	if result["content"].(string) != "line1\nline2\n" {
		t.Fatalf("Expected 'line1\\nline2\\n', got %q", result["content"])
	}
}

// --- E2E-016: write_file create_only mode, file absent ---
func TestFS_E2E016_WriteFileCreateOnlyAbsent(t *testing.T) {
	env := setupFSTest(t, 9205, 0)

	callToolExpectSuccess(t, env.client, "write_file", map[string]any{
		"root": "workspace", "path": "new.txt", "content": "created", "mode": "create_only",
	})

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "new.txt",
	})
	if result["content"].(string) != "created" {
		t.Fatalf("Expected 'created', got %q", result["content"])
	}
}

// --- E2E-017: write_file create_only mode, file exists ---
func TestFS_E2E017_WriteFileCreateOnlyExists(t *testing.T) {
	env := setupFSTest(t, 9206, 0)

	os.WriteFile(filepath.Join(env.rootDir, "existing.txt"), []byte("original"), 0644)

	errMsg := callToolExpectError(t, env.client, "write_file", map[string]any{
		"root": "workspace", "path": "existing.txt", "content": "nope", "mode": "create_only",
	})
	if !strings.Contains(errMsg, "already exists") {
		t.Fatalf("Expected 'already exists' error, got: %s", errMsg)
	}
}

// --- E2E-018: write_file auto-create parent directories ---
func TestFS_E2E018_WriteFileAutoCreateParent(t *testing.T) {
	env := setupFSTest(t, 9207, 0)

	callToolExpectSuccess(t, env.client, "write_file", map[string]any{
		"root": "workspace", "path": "deep/nested/dir/file.txt", "content": "deep",
	})

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "deep/nested/dir/file.txt",
	})
	if result["content"].(string) != "deep" {
		t.Fatalf("Expected 'deep', got %q", result["content"])
	}
}

// --- E2E-019: remove_file happy path ---
func TestFS_E2E019_RemoveFileHappy(t *testing.T) {
	env := setupFSTest(t, 9208, 0)

	os.WriteFile(filepath.Join(env.rootDir, "to_delete.txt"), []byte("delete me"), 0644)

	callToolExpectSuccess(t, env.client, "remove_file", map[string]any{
		"root": "workspace", "path": "to_delete.txt",
	})

	errMsg := callToolExpectError(t, env.client, "stat_file", map[string]any{
		"root": "workspace", "path": "to_delete.txt",
	})
	if !strings.Contains(errMsg, "not found") {
		t.Fatalf("Expected 'not found', got: %s", errMsg)
	}
}

// --- E2E-020: remove_file file not found ---
func TestFS_E2E020_RemoveFileNotFound(t *testing.T) {
	env := setupFSTest(t, 9209, 0)

	errMsg := callToolExpectError(t, env.client, "remove_file", map[string]any{
		"root": "workspace", "path": "ghost.txt",
	})
	if !strings.Contains(errMsg, "not found") {
		t.Fatalf("Expected 'not found', got: %s", errMsg)
	}
}

// --- E2E-021: remove_file target is directory ---
func TestFS_E2E021_RemoveFileIsDir(t *testing.T) {
	env := setupFSTest(t, 9210, 0)

	os.MkdirAll(filepath.Join(env.rootDir, "mydir"), 0755)

	errMsg := callToolExpectError(t, env.client, "remove_file", map[string]any{
		"root": "workspace", "path": "mydir",
	})
	if !strings.Contains(errMsg, "directory") {
		t.Fatalf("Expected 'directory' error, got: %s", errMsg)
	}
}

// --- E2E-022: patch_file apply valid unified diff ---
func TestFS_E2E022_PatchFileValid(t *testing.T) {
	env := setupFSTest(t, 9211, 0)

	os.WriteFile(filepath.Join(env.rootDir, "code.go"), []byte("package main\n\nfunc hello() {\n\tprintln(\"hello\")\n}\n"), 0644)

	patch := `--- a/code.go
+++ b/code.go
@@ -3,3 +3,3 @@
 func hello() {
-	println("hello")
+	println("world")
 }`
	result := callToolExpectSuccess(t, env.client, "patch_file", map[string]any{
		"root": "workspace", "path": "code.go", "patch": patch,
	})
	if result["hunks_applied"].(float64) != 1 {
		t.Fatal("Expected 1 hunk applied")
	}

	readResult := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "code.go",
	})
	if !strings.Contains(readResult["content"].(string), "world") {
		t.Fatal("Expected patched content to contain 'world'")
	}
}

// --- E2E-023: patch_file hunk mismatch fails atomically ---
func TestFS_E2E023_PatchFileHunkMismatch(t *testing.T) {
	env := setupFSTest(t, 9212, 0)

	original := "line A\nline B\nline C\n"
	os.WriteFile(filepath.Join(env.rootDir, "code.go"), []byte(original), 0644)

	patch := `--- a/code.go
+++ b/code.go
@@ -1,3 +1,3 @@
 line A
-line X
+line Y
 line C`
	errMsg := callToolExpectError(t, env.client, "patch_file", map[string]any{
		"root": "workspace", "path": "code.go", "patch": patch,
	})
	if !strings.Contains(errMsg, "does not match") {
		t.Fatalf("Expected 'does not match' error, got: %s", errMsg)
	}

	// Verify file unchanged
	data, _ := os.ReadFile(filepath.Join(env.rootDir, "code.go"))
	if string(data) != original {
		t.Fatal("File should be unchanged after failed patch")
	}
}

// --- E2E-024: patch_file creates new file ---
func TestFS_E2E024_PatchFileCreatesNew(t *testing.T) {
	env := setupFSTest(t, 9213, 0)

	patch := `--- /dev/null
+++ b/new_file.txt
@@ -0,0 +1,2 @@
+line one
+line two`
	callToolExpectSuccess(t, env.client, "patch_file", map[string]any{
		"root": "workspace", "path": "new_file.txt", "patch": patch,
	})

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "new_file.txt",
	})
	content := result["content"].(string)
	if !strings.Contains(content, "line one") || !strings.Contains(content, "line two") {
		t.Fatalf("Expected new file content, got %q", content)
	}
}

// --- E2E-025: create_folder happy path ---
func TestFS_E2E025_CreateFolderNested(t *testing.T) {
	env := setupFSTest(t, 9214, 0)

	callToolExpectSuccess(t, env.client, "create_folder", map[string]any{
		"root": "workspace", "path": "a/b/c",
	})

	result := callToolExpectSuccess(t, env.client, "list_folder", map[string]any{
		"root": "workspace", "path": "a/b",
	})
	if result["count"].(float64) != 1 {
		t.Fatal("Expected 1 entry (c)")
	}
}

// --- E2E-026: create_folder idempotent ---
func TestFS_E2E026_CreateFolderIdempotent(t *testing.T) {
	env := setupFSTest(t, 9215, 0)

	os.MkdirAll(filepath.Join(env.rootDir, "existing_dir"), 0755)

	callToolExpectSuccess(t, env.client, "create_folder", map[string]any{
		"root": "workspace", "path": "existing_dir",
	})
}

// --- E2E-027: create_folder conflict with file ---
func TestFS_E2E027_CreateFolderFileConflict(t *testing.T) {
	env := setupFSTest(t, 9216, 0)

	os.WriteFile(filepath.Join(env.rootDir, "conflict"), []byte("file"), 0644)

	errMsg := callToolExpectError(t, env.client, "create_folder", map[string]any{
		"root": "workspace", "path": "conflict",
	})
	if !strings.Contains(errMsg, "exists as a file") {
		t.Fatalf("Expected 'exists as a file' error, got: %s", errMsg)
	}
}

// --- E2E-028: remove_folder recursive ---
func TestFS_E2E028_RemoveFolderRecursive(t *testing.T) {
	env := setupFSTest(t, 9217, 0)

	os.MkdirAll(filepath.Join(env.rootDir, "dir", "sub"), 0755)
	os.WriteFile(filepath.Join(env.rootDir, "dir", "file.txt"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "dir", "sub", "nested.txt"), []byte("y"), 0644)

	callToolExpectSuccess(t, env.client, "remove_folder", map[string]any{
		"root": "workspace", "path": "dir",
	})

	errMsg := callToolExpectError(t, env.client, "stat_file", map[string]any{
		"root": "workspace", "path": "dir",
	})
	if !strings.Contains(errMsg, "not found") {
		t.Fatalf("Expected 'not found', got: %s", errMsg)
	}
}

// --- E2E-029: remove_folder refuse root deletion ---
func TestFS_E2E029_RemoveFolderRefuseRoot(t *testing.T) {
	env := setupFSTest(t, 9218, 0)

	errMsg := callToolExpectError(t, env.client, "remove_folder", map[string]any{
		"root": "workspace", "path": ".",
	})
	if !strings.Contains(errMsg, "cannot remove root directory") {
		t.Fatalf("Expected 'cannot remove root directory' error, got: %s", errMsg)
	}
}

// --- E2E-030: remove_folder not found ---
func TestFS_E2E030_RemoveFolderNotFound(t *testing.T) {
	env := setupFSTest(t, 9219, 0)

	errMsg := callToolExpectError(t, env.client, "remove_folder", map[string]any{
		"root": "workspace", "path": "nonexistent",
	})
	if !strings.Contains(errMsg, "not found") {
		t.Fatalf("Expected 'not found', got: %s", errMsg)
	}
}

// --- E2E-031: remove_folder target is file ---
func TestFS_E2E031_RemoveFolderIsFile(t *testing.T) {
	env := setupFSTest(t, 9220, 0)

	os.WriteFile(filepath.Join(env.rootDir, "not_a_dir.txt"), []byte("file"), 0644)

	errMsg := callToolExpectError(t, env.client, "remove_folder", map[string]any{
		"root": "workspace", "path": "not_a_dir.txt",
	})
	if !strings.Contains(errMsg, "file") {
		t.Fatalf("Expected file error, got: %s", errMsg)
	}
}

// --- E2E-032: stat_file file metadata ---
func TestFS_E2E032_StatFileMetadata(t *testing.T) {
	env := setupFSTest(t, 9221, 0)

	os.WriteFile(filepath.Join(env.rootDir, "data.json"), []byte(`{"key":"val"}`), 0644)

	result := callToolExpectSuccess(t, env.client, "stat_file", map[string]any{
		"root": "workspace", "path": "data.json",
	})
	if result["is_directory"].(bool) {
		t.Fatal("Expected is_directory=false")
	}
	if result["name"].(string) != "data.json" {
		t.Fatalf("Expected name=data.json, got %q", result["name"])
	}
}

// --- E2E-033: stat_file directory metadata ---
func TestFS_E2E033_StatDirectory(t *testing.T) {
	env := setupFSTest(t, 9222, 0)

	os.MkdirAll(filepath.Join(env.rootDir, "subdir"), 0755)

	result := callToolExpectSuccess(t, env.client, "stat_file", map[string]any{
		"root": "workspace", "path": "subdir",
	})
	if !result["is_directory"].(bool) {
		t.Fatal("Expected is_directory=true")
	}
}

// --- E2E-035: hash_file sha256 ---
func TestFS_E2E035_HashSHA256(t *testing.T) {
	env := setupFSTest(t, 9224, 0)

	content := "test content\n"
	os.WriteFile(filepath.Join(env.rootDir, "known.txt"), []byte(content), 0644)

	result := callToolExpectSuccess(t, env.client, "hash_file", map[string]any{
		"root": "workspace", "path": "known.txt", "algorithm": "sha256",
	})
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	if result["hash"].(string) != expected {
		t.Fatalf("Expected %s, got %s", expected, result["hash"])
	}
}

// --- E2E-037: hash_file unsupported algorithm ---
func TestFS_E2E037_HashUnsupportedAlgo(t *testing.T) {
	env := setupFSTest(t, 9226, 0)

	os.WriteFile(filepath.Join(env.rootDir, "file.txt"), []byte("data"), 0644)

	errMsg := callToolExpectError(t, env.client, "hash_file", map[string]any{
		"root": "workspace", "path": "file.txt", "algorithm": "md4",
	})
	if !strings.Contains(errMsg, "unsupported") {
		t.Fatalf("Expected 'unsupported' error, got: %s", errMsg)
	}
}

// --- E2E-038: hash_file target is directory ---
func TestFS_E2E038_HashDirectory(t *testing.T) {
	env := setupFSTest(t, 9227, 0)

	os.MkdirAll(filepath.Join(env.rootDir, "subdir"), 0755)

	errMsg := callToolExpectError(t, env.client, "hash_file", map[string]any{
		"root": "workspace", "path": "subdir", "algorithm": "sha256",
	})
	if !strings.Contains(errMsg, "directory") {
		t.Fatalf("Expected 'directory' error, got: %s", errMsg)
	}
}

// --- E2E-039: permissions_file ---
func TestFS_E2E039_PermissionsFile(t *testing.T) {
	env := setupFSTest(t, 9228, 0)

	os.WriteFile(filepath.Join(env.rootDir, "script.sh"), []byte("#!/bin/sh"), 0755)

	result := callToolExpectSuccess(t, env.client, "permissions_file", map[string]any{
		"root": "workspace", "path": "script.sh",
	})
	mode := result["mode"].(string)
	if mode != "0755" {
		t.Fatalf("Expected mode=0755, got %s", mode)
	}
}

// --- E2E-041: copy same root ---
func TestFS_E2E041_CopySameRoot(t *testing.T) {
	env := setupFSTest(t, 9230, 0)

	os.WriteFile(filepath.Join(env.rootDir, "original.txt"), []byte("content"), 0644)

	callToolExpectSuccess(t, env.client, "copy", map[string]any{
		"source_root": "workspace", "source_path": "original.txt",
		"dest_root": "workspace", "dest_path": "duplicate.txt",
	})

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "duplicate.txt",
	})
	if result["content"].(string) != "content" {
		t.Fatal("Copied file content mismatch")
	}
}

// --- E2E-042: copy cross-root ---
func TestFS_E2E042_CopyCrossRoot(t *testing.T) {
	env := setupFSTest(t, 9231, 0)

	os.WriteFile(filepath.Join(env.sourceDir, "file.txt"), []byte("cross root data"), 0644)

	callToolExpectSuccess(t, env.client, "copy", map[string]any{
		"source_root": "source", "source_path": "file.txt",
		"dest_root": "dest", "dest_path": "file.txt",
	})

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "dest", "path": "file.txt",
	})
	if result["content"].(string) != "cross root data" {
		t.Fatal("Cross-root copy content mismatch")
	}
}

// --- E2E-043: copy directory recursive ---
func TestFS_E2E043_CopyDirRecursive(t *testing.T) {
	env := setupFSTest(t, 9232, 0)

	os.MkdirAll(filepath.Join(env.rootDir, "project", "sub"), 0755)
	os.WriteFile(filepath.Join(env.rootDir, "project", "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "project", "sub", "b.txt"), []byte("b"), 0644)

	callToolExpectSuccess(t, env.client, "copy", map[string]any{
		"source_root": "workspace", "source_path": "project",
		"dest_root": "workspace", "dest_path": "project-backup",
	})

	result := callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "workspace", "pattern": "project-backup/**/*",
	})
	if result["total_matches"].(float64) < 2 {
		t.Fatal("Expected at least 2 files in backup")
	}
}

// --- E2E-044: copy source not found ---
func TestFS_E2E044_CopySourceNotFound(t *testing.T) {
	env := setupFSTest(t, 9233, 0)

	errMsg := callToolExpectError(t, env.client, "copy", map[string]any{
		"source_root": "workspace", "source_path": "missing.txt",
		"dest_root": "workspace", "dest_path": "dest.txt",
	})
	if !strings.Contains(errMsg, "not found") {
		t.Fatalf("Expected 'not found', got: %s", errMsg)
	}
}

// --- E2E-045: move same root ---
func TestFS_E2E045_MoveSameRoot(t *testing.T) {
	env := setupFSTest(t, 9234, 0)

	os.WriteFile(filepath.Join(env.rootDir, "old_name.txt"), []byte("move me"), 0644)

	callToolExpectSuccess(t, env.client, "move", map[string]any{
		"source_root": "workspace", "source_path": "old_name.txt",
		"dest_root": "workspace", "dest_path": "new_name.txt",
	})

	// Old path should not exist
	callToolExpectError(t, env.client, "stat_file", map[string]any{
		"root": "workspace", "path": "old_name.txt",
	})

	// New path should have content
	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "new_name.txt",
	})
	if result["content"].(string) != "move me" {
		t.Fatal("Moved file content mismatch")
	}
}

// --- E2E-046: move cross-root ---
func TestFS_E2E046_MoveCrossRoot(t *testing.T) {
	env := setupFSTest(t, 9235, 0)

	os.WriteFile(filepath.Join(env.sourceDir, "file.txt"), []byte("move across"), 0644)

	callToolExpectSuccess(t, env.client, "move", map[string]any{
		"source_root": "source", "source_path": "file.txt",
		"dest_root": "dest", "dest_path": "file.txt",
	})

	// Check it exists in dest
	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "dest", "path": "file.txt",
	})
	if result["content"].(string) != "move across" {
		t.Fatal("Cross-root move content mismatch")
	}

	// Check it doesn't exist in source
	callToolExpectError(t, env.client, "stat_file", map[string]any{
		"root": "source", "path": "file.txt",
	})
}

// --- E2E-047: move source not found ---
func TestFS_E2E047_MoveSourceNotFound(t *testing.T) {
	env := setupFSTest(t, 9236, 0)

	errMsg := callToolExpectError(t, env.client, "move", map[string]any{
		"source_root": "workspace", "source_path": "missing.txt",
		"dest_root": "workspace", "dest_path": "dest.txt",
	})
	if !strings.Contains(errMsg, "not found") {
		t.Fatalf("Expected 'not found', got: %s", errMsg)
	}
}

// --- E2E-048: grep regex with context ---
func TestFS_E2E048_GrepWithContext(t *testing.T) {
	env := setupFSTest(t, 9237, 0)

	content := "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\n// TODO: fix this\nline 11\nline 12\n"
	os.WriteFile(filepath.Join(env.rootDir, "code.go"), []byte(content), 0644)

	result := callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "TODO", "context_lines": 2,
	})
	matches := result["matches"].([]any)
	if len(matches) != 1 {
		t.Fatalf("Expected 1 match, got %d", len(matches))
	}
	m := matches[0].(map[string]any)
	if m["line_number"].(float64) != 10 {
		t.Fatalf("Expected match at line 10, got %v", m["line_number"])
	}
}

// --- E2E-049: grep case insensitive ---
func TestFS_E2E049_GrepCaseInsensitive(t *testing.T) {
	env := setupFSTest(t, 9238, 0)

	os.WriteFile(filepath.Join(env.rootDir, "mixed.txt"), []byte("Error\nerror\nERROR\n"), 0644)

	result := callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "error", "case_insensitive": true,
	})
	if result["total_matches"].(float64) != 3 {
		t.Fatalf("Expected 3 matches, got %v", result["total_matches"])
	}
}

// --- E2E-050: grep file type filter ---
func TestFS_E2E050_GrepFileTypeFilter(t *testing.T) {
	env := setupFSTest(t, 9239, 0)

	os.WriteFile(filepath.Join(env.rootDir, "file.go"), []byte("// TODO\n"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "file.py"), []byte("# TODO\n"), 0644)

	result := callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "TODO", "glob_filter": "*.go",
	})
	if result["total_matches"].(float64) != 1 {
		t.Fatalf("Expected 1 match (only .go), got %v", result["total_matches"])
	}
}

// --- E2E-051: grep no matches ---
func TestFS_E2E051_GrepNoMatches(t *testing.T) {
	env := setupFSTest(t, 9240, 0)

	os.WriteFile(filepath.Join(env.rootDir, "file.txt"), []byte("hello world\n"), 0644)

	result := callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "ZZZZUNIQUEZZZZZ",
	})
	if result["total_matches"].(float64) != 0 {
		t.Fatal("Expected 0 matches")
	}
}

// --- E2E-052: grep invalid regex ---
func TestFS_E2E052_GrepInvalidRegex(t *testing.T) {
	env := setupFSTest(t, 9241, 0)

	errMsg := callToolExpectError(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "[invalid",
	})
	if !strings.Contains(errMsg, "invalid pattern") {
		t.Fatalf("Expected 'invalid pattern', got: %s", errMsg)
	}
}

// --- E2E-053: grep max results truncation ---
func TestFS_E2E053_GrepMaxResults(t *testing.T) {
	env := setupFSTest(t, 9242, 0)

	// Create files with many matches
	for i := range 10 {
		content := strings.Repeat(fmt.Sprintf("match line %d\n", i), 5)
		os.WriteFile(filepath.Join(env.rootDir, fmt.Sprintf("file%d.txt", i)), []byte(content), 0644)
	}

	result := callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "match", "max_results": 5,
	})
	if result["total_matches"].(float64) != 5 {
		t.Fatalf("Expected 5 matches, got %v", result["total_matches"])
	}
	if result["truncated"] != true {
		t.Fatal("Expected truncated=true")
	}
}

// --- E2E-054: grep binary file skipped ---
func TestFS_E2E054_GrepBinarySkipped(t *testing.T) {
	env := setupFSTest(t, 9243, 0)

	os.WriteFile(filepath.Join(env.rootDir, "text.txt"), []byte("pattern here\n"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "binary.bin"), []byte("pattern\x00here\x00binary"), 0644)

	result := callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "pattern",
	})
	if result["total_matches"].(float64) != 1 {
		t.Fatalf("Expected 1 match (text only, binary skipped), got %v", result["total_matches"])
	}
}

// --- E2E-055: glob pattern match ---
func TestFS_E2E055_GlobPattern(t *testing.T) {
	env := setupFSTest(t, 9244, 0)

	os.WriteFile(filepath.Join(env.rootDir, "a.go"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "b.go"), []byte("b"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "c.txt"), []byte("c"), 0644)

	result := callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "workspace", "pattern": "*.go",
	})
	if result["total_matches"].(float64) != 2 {
		t.Fatalf("Expected 2 .go files, got %v", result["total_matches"])
	}
}

// --- E2E-056: glob regex match ---
func TestFS_E2E056_GlobRegex(t *testing.T) {
	env := setupFSTest(t, 9245, 0)

	os.WriteFile(filepath.Join(env.rootDir, "test_1.go"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "test_2.go"), []byte("b"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "main.go"), []byte("c"), 0644)

	result := callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "workspace", "regex": `test_\d+\.go`,
	})
	if result["total_matches"].(float64) != 2 {
		t.Fatalf("Expected 2 test files, got %v", result["total_matches"])
	}
}

// --- E2E-057: glob doublestar recursive ---
func TestFS_E2E057_GlobDoublestar(t *testing.T) {
	env := setupFSTest(t, 9246, 0)

	os.MkdirAll(filepath.Join(env.rootDir, "src", "pkg", "sub"), 0755)
	os.WriteFile(filepath.Join(env.rootDir, "src", "a.go"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "src", "pkg", "b.go"), []byte("b"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "src", "pkg", "sub", "c.go"), []byte("c"), 0644)

	result := callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "workspace", "pattern": "**/*.go",
	})
	if result["total_matches"].(float64) != 3 {
		t.Fatalf("Expected 3 .go files, got %v", result["total_matches"])
	}
}

// --- E2E-058: glob type filter ---
func TestFS_E2E058_GlobTypeFilter(t *testing.T) {
	env := setupFSTest(t, 9247, 0)

	os.WriteFile(filepath.Join(env.rootDir, "file.txt"), []byte("f"), 0644)
	os.MkdirAll(filepath.Join(env.rootDir, "dir"), 0755)

	result := callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "workspace", "pattern": "*", "type_filter": "file",
	})
	matches := result["matches"].([]any)
	for _, m := range matches {
		if m.(map[string]any)["type"].(string) != "file" {
			t.Fatal("Expected only files")
		}
	}
}

// --- E2E-059: glob no matches ---
func TestFS_E2E059_GlobNoMatches(t *testing.T) {
	env := setupFSTest(t, 9248, 0)

	result := callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "workspace", "pattern": "*.xyz",
	})
	if result["total_matches"].(float64) != 0 {
		t.Fatal("Expected 0 matches")
	}
}

// --- E2E-060: glob both pattern and regex rejected ---
func TestFS_E2E060_GlobBothPatternAndRegex(t *testing.T) {
	env := setupFSTest(t, 9249, 0)

	errMsg := callToolExpectError(t, env.client, "glob", map[string]any{
		"root": "workspace", "pattern": "*.go", "regex": `.*\.go`,
	})
	if !strings.Contains(errMsg, "exactly one") {
		t.Fatalf("Expected 'exactly one' error, got: %s", errMsg)
	}
}

// --- E2E-061: list_roots returns configured roots ---
func TestFS_E2E061_ListRoots(t *testing.T) {
	env := setupFSTest(t, 9250, 0)

	raw, err := env.client.CallTool("list_roots", map[string]any{})
	if err != nil {
		t.Fatalf("list_roots failed: %v", err)
	}
	text := raw.Content[0].Text
	if !strings.Contains(text, "workspace") || !strings.Contains(text, "source") || !strings.Contains(text, "dest") {
		t.Fatalf("Expected all roots, got: %s", text)
	}
}

// --- E2E-062: list_roots does not expose host paths ---
func TestFS_E2E062_ListRootsNoPaths(t *testing.T) {
	env := setupFSTest(t, 9251, 0)

	raw, err := env.client.CallTool("list_roots", map[string]any{})
	if err != nil {
		t.Fatalf("list_roots failed: %v", err)
	}
	text := raw.Content[0].Text
	if strings.Contains(text, env.rootDir) || strings.Contains(text, "/tmp") {
		t.Fatalf("list_roots should not expose host paths, got: %s", text)
	}
}

// --- E2E-063: Path traversal via .. rejected ---
func TestFS_E2E063_PathTraversal(t *testing.T) {
	env := setupFSTest(t, 9252, 0)

	errMsg := callToolExpectError(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "../../etc/passwd",
	})
	if !strings.Contains(errMsg, "outside root") {
		t.Fatalf("Expected 'outside root' error, got: %s", errMsg)
	}
}

// --- E2E-064: Symlink escape rejected ---
func TestFS_E2E064_SymlinkEscape(t *testing.T) {
	env := setupFSTest(t, 9253, 0)

	os.Symlink("/tmp", filepath.Join(env.rootDir, "escape"))

	errMsg := callToolExpectError(t, env.client, "list_folder", map[string]any{
		"root": "workspace", "path": "escape",
	})
	if !strings.Contains(errMsg, "outside root") {
		t.Fatalf("Expected 'outside root' error, got: %s", errMsg)
	}
}

// --- E2E-065: Symlink within root followed ---
func TestFS_E2E065_SymlinkWithinRoot(t *testing.T) {
	env := setupFSTest(t, 9254, 0)

	os.WriteFile(filepath.Join(env.rootDir, "real.txt"), []byte("real content"), 0644)
	os.Symlink(filepath.Join(env.rootDir, "real.txt"), filepath.Join(env.rootDir, "link.txt"))

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "link.txt",
	})
	if result["content"].(string) != "real content" {
		t.Fatal("Symlink within root should resolve")
	}
}

// --- E2E-066: Tool not in allowlist rejected ---
func TestFS_E2E066_AllowlistRejected(t *testing.T) {
	env := setupFSTest(t, 9255, 0)

	errMsg := callToolExpectError(t, env.client, "write_file", map[string]any{
		"root": "readonly", "path": "test.txt", "content": "nope",
	})
	if !strings.Contains(errMsg, "not allowed") {
		t.Fatalf("Expected 'not allowed' error, got: %s", errMsg)
	}
}

// --- E2E-067: Cross-root copy denied by source allowlist ---
func TestFS_E2E067_CopyDeniedBySource(t *testing.T) {
	env := setupFSTest(t, 9256, 0)

	errMsg := callToolExpectError(t, env.client, "copy", map[string]any{
		"source_root": "restricted", "source_path": "file.txt",
		"dest_root": "workspace", "dest_path": "file.txt",
	})
	if !strings.Contains(errMsg, "not allowed") && !strings.Contains(errMsg, "restricted") {
		t.Fatalf("Expected allowlist error for restricted root, got: %s", errMsg)
	}
}

// --- E2E-068: Cross-root copy denied by dest allowlist ---
func TestFS_E2E068_CopyDeniedByDest(t *testing.T) {
	env := setupFSTest(t, 9257, 0)

	os.WriteFile(filepath.Join(env.sourceDir, "file.txt"), []byte("data"), 0644)

	errMsg := callToolExpectError(t, env.client, "copy", map[string]any{
		"source_root": "source", "source_path": "file.txt",
		"dest_root": "readonly", "dest_path": "file.txt",
	})
	if !strings.Contains(errMsg, "not allowed") {
		t.Fatalf("Expected 'not allowed' error, got: %s", errMsg)
	}
}

// --- E2E-070: Null bytes in path rejected ---
func TestFS_E2E070_NullBytesInPath(t *testing.T) {
	env := setupFSTest(t, 9259, 0)

	errMsg := callToolExpectError(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "file\x00.txt",
	})
	if !strings.Contains(errMsg, "null") {
		t.Fatalf("Expected null byte error, got: %s", errMsg)
	}
}

// --- E2E-072: Invalid root name ---
func TestFS_E2E072_InvalidRoot(t *testing.T) {
	env := setupFSTest(t, 9261, 0)

	errMsg := callToolExpectError(t, env.client, "read_file", map[string]any{
		"root": "nonexistent", "path": "file.txt",
	})
	if !strings.Contains(errMsg, "unknown root") {
		t.Fatalf("Expected 'unknown root' error, got: %s", errMsg)
	}
}

// --- E2E-073: Missing required parameters ---
func TestFS_E2E073_MissingParams(t *testing.T) {
	env := setupFSTest(t, 9262, 0)

	// Missing root
	errMsg := callToolExpectError(t, env.client, "read_file", map[string]any{
		"path": "file.txt",
	})
	if !strings.Contains(errMsg, "root") && !strings.Contains(errMsg, "required") {
		t.Fatalf("Expected 'root required' error, got: %s", errMsg)
	}

	// Missing path
	errMsg = callToolExpectError(t, env.client, "read_file", map[string]any{
		"root": "workspace",
	})
	if !strings.Contains(errMsg, "path") && !strings.Contains(errMsg, "required") {
		t.Fatalf("Expected 'path required' error, got: %s", errMsg)
	}
}

// --- E2E-074: Invalid write mode ---
func TestFS_E2E074_InvalidWriteMode(t *testing.T) {
	env := setupFSTest(t, 9263, 0)

	errMsg := callToolExpectError(t, env.client, "write_file", map[string]any{
		"root": "workspace", "path": "test.txt", "content": "x", "mode": "truncate",
	})
	if !strings.Contains(errMsg, "invalid write mode") {
		t.Fatalf("Expected 'invalid write mode' error, got: %s", errMsg)
	}
}

// --- E2E-075: Invalid hash algorithm ---
func TestFS_E2E075_InvalidHashAlgo(t *testing.T) {
	env := setupFSTest(t, 9264, 0)

	os.WriteFile(filepath.Join(env.rootDir, "file.txt"), []byte("data"), 0644)

	errMsg := callToolExpectError(t, env.client, "hash_file", map[string]any{
		"root": "workspace", "path": "file.txt", "algorithm": "sha512",
	})
	if !strings.Contains(errMsg, "unsupported") {
		t.Fatalf("Expected 'unsupported' error, got: %s", errMsg)
	}
}

// --- E2E-076: Invalid glob/regex syntax ---
func TestFS_E2E076_InvalidGlobRegex(t *testing.T) {
	env := setupFSTest(t, 9265, 0)

	errMsg := callToolExpectError(t, env.client, "glob", map[string]any{
		"root": "workspace", "regex": "[unclosed",
	})
	if !strings.Contains(errMsg, "invalid regex") {
		t.Fatalf("Expected 'invalid regex' error, got: %s", errMsg)
	}
}

// --- E2E-084: Health endpoint returns 200 ---
func TestFS_E2E084_HealthEndpoint(t *testing.T) {
	_ = setupFSTest(t, 9273, 0)

	// Health check already verified in setupFSTest, but let's explicitly test
	resp, err := httpGet("http://localhost:9273/health")
	if err != nil {
		t.Fatalf("Health check failed: %v", err)
	}
	if resp != 200 {
		t.Fatalf("Expected 200, got %d", resp)
	}
}

// httpGet is a simple GET request returning status code.
func httpGet(url string) (int, error) {
	resp, err := (&http.Client{}).Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}
