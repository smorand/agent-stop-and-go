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

// =============================================================================
// Coverage gap tests: hash_file, permissions_file, stat_file, read_file,
// list_folder, grep, glob, move, patch_file, write_file, copy edge cases
// =============================================================================

// --- E2E-085: hash_file md5 algorithm ---
func TestFS_E2E085_HashMD5(t *testing.T) {
	env := setupFSTest(t, 9280, 0)

	content := "md5 test content\n"
	os.WriteFile(filepath.Join(env.rootDir, "md5test.txt"), []byte(content), 0644)

	result := callToolExpectSuccess(t, env.client, "hash_file", map[string]any{
		"root": "workspace", "path": "md5test.txt", "algorithm": "md5",
	})
	if result["algorithm"].(string) != "md5" {
		t.Fatalf("Expected algorithm=md5, got %q", result["algorithm"])
	}
	hashVal := result["hash"].(string)
	if len(hashVal) != 32 { // md5 hex is 32 chars
		t.Fatalf("Expected 32-char md5 hash, got %d chars: %s", len(hashVal), hashVal)
	}
}

// --- E2E-086: hash_file sha1 algorithm ---
func TestFS_E2E086_HashSHA1(t *testing.T) {
	env := setupFSTest(t, 9281, 0)

	content := "sha1 test content\n"
	os.WriteFile(filepath.Join(env.rootDir, "sha1test.txt"), []byte(content), 0644)

	result := callToolExpectSuccess(t, env.client, "hash_file", map[string]any{
		"root": "workspace", "path": "sha1test.txt", "algorithm": "sha1",
	})
	if result["algorithm"].(string) != "sha1" {
		t.Fatalf("Expected algorithm=sha1, got %q", result["algorithm"])
	}
	hashVal := result["hash"].(string)
	if len(hashVal) != 40 { // sha1 hex is 40 chars
		t.Fatalf("Expected 40-char sha1 hash, got %d chars: %s", len(hashVal), hashVal)
	}
}

// --- E2E-087: hash_file nonexistent file ---
func TestFS_E2E087_HashNonexistent(t *testing.T) {
	env := setupFSTest(t, 9282, 0)

	errMsg := callToolExpectError(t, env.client, "hash_file", map[string]any{
		"root": "workspace", "path": "ghost.txt", "algorithm": "sha256",
	})
	if !strings.Contains(errMsg, "not found") {
		t.Fatalf("Expected 'not found' error, got: %s", errMsg)
	}
}

// --- E2E-088: permissions_file on directory ---
func TestFS_E2E088_PermissionsDirectory(t *testing.T) {
	env := setupFSTest(t, 9283, 0)

	os.MkdirAll(filepath.Join(env.rootDir, "permdir"), 0750)

	result := callToolExpectSuccess(t, env.client, "permissions_file", map[string]any{
		"root": "workspace", "path": "permdir",
	})
	mode := result["mode"].(string)
	if mode != "0750" {
		t.Fatalf("Expected mode=0750, got %s", mode)
	}
	// Verify owner and group fields are present
	if _, ok := result["owner"]; !ok {
		t.Fatal("Expected 'owner' field in permissions result")
	}
	if _, ok := result["group"]; !ok {
		t.Fatal("Expected 'group' field in permissions result")
	}
}

// --- E2E-089: permissions_file nonexistent path ---
func TestFS_E2E089_PermissionsNonexistent(t *testing.T) {
	env := setupFSTest(t, 9284, 0)

	errMsg := callToolExpectError(t, env.client, "permissions_file", map[string]any{
		"root": "workspace", "path": "no_such_file",
	})
	if !strings.Contains(errMsg, "not found") {
		t.Fatalf("Expected 'not found' error, got: %s", errMsg)
	}
}

// --- E2E-090: stat_file nonexistent path ---
func TestFS_E2E090_StatNonexistent(t *testing.T) {
	env := setupFSTest(t, 9285, 0)

	errMsg := callToolExpectError(t, env.client, "stat_file", map[string]any{
		"root": "workspace", "path": "nonexistent.txt",
	})
	if !strings.Contains(errMsg, "not found") {
		t.Fatalf("Expected 'not found' error, got: %s", errMsg)
	}
}

// --- E2E-091: stat_file on symlink within root resolves to target ---
// Security note: ValidatePath resolves symlinks before reaching the handler,
// so stat_file sees the resolved target, not the symlink itself.
// This test verifies stat_file returns correct metadata for a symlink target.
func TestFS_E2E091_StatSymlinkResolvesToTarget(t *testing.T) {
	env := setupFSTest(t, 9286, 0)

	os.WriteFile(filepath.Join(env.rootDir, "target.txt"), []byte("target content"), 0644)
	os.Symlink(filepath.Join(env.rootDir, "target.txt"), filepath.Join(env.rootDir, "link.txt"))

	result := callToolExpectSuccess(t, env.client, "stat_file", map[string]any{
		"root": "workspace", "path": "link.txt",
	})
	// After symlink resolution, it behaves as the target file
	if result["is_directory"] != false {
		t.Fatal("Expected is_directory=false for resolved symlink target")
	}
	size := int64(result["size"].(float64))
	if size != 14 { // len("target content") = 14
		t.Fatalf("Expected size=14 (target file size), got %d", size)
	}
	if result["name"].(string) != "target.txt" {
		t.Fatalf("Expected name=target.txt (resolved target), got %q", result["name"])
	}
}

// --- E2E-092: list_folder shows symlink entries with target_type ---
func TestFS_E2E092_ListFolderSymlinkEntries(t *testing.T) {
	env := setupFSTest(t, 9287, 0)

	// Create a file, a directory, and symlinks to each
	os.WriteFile(filepath.Join(env.rootDir, "realfile.txt"), []byte("content"), 0644)
	os.MkdirAll(filepath.Join(env.rootDir, "realdir"), 0755)
	os.Symlink(filepath.Join(env.rootDir, "realfile.txt"), filepath.Join(env.rootDir, "link_to_file"))
	os.Symlink(filepath.Join(env.rootDir, "realdir"), filepath.Join(env.rootDir, "link_to_dir"))
	os.Symlink("/tmp", filepath.Join(env.rootDir, "link_external"))

	result := callToolExpectSuccess(t, env.client, "list_folder", map[string]any{
		"root": "workspace", "path": ".",
	})

	entries := result["entries"].([]any)
	entryMap := make(map[string]map[string]any)
	for _, e := range entries {
		entry := e.(map[string]any)
		entryMap[entry["name"].(string)] = entry
	}

	// Check symlink to file
	linkFile, ok := entryMap["link_to_file"]
	if !ok {
		t.Fatal("Expected link_to_file in listing")
	}
	if linkFile["type"].(string) != "symlink" {
		t.Fatalf("Expected type=symlink for link_to_file, got %q", linkFile["type"])
	}
	if linkFile["target_type"].(string) != "file" {
		t.Fatalf("Expected target_type=file for link_to_file, got %q", linkFile["target_type"])
	}

	// Check symlink to directory
	linkDir, ok := entryMap["link_to_dir"]
	if !ok {
		t.Fatal("Expected link_to_dir in listing")
	}
	if linkDir["type"].(string) != "symlink" {
		t.Fatalf("Expected type=symlink for link_to_dir, got %q", linkDir["type"])
	}
	if linkDir["target_type"].(string) != "directory" {
		t.Fatalf("Expected target_type=directory for link_to_dir, got %q", linkDir["target_type"])
	}

	// Check symlink to external (outside root)
	linkExt, ok := entryMap["link_external"]
	if !ok {
		t.Fatal("Expected link_external in listing")
	}
	if linkExt["type"].(string) != "symlink" {
		t.Fatalf("Expected type=symlink for link_external, got %q", linkExt["type"])
	}
	if linkExt["target_type"].(string) != "external" {
		t.Fatalf("Expected target_type=external for link_external, got %q", linkExt["target_type"])
	}
}

// --- E2E-093: list_folder nonexistent path ---
func TestFS_E2E093_ListFolderNonexistent(t *testing.T) {
	env := setupFSTest(t, 9288, 0)

	errMsg := callToolExpectError(t, env.client, "list_folder", map[string]any{
		"root": "workspace", "path": "does_not_exist",
	})
	if !strings.Contains(errMsg, "not found") {
		t.Fatalf("Expected 'not found' error, got: %s", errMsg)
	}
}

// --- E2E-094: read_file nonexistent file ---
func TestFS_E2E094_ReadFileNonexistent(t *testing.T) {
	env := setupFSTest(t, 9289, 0)

	errMsg := callToolExpectError(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "no_such_file.txt",
	})
	if !strings.Contains(errMsg, "not found") {
		t.Fatalf("Expected 'not found' error, got: %s", errMsg)
	}
}

// --- E2E-095: read_file on directory ---
func TestFS_E2E095_ReadFileIsDirectory(t *testing.T) {
	env := setupFSTest(t, 9300, 0)

	os.MkdirAll(filepath.Join(env.rootDir, "adir"), 0755)

	errMsg := callToolExpectError(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "adir",
	})
	if !strings.Contains(errMsg, "directory") {
		t.Fatalf("Expected 'directory' error, got: %s", errMsg)
	}
}

// --- E2E-096: grep with max_depth parameter ---
// max_depth controls directory traversal: dirs with depth > max_depth are skipped.
// Depth is the number of path separators in the relative path from search root.
// d1 → depth=0, d1/d2 → depth=1, d1/d2/d3 → depth=2.
func TestFS_E2E096_GrepMaxDepth(t *testing.T) {
	env := setupFSTest(t, 9301, 0)

	// Create nested directories with matching files at various depths
	os.WriteFile(filepath.Join(env.rootDir, "root.txt"), []byte("MATCH\n"), 0644)
	os.MkdirAll(filepath.Join(env.rootDir, "d1", "d2", "d3"), 0755)
	os.WriteFile(filepath.Join(env.rootDir, "d1", "depth1.txt"), []byte("MATCH\n"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "d1", "d2", "depth2.txt"), []byte("MATCH\n"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "d1", "d2", "d3", "depth3.txt"), []byte("MATCH\n"), 0644)

	// max_depth=0: d1 dir has depth=0, 0 > 0 = false, so d1 is entered.
	// d1/d2 dir has depth=1, 1 > 0 = true, so d2 is skipped.
	// Matches: root.txt + d1/depth1.txt = 2
	result := callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "MATCH", "max_depth": 0,
	})
	totalMatches := int(result["total_matches"].(float64))
	if totalMatches != 2 {
		t.Fatalf("Expected 2 matches at max_depth=0, got %d", totalMatches)
	}

	// max_depth=1: d1 (depth=0) entered, d1/d2 (depth=1, 1>1=false) entered,
	// d1/d2/d3 (depth=2, 2>1=true) skipped.
	// Matches: root.txt, d1/depth1.txt, d1/d2/depth2.txt = 3
	result = callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "MATCH", "max_depth": 1,
	})
	totalMatches = int(result["total_matches"].(float64))
	if totalMatches != 3 {
		t.Fatalf("Expected 3 matches at max_depth=1 (d3 skipped), got %d", totalMatches)
	}
}

// --- E2E-097: grep in subdirectory path ---
func TestFS_E2E097_GrepSubdirectory(t *testing.T) {
	env := setupFSTest(t, 9302, 0)

	os.WriteFile(filepath.Join(env.rootDir, "top.txt"), []byte("FINDME\n"), 0644)
	os.MkdirAll(filepath.Join(env.rootDir, "sub"), 0755)
	os.WriteFile(filepath.Join(env.rootDir, "sub", "inner.txt"), []byte("FINDME\n"), 0644)

	// Search only in "sub" subdirectory
	result := callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "FINDME", "path": "sub",
	})
	totalMatches := int(result["total_matches"].(float64))
	if totalMatches != 1 {
		t.Fatalf("Expected 1 match in sub dir, got %d", totalMatches)
	}
	matches := result["matches"].([]any)
	m := matches[0].(map[string]any)
	if m["file"].(string) != "inner.txt" {
		t.Fatalf("Expected match in inner.txt, got %q", m["file"])
	}
}

// --- E2E-098: glob with max_depth parameter ---
// max_depth controls directory traversal: dirs with depth > max_depth are skipped.
// Depth is computed as the number of path separators in the relative path.
// d1 → depth=0, d1/d2 → depth=1, d1/d2/d3 → depth=2.
func TestFS_E2E098_GlobMaxDepth(t *testing.T) {
	env := setupFSTest(t, 9303, 0)

	os.WriteFile(filepath.Join(env.rootDir, "root.go"), []byte("a"), 0644)
	os.MkdirAll(filepath.Join(env.rootDir, "d1", "d2"), 0755)
	os.WriteFile(filepath.Join(env.rootDir, "d1", "level1.go"), []byte("b"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "d1", "d2", "level2.go"), []byte("c"), 0644)

	// max_depth=0: d1 dir has depth=0 (rel="d1", 0 separators). 0 > 0 = false, so d1 is entered.
	// d1/d2 dir has depth=1 (rel="d1/d2"). 1 > 0 = true, so d2 is skipped.
	// Matches: root.go + d1/level1.go = 2 .go files
	result := callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "workspace", "pattern": "*.go", "max_depth": 0,
	})
	totalMatches := int(result["total_matches"].(float64))
	if totalMatches != 2 {
		t.Fatalf("Expected 2 matches at max_depth=0, got %d", totalMatches)
	}
}

// --- E2E-099: glob in subdirectory path ---
func TestFS_E2E099_GlobSubdirectory(t *testing.T) {
	env := setupFSTest(t, 9304, 0)

	os.WriteFile(filepath.Join(env.rootDir, "top.go"), []byte("a"), 0644)
	os.MkdirAll(filepath.Join(env.rootDir, "pkg"), 0755)
	os.WriteFile(filepath.Join(env.rootDir, "pkg", "inner.go"), []byte("b"), 0644)

	result := callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "workspace", "pattern": "*.go", "path": "pkg",
	})
	totalMatches := int(result["total_matches"].(float64))
	if totalMatches != 1 {
		t.Fatalf("Expected 1 match in pkg subdir, got %d", totalMatches)
	}
	matches := result["matches"].([]any)
	m := matches[0].(map[string]any)
	if m["path"].(string) != "inner.go" {
		t.Fatalf("Expected inner.go, got %q", m["path"])
	}
}

// --- E2E-100: glob max_results truncation ---
func TestFS_E2E100_GlobMaxResults(t *testing.T) {
	env := setupFSTest(t, 9305, 0)

	// Create 10 files
	for i := range 10 {
		os.WriteFile(filepath.Join(env.rootDir, fmt.Sprintf("file_%02d.txt", i)), []byte("x"), 0644)
	}

	result := callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "workspace", "pattern": "*.txt", "max_results": 3,
	})
	totalMatches := int(result["total_matches"].(float64))
	if totalMatches != 3 {
		t.Fatalf("Expected 3 matches, got %d", totalMatches)
	}
	if result["truncated"] != true {
		t.Fatal("Expected truncated=true")
	}
}

// --- E2E-101: move directory within same root ---
func TestFS_E2E101_MoveDirectory(t *testing.T) {
	env := setupFSTest(t, 9306, 0)

	os.MkdirAll(filepath.Join(env.rootDir, "src", "sub"), 0755)
	os.WriteFile(filepath.Join(env.rootDir, "src", "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(env.rootDir, "src", "sub", "b.txt"), []byte("b"), 0644)

	callToolExpectSuccess(t, env.client, "move", map[string]any{
		"source_root": "workspace", "source_path": "src",
		"dest_root": "workspace", "dest_path": "dst",
	})

	// Source should not exist
	callToolExpectError(t, env.client, "stat_file", map[string]any{
		"root": "workspace", "path": "src",
	})

	// Destination should have the content
	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "dst/a.txt",
	})
	if result["content"].(string) != "a" {
		t.Fatal("Moved directory content mismatch for a.txt")
	}

	result = callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "dst/sub/b.txt",
	})
	if result["content"].(string) != "b" {
		t.Fatal("Moved directory content mismatch for sub/b.txt")
	}
}

// --- E2E-102: write_file default mode (no mode param) acts as overwrite ---
func TestFS_E2E102_WriteFileDefaultMode(t *testing.T) {
	env := setupFSTest(t, 9307, 0)

	os.WriteFile(filepath.Join(env.rootDir, "default.txt"), []byte("original"), 0644)

	// Call without specifying mode — should default to overwrite
	result := callToolExpectSuccess(t, env.client, "write_file", map[string]any{
		"root": "workspace", "path": "default.txt", "content": "replaced",
	})
	if result["mode"].(string) != "overwrite" {
		t.Fatalf("Expected mode=overwrite, got %q", result["mode"])
	}

	readResult := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "default.txt",
	})
	if readResult["content"].(string) != "replaced" {
		t.Fatalf("Expected 'replaced', got %q", readResult["content"])
	}
}

// --- E2E-103: write_file missing content parameter ---
func TestFS_E2E103_WriteFileMissingContent(t *testing.T) {
	env := setupFSTest(t, 9308, 0)

	errMsg := callToolExpectError(t, env.client, "write_file", map[string]any{
		"root": "workspace", "path": "test.txt",
	})
	if !strings.Contains(errMsg, "content") && !strings.Contains(errMsg, "required") {
		t.Fatalf("Expected 'content required' error, got: %s", errMsg)
	}
}

// --- E2E-104: patch_file with multiple hunks ---
func TestFS_E2E104_PatchFileMultipleHunks(t *testing.T) {
	env := setupFSTest(t, 9309, 0)

	original := "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7\nline 8\nline 9\nline 10\n"
	os.WriteFile(filepath.Join(env.rootDir, "multi.txt"), []byte(original), 0644)

	// Patch modifies line 2 and line 9 in two separate hunks
	patch := `--- a/multi.txt
+++ b/multi.txt
@@ -1,3 +1,3 @@
 line 1
-line 2
+line TWO
 line 3
@@ -8,3 +8,3 @@
 line 8
-line 9
+line NINE
 line 10`
	result := callToolExpectSuccess(t, env.client, "patch_file", map[string]any{
		"root": "workspace", "path": "multi.txt", "patch": patch,
	})
	if result["hunks_applied"].(float64) != 2 {
		t.Fatalf("Expected 2 hunks applied, got %v", result["hunks_applied"])
	}

	readResult := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "multi.txt",
	})
	content := readResult["content"].(string)
	if !strings.Contains(content, "line TWO") {
		t.Fatal("Expected 'line TWO' in patched content")
	}
	if !strings.Contains(content, "line NINE") {
		t.Fatal("Expected 'line NINE' in patched content")
	}
	// Original unchanged lines should still be present
	if !strings.Contains(content, "line 5") {
		t.Fatal("Expected 'line 5' (unchanged) in patched content")
	}
}

// --- E2E-105: patch_file missing patch parameter ---
func TestFS_E2E105_PatchFileMissingPatch(t *testing.T) {
	env := setupFSTest(t, 9310, 0)

	os.WriteFile(filepath.Join(env.rootDir, "file.txt"), []byte("data\n"), 0644)

	errMsg := callToolExpectError(t, env.client, "patch_file", map[string]any{
		"root": "workspace", "path": "file.txt",
	})
	if !strings.Contains(errMsg, "patch") && !strings.Contains(errMsg, "required") {
		t.Fatalf("Expected 'patch required' error, got: %s", errMsg)
	}
}

// --- E2E-106: copy creates nested parent directories at destination ---
func TestFS_E2E106_CopyCreatesParentDirs(t *testing.T) {
	env := setupFSTest(t, 9311, 0)

	os.WriteFile(filepath.Join(env.rootDir, "original.txt"), []byte("nested copy"), 0644)

	callToolExpectSuccess(t, env.client, "copy", map[string]any{
		"source_root": "workspace", "source_path": "original.txt",
		"dest_root": "workspace", "dest_path": "deep/nested/copy.txt",
	})

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "deep/nested/copy.txt",
	})
	if result["content"].(string) != "nested copy" {
		t.Fatal("Nested copy content mismatch")
	}
}

// --- E2E-107: glob neither pattern nor regex provided ---
func TestFS_E2E107_GlobNeitherPatternNorRegex(t *testing.T) {
	env := setupFSTest(t, 9312, 0)

	errMsg := callToolExpectError(t, env.client, "glob", map[string]any{
		"root": "workspace",
	})
	if !strings.Contains(errMsg, "exactly one") {
		t.Fatalf("Expected 'exactly one' error, got: %s", errMsg)
	}
}

// --- E2E-108: grep with context_lines shows before and after context ---
func TestFS_E2E108_GrepContextContent(t *testing.T) {
	env := setupFSTest(t, 9313, 0)

	content := "alpha\nbeta\ngamma\ndelta\nepsilon\n"
	os.WriteFile(filepath.Join(env.rootDir, "greek.txt"), []byte(content), 0644)

	result := callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "workspace", "pattern": "gamma", "context_lines": 1,
	})
	matches := result["matches"].([]any)
	if len(matches) != 1 {
		t.Fatalf("Expected 1 match, got %d", len(matches))
	}
	m := matches[0].(map[string]any)

	// Check context_before contains "beta"
	before := m["context_before"].([]any)
	if len(before) != 1 || before[0].(string) != "beta" {
		t.Fatalf("Expected context_before=[beta], got %v", before)
	}

	// Check context_after contains "delta"
	after := m["context_after"].([]any)
	if len(after) != 1 || after[0].(string) != "delta" {
		t.Fatalf("Expected context_after=[delta], got %v", after)
	}
}

// --- E2E-109: read_file truncated flag on byte-based read ---
// The "truncated" flag in byte mode indicates that the actual bytes read were
// less than the requested limit (i.e., we hit EOF before filling the buffer).
func TestFS_E2E109_ReadFileByteTruncated(t *testing.T) {
	env := setupFSTest(t, 9314, 0)

	content := "abcdefghij" // 10 bytes
	os.WriteFile(filepath.Join(env.rootDir, "trunc.txt"), []byte(content), 0644)

	// Read exactly 5 bytes — all 5 are available, so truncated=false
	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "trunc.txt", "offset_bytes": 0, "limit_bytes": 5,
	})
	if result["content"].(string) != "abcde" {
		t.Fatalf("Expected 'abcde', got %q", result["content"])
	}
	if result["truncated"] != false {
		t.Fatal("Expected truncated=false when all requested bytes are available")
	}

	// Request more bytes than available from offset 8 (only 2 bytes remain)
	result = callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "trunc.txt", "offset_bytes": 8, "limit_bytes": 10,
	})
	if result["content"].(string) != "ij" {
		t.Fatalf("Expected 'ij', got %q", result["content"])
	}
	if result["truncated"] != true {
		t.Fatal("Expected truncated=true when fewer bytes available than requested")
	}
}

// --- E2E-110: read_file line-based truncated flag ---
func TestFS_E2E110_ReadFileLineTruncated(t *testing.T) {
	env := setupFSTest(t, 9315, 0)

	content := "line1\nline2\nline3\nline4\nline5\n"
	os.WriteFile(filepath.Join(env.rootDir, "lines.txt"), []byte(content), 0644)

	// Read 2 lines from 5-line file
	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "lines.txt", "offset_lines": 1, "limit_lines": 2,
	})
	if result["truncated"] != true {
		t.Fatal("Expected truncated=true when limit_lines < total lines")
	}
	if result["lines_total"].(float64) != 5 {
		t.Fatalf("Expected lines_total=5, got %v", result["lines_total"])
	}

	// Read all lines — should NOT be truncated
	result = callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "lines.txt", "offset_lines": 1, "limit_lines": 10,
	})
	if result["truncated"] != false {
		t.Fatal("Expected truncated=false when all lines returned")
	}
}

// --- E2E-111: allowlist enforcement on multiple tools ---
func TestFS_E2E111_AllowlistMultipleTools(t *testing.T) {
	env := setupFSTest(t, 9316, 0)

	// The "restricted" root only allows list_folder. Verify multiple tool types are blocked.

	// remove_file should be blocked
	errMsg := callToolExpectError(t, env.client, "remove_file", map[string]any{
		"root": "restricted", "path": "test.txt",
	})
	if !strings.Contains(errMsg, "not allowed") {
		t.Fatalf("Expected 'not allowed' for remove_file on restricted, got: %s", errMsg)
	}

	// grep should be blocked
	errMsg = callToolExpectError(t, env.client, "grep", map[string]any{
		"root": "restricted", "pattern": "test",
	})
	if !strings.Contains(errMsg, "not allowed") {
		t.Fatalf("Expected 'not allowed' for grep on restricted, got: %s", errMsg)
	}

	// stat_file should be blocked
	errMsg = callToolExpectError(t, env.client, "stat_file", map[string]any{
		"root": "restricted", "path": "test.txt",
	})
	if !strings.Contains(errMsg, "not allowed") {
		t.Fatalf("Expected 'not allowed' for stat_file on restricted, got: %s", errMsg)
	}

	// But list_folder should work
	callToolExpectSuccess(t, env.client, "list_folder", map[string]any{
		"root": "restricted", "path": ".",
	})
}

// --- E2E-112: readonly root allows read-only tools ---
func TestFS_E2E112_ReadonlyRootReadTools(t *testing.T) {
	env := setupFSTest(t, 9317, 0)

	// The "readonly" root allows: list_folder, read_file, stat_file, grep, glob
	// Create a file in the readonly root directory directly
	readonlyDir := filepath.Join(filepath.Dir(env.rootDir), "readonly")
	os.WriteFile(filepath.Join(readonlyDir, "readme.txt"), []byte("readonly data\n"), 0644)

	// read_file should work
	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "readonly", "path": "readme.txt",
	})
	if result["content"].(string) != "readonly data\n" {
		t.Fatalf("Expected 'readonly data\\n', got %q", result["content"])
	}

	// stat_file should work
	callToolExpectSuccess(t, env.client, "stat_file", map[string]any{
		"root": "readonly", "path": "readme.txt",
	})

	// grep should work
	grepResult := callToolExpectSuccess(t, env.client, "grep", map[string]any{
		"root": "readonly", "pattern": "readonly",
	})
	if grepResult["total_matches"].(float64) != 1 {
		t.Fatalf("Expected 1 grep match, got %v", grepResult["total_matches"])
	}

	// glob should work
	globResult := callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "readonly", "pattern": "*.txt",
	})
	if globResult["total_matches"].(float64) != 1 {
		t.Fatalf("Expected 1 glob match, got %v", globResult["total_matches"])
	}

	// write_file should be blocked
	errMsg := callToolExpectError(t, env.client, "write_file", map[string]any{
		"root": "readonly", "path": "test.txt", "content": "nope",
	})
	if !strings.Contains(errMsg, "not allowed") {
		t.Fatalf("Expected 'not allowed' for write_file on readonly, got: %s", errMsg)
	}

	// remove_file should be blocked
	errMsg = callToolExpectError(t, env.client, "remove_file", map[string]any{
		"root": "readonly", "path": "readme.txt",
	})
	if !strings.Contains(errMsg, "not allowed") {
		t.Fatalf("Expected 'not allowed' for remove_file on readonly, got: %s", errMsg)
	}
}

// --- E2E-113: path traversal variants ---
func TestFS_E2E113_PathTraversalVariants(t *testing.T) {
	env := setupFSTest(t, 9318, 0)

	// Various path traversal attempts
	traversals := []string{
		"../../../etc/passwd",
		"sub/../../..",
		"sub/../../../etc/hosts",
	}
	for _, path := range traversals {
		errMsg := callToolExpectError(t, env.client, "read_file", map[string]any{
			"root": "workspace", "path": path,
		})
		if !strings.Contains(errMsg, "outside root") && !strings.Contains(errMsg, "not found") {
			t.Fatalf("Expected security error for path %q, got: %s", path, errMsg)
		}
	}
}

// --- E2E-114: write_file and read_file with special characters ---
func TestFS_E2E114_SpecialCharacterContent(t *testing.T) {
	env := setupFSTest(t, 9319, 0)

	// Content with unicode, newlines, special characters
	specialContent := "Hello World\n\tTabbed line\nEmoji in content\nJSON: {\"key\": \"value\"}\nPath: /usr/bin\n"

	callToolExpectSuccess(t, env.client, "write_file", map[string]any{
		"root": "workspace", "path": "special.txt", "content": specialContent,
	})

	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "special.txt",
	})
	if result["content"].(string) != specialContent {
		t.Fatalf("Content with special characters mismatch.\nExpected: %q\nGot: %q", specialContent, result["content"])
	}
}

// --- E2E-115: move to destination with nested parent creation ---
func TestFS_E2E115_MoveCreatesParentDirs(t *testing.T) {
	env := setupFSTest(t, 9320, 0)

	os.WriteFile(filepath.Join(env.rootDir, "moveme.txt"), []byte("move to nested"), 0644)

	callToolExpectSuccess(t, env.client, "move", map[string]any{
		"source_root": "workspace", "source_path": "moveme.txt",
		"dest_root": "workspace", "dest_path": "deep/nested/moveme.txt",
	})

	// Old path gone
	callToolExpectError(t, env.client, "stat_file", map[string]any{
		"root": "workspace", "path": "moveme.txt",
	})

	// New path has content
	result := callToolExpectSuccess(t, env.client, "read_file", map[string]any{
		"root": "workspace", "path": "deep/nested/moveme.txt",
	})
	if result["content"].(string) != "move to nested" {
		t.Fatal("Move to nested directory content mismatch")
	}
}

// --- E2E-116: glob type_filter directory ---
func TestFS_E2E116_GlobTypeFilterDirectory(t *testing.T) {
	env := setupFSTest(t, 9321, 0)

	os.WriteFile(filepath.Join(env.rootDir, "file.txt"), []byte("f"), 0644)
	os.MkdirAll(filepath.Join(env.rootDir, "dir1"), 0755)
	os.MkdirAll(filepath.Join(env.rootDir, "dir2"), 0755)

	result := callToolExpectSuccess(t, env.client, "glob", map[string]any{
		"root": "workspace", "pattern": "*", "type_filter": "directory",
	})
	totalMatches := int(result["total_matches"].(float64))
	if totalMatches != 2 {
		t.Fatalf("Expected 2 directory matches, got %d", totalMatches)
	}
	matches := result["matches"].([]any)
	for _, m := range matches {
		if m.(map[string]any)["type"].(string) != "directory" {
			t.Fatal("Expected only directories")
		}
	}
}

// --- E2E-117: stat_file returns correct size for file ---
func TestFS_E2E117_StatFileSize(t *testing.T) {
	env := setupFSTest(t, 9322, 0)

	content := "exactly 26 bytes of text.\n"
	os.WriteFile(filepath.Join(env.rootDir, "sized.txt"), []byte(content), 0644)

	result := callToolExpectSuccess(t, env.client, "stat_file", map[string]any{
		"root": "workspace", "path": "sized.txt",
	})
	size := int64(result["size"].(float64))
	if size != int64(len(content)) {
		t.Fatalf("Expected size=%d, got %d", len(content), size)
	}
	if result["modified_at"] == nil || result["modified_at"].(string) == "" {
		t.Fatal("Expected non-empty modified_at")
	}
}

// --- E2E-118: patch_file with empty/invalid patch format ---
func TestFS_E2E118_PatchFileInvalidFormat(t *testing.T) {
	env := setupFSTest(t, 9323, 0)

	os.WriteFile(filepath.Join(env.rootDir, "file.txt"), []byte("data\n"), 0644)

	// Patch with no hunks
	errMsg := callToolExpectError(t, env.client, "patch_file", map[string]any{
		"root": "workspace", "path": "file.txt", "patch": "this is not a valid patch",
	})
	if !strings.Contains(errMsg, "no hunks") {
		t.Fatalf("Expected 'no hunks' error, got: %s", errMsg)
	}
}

// --- E2E-119: copy preserves file content integrity across roots ---
func TestFS_E2E119_CopyIntegrity(t *testing.T) {
	env := setupFSTest(t, 9324, 0)

	// Write a file with known content, copy cross-root, verify hash matches
	content := "integrity check content with various data: 1234567890!@#$%^&*()\n"
	os.WriteFile(filepath.Join(env.sourceDir, "integrity.txt"), []byte(content), 0644)

	// Hash the source
	srcHash := callToolExpectSuccess(t, env.client, "hash_file", map[string]any{
		"root": "source", "path": "integrity.txt", "algorithm": "sha256",
	})

	// Copy
	callToolExpectSuccess(t, env.client, "copy", map[string]any{
		"source_root": "source", "source_path": "integrity.txt",
		"dest_root": "dest", "dest_path": "integrity.txt",
	})

	// Hash the dest
	dstHash := callToolExpectSuccess(t, env.client, "hash_file", map[string]any{
		"root": "dest", "path": "integrity.txt", "algorithm": "sha256",
	})

	if srcHash["hash"].(string) != dstHash["hash"].(string) {
		t.Fatalf("Hash mismatch after copy: src=%s, dst=%s", srcHash["hash"], dstHash["hash"])
	}
}
