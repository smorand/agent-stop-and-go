package filesystem

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server holds the filesystem MCP server state.
type Server struct {
	roots           map[string]*ResolvedRoot
	maxFullReadSize int64
}

// NewServer creates a new filesystem server with resolved roots.
func NewServer(roots map[string]*ResolvedRoot, maxFullReadSize int64) *Server {
	return &Server{
		roots:           roots,
		maxFullReadSize: maxFullReadSize,
	}
}

// helper: get required string param
func reqString(req mcp.CallToolRequest, key string) (string, error) {
	v := req.GetString(key, "")
	if v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}

// helper: get optional int param
func optInt(req mcp.CallToolRequest, key string, def int) int {
	v := req.GetInt(key, def)
	return v
}

// helper: get optional bool param
func optBool(req mcp.CallToolRequest, key string, def bool) bool {
	return req.GetBool(key, def)
}

// helper: resolve root, check allowlist, validate path
func (s *Server) resolveAndValidate(req mcp.CallToolRequest, toolName string) (*ResolvedRoot, string, error) {
	rootName, err := reqString(req, "root")
	if err != nil {
		return nil, "", err
	}
	root, err := GetRoot(s.roots, rootName)
	if err != nil {
		return nil, "", err
	}
	if err := CheckAllowlist(root, toolName); err != nil {
		return nil, "", err
	}
	pathStr, err := reqString(req, "path")
	if err != nil {
		return nil, "", err
	}
	resolved, err := ValidatePath(root, pathStr)
	if err != nil {
		return nil, "", err
	}
	return root, resolved, nil
}

// helper: JSON text result
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal result: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// helper: log tool call
func logTool(toolName, rootName, path string, dur time.Duration, err error) {
	if err != nil {
		slog.Warn("tool call failed", "tool", toolName, "root", rootName, "path", path, "duration", dur, "error", err)
	} else {
		slog.Info("tool call", "tool", toolName, "root", rootName, "path", path, "duration", dur)
	}
}

// ListRoots returns all configured roots with names and allowed tools.
func (s *Server) ListRoots() server.ToolHandlerFunc {
	return func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		type rootInfo struct {
			Name         string   `json:"name"`
			AllowedTools []string `json:"allowed_tools"`
		}
		var result []rootInfo
		for _, r := range s.roots {
			tools := make([]string, 0, len(r.AllowedTools))
			for t := range r.AllowedTools {
				tools = append(tools, t)
			}
			sort.Strings(tools)
			result = append(result, rootInfo{Name: r.Name, AllowedTools: tools})
		}
		sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
		logTool("list_roots", "", "", time.Since(start), nil)
		return jsonResult(result)
	}
}

// ListFolder lists directory contents.
func (s *Server) ListFolder() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		root, resolved, err := s.resolveAndValidate(req, "list_folder")
		if err != nil {
			logTool("list_folder", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		info, err := os.Stat(resolved)
		if err != nil {
			logTool("list_folder", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("path not found: %s", req.GetString("path", ""))), nil
		}
		if !info.IsDir() {
			err := fmt.Errorf("path is not a directory: %s", req.GetString("path", ""))
			logTool("list_folder", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		entries, err := os.ReadDir(resolved)
		if err != nil {
			logTool("list_folder", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("read directory: %v", err)), nil
		}

		type entry struct {
			Name       string `json:"name"`
			Type       string `json:"type"`
			Size       int64  `json:"size"`
			ModifiedAt string `json:"modified_at"`
			TargetType string `json:"target_type,omitempty"`
		}

		result := make([]entry, 0, len(entries))
		for _, e := range entries {
			fi, fiErr := e.Info()
			if fiErr != nil {
				continue
			}
			ent := entry{
				Name:       e.Name(),
				Size:       fi.Size(),
				ModifiedAt: fi.ModTime().UTC().Format(time.RFC3339),
			}

			if e.Type()&fs.ModeSymlink != 0 {
				ent.Type = "symlink"
				// Check if symlink target is within root
				linkPath := filepath.Join(resolved, e.Name())
				target, evalErr := filepath.EvalSymlinks(linkPath)
				if evalErr == nil {
					target, _ = filepath.Abs(target)
					if isWithinRoot(target, root.RealPath) {
						tInfo, tErr := os.Stat(target)
						if tErr == nil {
							if tInfo.IsDir() {
								ent.TargetType = "directory"
							} else {
								ent.TargetType = "file"
							}
						}
					} else {
						ent.TargetType = "external"
					}
				}
			} else if e.IsDir() {
				ent.Type = "directory"
			} else {
				ent.Type = "file"
			}
			result = append(result, ent)
		}

		logTool("list_folder", root.Name, resolved, time.Since(start), nil)
		return jsonResult(map[string]any{"entries": result, "count": len(result)})
	}
}

// ReadFile reads file content fully or partially.
func (s *Server) ReadFile() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		root, resolved, err := s.resolveAndValidate(req, "read_file")
		if err != nil {
			logTool("read_file", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Check mutual exclusivity
		offsetBytes := req.GetInt("offset_bytes", -1)
		limitBytes := req.GetInt("limit_bytes", -1)
		offsetLines := req.GetInt("offset_lines", -1)
		limitLines := req.GetInt("limit_lines", -1)

		hasByte := offsetBytes >= 0 || limitBytes >= 0
		hasLine := offsetLines >= 0 || limitLines >= 0

		if hasByte && hasLine {
			err := fmt.Errorf("byte-based and line-based parameters are mutually exclusive")
			logTool("read_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		info, err := os.Stat(resolved)
		if err != nil {
			logTool("read_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("file not found: %s", req.GetString("path", ""))), nil
		}
		if info.IsDir() {
			err := fmt.Errorf("path is a directory, not a file: %s", req.GetString("path", ""))
			logTool("read_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		fileSize := info.Size()

		// Full read check
		if !hasByte && !hasLine {
			if fileSize > s.maxFullReadSize {
				err := fmt.Errorf("file too large for full read (size: %s, limit: %s); use offset/limit parameters",
					formatSize(fileSize), formatSize(s.maxFullReadSize))
				logTool("read_file", root.Name, resolved, time.Since(start), err)
				return mcp.NewToolResultError(err.Error()), nil
			}
		}

		f, err := os.Open(resolved)
		if err != nil {
			logTool("read_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("open file: %v", err)), nil
		}
		defer f.Close()

		result := map[string]any{
			"size": fileSize,
		}

		// Check binary
		isBinary := false
		header := make([]byte, 8192)
		n, _ := f.Read(header)
		if n > 0 {
			for i := range n {
				if header[i] == 0 {
					isBinary = true
					break
				}
			}
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			logTool("read_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("seek file: %v", err)), nil
		}

		if isBinary {
			result["binary"] = true
		}

		if hasByte {
			ob := int64(0)
			if offsetBytes >= 0 {
				ob = int64(offsetBytes)
			}
			lb := fileSize - ob
			if limitBytes >= 0 {
				lb = int64(limitBytes)
			}

			if _, err := f.Seek(ob, io.SeekStart); err != nil {
				logTool("read_file", root.Name, resolved, time.Since(start), err)
				return mcp.NewToolResultError(fmt.Sprintf("seek: %v", err)), nil
			}
			data := make([]byte, lb)
			nRead, readErr := io.ReadFull(f, data)
			if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
				logTool("read_file", root.Name, resolved, time.Since(start), readErr)
				return mcp.NewToolResultError(fmt.Sprintf("read: %v", readErr)), nil
			}
			result["content"] = string(data[:nRead])
			result["truncated"] = int64(nRead) < lb
		} else if hasLine {
			content, err := io.ReadAll(f)
			if err != nil {
				logTool("read_file", root.Name, resolved, time.Since(start), err)
				return mcp.NewToolResultError(fmt.Sprintf("read: %v", err)), nil
			}
			allLines := strings.Split(string(content), "\n")
			// Remove trailing empty line from final newline
			if len(allLines) > 0 && allLines[len(allLines)-1] == "" {
				allLines = allLines[:len(allLines)-1]
			}
			totalLines := len(allLines)
			result["lines_total"] = totalLines

			ol := 1
			if offsetLines >= 0 {
				ol = offsetLines
			}
			ll := totalLines
			if limitLines >= 0 {
				ll = limitLines
			}

			startLine := ol - 1 // Convert to 0-based
			if startLine < 0 {
				startLine = 0
			}
			if startLine >= totalLines {
				result["content"] = ""
				result["truncated"] = false
			} else {
				endLine := startLine + ll
				if endLine > totalLines {
					endLine = totalLines
				}
				selectedLines := allLines[startLine:endLine]
				result["content"] = strings.Join(selectedLines, "\n")
				result["truncated"] = endLine < totalLines
			}
		} else {
			// Full read
			content, err := io.ReadAll(f)
			if err != nil {
				logTool("read_file", root.Name, resolved, time.Since(start), err)
				return mcp.NewToolResultError(fmt.Sprintf("read: %v", err)), nil
			}
			result["content"] = string(content)
			result["truncated"] = false
		}

		logTool("read_file", root.Name, resolved, time.Since(start), nil)
		return jsonResult(result)
	}
}

// WriteFile writes content to a file.
func (s *Server) WriteFile() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		root, resolved, err := s.resolveAndValidate(req, "write_file")
		if err != nil {
			logTool("write_file", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		content, err := reqString(req, "content")
		if err != nil {
			logTool("write_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		mode := req.GetString("mode", "overwrite")
		switch mode {
		case "overwrite", "append", "create_only":
			// valid
		default:
			err := fmt.Errorf("invalid write mode: %s; valid modes: overwrite, append, create_only", mode)
			logTool("write_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Auto-create parent directories
		if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
			logTool("write_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("create parent directories: %v", err)), nil
		}

		if mode == "create_only" {
			if _, err := os.Stat(resolved); err == nil {
				err := fmt.Errorf("file already exists: %s; use overwrite mode to replace", req.GetString("path", ""))
				logTool("write_file", root.Name, resolved, time.Since(start), err)
				return mcp.NewToolResultError(err.Error()), nil
			}
		}

		var flag int
		switch mode {
		case "overwrite":
			flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		case "append":
			flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		case "create_only":
			flag = os.O_WRONLY | os.O_CREATE | os.O_EXCL
		}

		f, err := os.OpenFile(resolved, flag, 0644)
		if err != nil {
			logTool("write_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("open file: %v", err)), nil
		}
		defer f.Close()

		n, err := f.WriteString(content)
		if err != nil {
			logTool("write_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("write: %v", err)), nil
		}

		logTool("write_file", root.Name, resolved, time.Since(start), nil)
		return jsonResult(map[string]any{
			"path": req.GetString("path", ""),
			"size": n,
			"mode": mode,
		})
	}
}

// RemoveFile deletes a single file.
func (s *Server) RemoveFile() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		root, resolved, err := s.resolveAndValidate(req, "remove_file")
		if err != nil {
			logTool("remove_file", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		info, err := os.Lstat(resolved)
		if err != nil {
			logTool("remove_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("file not found: %s", req.GetString("path", ""))), nil
		}
		if info.IsDir() {
			err := fmt.Errorf("path is a directory, use remove_folder instead: %s", req.GetString("path", ""))
			logTool("remove_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		if err := os.Remove(resolved); err != nil {
			logTool("remove_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("remove: %v", err)), nil
		}

		logTool("remove_file", root.Name, resolved, time.Since(start), nil)
		return jsonResult(map[string]any{
			"path":    req.GetString("path", ""),
			"removed": true,
		})
	}
}

// PatchFile applies a unified diff to a file.
func (s *Server) PatchFile() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		root, resolved, err := s.resolveAndValidate(req, "patch_file")
		if err != nil {
			logTool("patch_file", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		patchStr, err := reqString(req, "patch")
		if err != nil {
			logTool("patch_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		hunks, err := ParsePatch(patchStr)
		if err != nil {
			logTool("patch_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("parse patch: %v", err)), nil
		}

		// Read existing content (or empty if file doesn't exist)
		original := ""
		if data, err := os.ReadFile(resolved); err == nil {
			original = string(data)
		}

		patched, err := ApplyPatch(original, hunks)
		if err != nil {
			logTool("patch_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Auto-create parent directories
		if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
			logTool("patch_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("create parent directories: %v", err)), nil
		}

		if err := os.WriteFile(resolved, []byte(patched), 0644); err != nil {
			logTool("patch_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("write patched file: %v", err)), nil
		}

		logTool("patch_file", root.Name, resolved, time.Since(start), nil)
		return jsonResult(map[string]any{
			"path":          req.GetString("path", ""),
			"hunks_applied": len(hunks),
		})
	}
}

// CreateFolder creates a directory (and parents).
func (s *Server) CreateFolder() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		root, resolved, err := s.resolveAndValidate(req, "create_folder")
		if err != nil {
			logTool("create_folder", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Check if path exists and is a file
		if info, err := os.Stat(resolved); err == nil {
			if !info.IsDir() {
				err := fmt.Errorf("path exists as a file: %s", req.GetString("path", ""))
				logTool("create_folder", root.Name, resolved, time.Since(start), err)
				return mcp.NewToolResultError(err.Error()), nil
			}
			// Already a directory — idempotent
			logTool("create_folder", root.Name, resolved, time.Since(start), nil)
			return jsonResult(map[string]any{
				"path":    req.GetString("path", ""),
				"created": true,
			})
		}

		if err := os.MkdirAll(resolved, 0755); err != nil {
			logTool("create_folder", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("create directory: %v", err)), nil
		}

		logTool("create_folder", root.Name, resolved, time.Since(start), nil)
		return jsonResult(map[string]any{
			"path":    req.GetString("path", ""),
			"created": true,
		})
	}
}

// RemoveFolder removes a directory recursively.
func (s *Server) RemoveFolder() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		root, resolved, err := s.resolveAndValidate(req, "remove_folder")
		if err != nil {
			logTool("remove_folder", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		// Cannot remove root directory itself
		if resolved == root.RealPath {
			err := fmt.Errorf("cannot remove root directory")
			logTool("remove_folder", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		info, err := os.Lstat(resolved)
		if err != nil {
			logTool("remove_folder", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("directory not found: %s", req.GetString("path", ""))), nil
		}
		if !info.IsDir() {
			err := fmt.Errorf("path is a file, use remove_file instead: %s", req.GetString("path", ""))
			logTool("remove_folder", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		if err := os.RemoveAll(resolved); err != nil {
			logTool("remove_folder", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("remove: %v", err)), nil
		}

		logTool("remove_folder", root.Name, resolved, time.Since(start), nil)
		return jsonResult(map[string]any{
			"path":    req.GetString("path", ""),
			"removed": true,
		})
	}
}

// StatFile returns file/directory metadata.
func (s *Server) StatFile() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		root, resolved, err := s.resolveAndValidate(req, "stat_file")
		if err != nil {
			logTool("stat_file", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		linfo, err := os.Lstat(resolved)
		if err != nil {
			logTool("stat_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("not found: %s", req.GetString("path", ""))), nil
		}

		result := map[string]any{
			"name":         linfo.Name(),
			"path":         req.GetString("path", ""),
			"size":         linfo.Size(),
			"is_directory": linfo.IsDir(),
			"is_symlink":   linfo.Mode()&fs.ModeSymlink != 0,
			"modified_at":  linfo.ModTime().UTC().Format(time.RFC3339),
		}

		// If it's a symlink, also stat the target
		if linfo.Mode()&fs.ModeSymlink != 0 {
			targetInfo, tErr := os.Stat(resolved)
			if tErr == nil {
				result["size"] = targetInfo.Size()
				result["is_directory"] = targetInfo.IsDir()
			}
		}

		logTool("stat_file", root.Name, resolved, time.Since(start), nil)
		return jsonResult(result)
	}
}

// HashFile computes a cryptographic hash.
func (s *Server) HashFile() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		root, resolved, err := s.resolveAndValidate(req, "hash_file")
		if err != nil {
			logTool("hash_file", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		algorithm, err := reqString(req, "algorithm")
		if err != nil {
			logTool("hash_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		var h hash.Hash
		switch algorithm {
		case "md5":
			h = md5.New()
		case "sha1":
			h = sha1.New()
		case "sha256":
			h = sha256.New()
		default:
			err := fmt.Errorf("unsupported hash algorithm: %s; supported: md5, sha1, sha256", algorithm)
			logTool("hash_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		info, err := os.Stat(resolved)
		if err != nil {
			logTool("hash_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("file not found: %s", req.GetString("path", ""))), nil
		}
		if info.IsDir() {
			err := fmt.Errorf("cannot hash a directory: %s", req.GetString("path", ""))
			logTool("hash_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		f, err := os.Open(resolved)
		if err != nil {
			logTool("hash_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("open: %v", err)), nil
		}
		defer f.Close()

		if _, err := io.Copy(h, f); err != nil {
			logTool("hash_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("hash: %v", err)), nil
		}

		logTool("hash_file", root.Name, resolved, time.Since(start), nil)
		return jsonResult(map[string]any{
			"path":      req.GetString("path", ""),
			"algorithm": algorithm,
			"hash":      fmt.Sprintf("%x", h.Sum(nil)),
			"size":      info.Size(),
		})
	}
}

// PermissionsFile returns file permissions info.
func (s *Server) PermissionsFile() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		root, resolved, err := s.resolveAndValidate(req, "permissions_file")
		if err != nil {
			logTool("permissions_file", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		info, err := os.Lstat(resolved)
		if err != nil {
			logTool("permissions_file", root.Name, resolved, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("not found: %s", req.GetString("path", ""))), nil
		}

		mode := info.Mode().Perm()
		result := map[string]any{
			"path":        req.GetString("path", ""),
			"mode":        fmt.Sprintf("0%o", mode),
			"mode_string": info.Mode().String(),
		}

		// Try to get owner/group info
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			uid := strconv.Itoa(int(stat.Uid))
			gid := strconv.Itoa(int(stat.Gid))
			if u, err := user.LookupId(uid); err == nil {
				result["owner"] = u.Username
			} else {
				result["owner"] = uid
			}
			if g, err := user.LookupGroupId(gid); err == nil {
				result["group"] = g.Name
			} else {
				result["group"] = gid
			}
		}

		logTool("permissions_file", root.Name, resolved, time.Since(start), nil)
		return jsonResult(result)
	}
}

// Copy copies files/directories within or across roots.
func (s *Server) Copy() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		srcRootName, err := reqString(req, "source_root")
		if err != nil {
			logTool("copy", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		srcRoot, err := GetRoot(s.roots, srcRootName)
		if err != nil {
			logTool("copy", srcRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := CheckAllowlist(srcRoot, "copy"); err != nil {
			logTool("copy", srcRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		destRootName, err := reqString(req, "dest_root")
		if err != nil {
			logTool("copy", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		destRoot, err := GetRoot(s.roots, destRootName)
		if err != nil {
			logTool("copy", destRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := CheckAllowlist(destRoot, "copy"); err != nil {
			logTool("copy", destRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		srcPathStr, err := reqString(req, "source_path")
		if err != nil {
			logTool("copy", srcRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		srcResolved, err := ValidatePath(srcRoot, srcPathStr)
		if err != nil {
			logTool("copy", srcRootName, srcPathStr, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		destPathStr, err := reqString(req, "dest_path")
		if err != nil {
			logTool("copy", destRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		destResolved, err := ValidatePath(destRoot, destPathStr)
		if err != nil {
			logTool("copy", destRootName, destPathStr, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		srcInfo, err := os.Stat(srcResolved)
		if err != nil {
			logTool("copy", srcRootName, srcPathStr, time.Since(start), err)
			return mcp.NewToolResultError("source file not found"), nil
		}

		// Create parent dirs
		if err := os.MkdirAll(filepath.Dir(destResolved), 0755); err != nil {
			logTool("copy", destRootName, destPathStr, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("create parent directories: %v", err)), nil
		}

		if srcInfo.IsDir() {
			if err := copyDir(srcResolved, destResolved, srcRoot, destRoot); err != nil {
				logTool("copy", srcRootName, srcPathStr, time.Since(start), err)
				return mcp.NewToolResultError(fmt.Sprintf("copy directory: %v", err)), nil
			}
			logTool("copy", srcRootName, srcPathStr, time.Since(start), nil)
			return jsonResult(map[string]any{
				"source":       srcPathStr,
				"destination":  destPathStr,
				"is_directory": true,
			})
		}

		size, err := copyFile(srcResolved, destResolved)
		if err != nil {
			logTool("copy", srcRootName, srcPathStr, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("copy: %v", err)), nil
		}

		logTool("copy", srcRootName, srcPathStr, time.Since(start), nil)
		return jsonResult(map[string]any{
			"source":       srcPathStr,
			"destination":  destPathStr,
			"is_directory": false,
			"size":         size,
		})
	}
}

// Move moves files/directories within or across roots.
func (s *Server) Move() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		srcRootName, err := reqString(req, "source_root")
		if err != nil {
			logTool("move", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		srcRoot, err := GetRoot(s.roots, srcRootName)
		if err != nil {
			logTool("move", srcRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := CheckAllowlist(srcRoot, "move"); err != nil {
			logTool("move", srcRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		destRootName, err := reqString(req, "dest_root")
		if err != nil {
			logTool("move", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		destRoot, err := GetRoot(s.roots, destRootName)
		if err != nil {
			logTool("move", destRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := CheckAllowlist(destRoot, "move"); err != nil {
			logTool("move", destRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		srcPathStr, err := reqString(req, "source_path")
		if err != nil {
			logTool("move", srcRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		srcResolved, err := ValidatePath(srcRoot, srcPathStr)
		if err != nil {
			logTool("move", srcRootName, srcPathStr, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		destPathStr, err := reqString(req, "dest_path")
		if err != nil {
			logTool("move", destRootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		destResolved, err := ValidatePath(destRoot, destPathStr)
		if err != nil {
			logTool("move", destRootName, destPathStr, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		if _, err := os.Stat(srcResolved); err != nil {
			logTool("move", srcRootName, srcPathStr, time.Since(start), err)
			return mcp.NewToolResultError("source file not found"), nil
		}

		// Create parent dirs at destination
		if err := os.MkdirAll(filepath.Dir(destResolved), 0755); err != nil {
			logTool("move", destRootName, destPathStr, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("create parent directories: %v", err)), nil
		}

		// Try rename first (atomic for same filesystem)
		if err := os.Rename(srcResolved, destResolved); err != nil {
			// Cross-device: copy + delete
			srcInfo, sErr := os.Stat(srcResolved)
			if sErr != nil {
				logTool("move", srcRootName, srcPathStr, time.Since(start), sErr)
				return mcp.NewToolResultError(fmt.Sprintf("stat source: %v", sErr)), nil
			}
			if srcInfo.IsDir() {
				if cErr := copyDir(srcResolved, destResolved, srcRoot, destRoot); cErr != nil {
					logTool("move", srcRootName, srcPathStr, time.Since(start), cErr)
					return mcp.NewToolResultError(fmt.Sprintf("copy for move: %v", cErr)), nil
				}
			} else {
				if _, cErr := copyFile(srcResolved, destResolved); cErr != nil {
					logTool("move", srcRootName, srcPathStr, time.Since(start), cErr)
					return mcp.NewToolResultError(fmt.Sprintf("copy for move: %v", cErr)), nil
				}
			}
			if rErr := os.RemoveAll(srcResolved); rErr != nil {
				logTool("move", srcRootName, srcPathStr, time.Since(start), rErr)
				return mcp.NewToolResultError(fmt.Sprintf("remove source after move: %v", rErr)), nil
			}
		}

		logTool("move", srcRootName, srcPathStr, time.Since(start), nil)
		return jsonResult(map[string]any{
			"source":      srcPathStr,
			"destination": destPathStr,
		})
	}
}

// Grep searches file content with regex.
func (s *Server) Grep() server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		rootName, err := reqString(req, "root")
		if err != nil {
			logTool("grep", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		root, err := GetRoot(s.roots, rootName)
		if err != nil {
			logTool("grep", rootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := CheckAllowlist(root, "grep"); err != nil {
			logTool("grep", rootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		patternStr, err := reqString(req, "pattern")
		if err != nil {
			logTool("grep", rootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		caseInsensitive := optBool(req, "case_insensitive", false)
		if caseInsensitive {
			patternStr = "(?i)" + patternStr
		}

		re, err := regexp.Compile(patternStr)
		if err != nil {
			logTool("grep", rootName, "", time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("invalid pattern: %v", err)), nil
		}

		searchPath := req.GetString("path", ".")
		globFilter := req.GetString("glob_filter", "")
		contextLines := optInt(req, "context_lines", 0)
		maxResults := optInt(req, "max_results", 100)
		timeoutSec := optInt(req, "timeout_seconds", 300)
		maxDepth := optInt(req, "max_depth", -1)

		searchResolved, err := ValidatePath(root, searchPath)
		if err != nil {
			logTool("grep", rootName, searchPath, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)

		type grepMatch struct {
			File          string   `json:"file"`
			LineNumber    int      `json:"line_number"`
			LineContent   string   `json:"line_content"`
			ContextBefore []string `json:"context_before,omitempty"`
			ContextAfter  []string `json:"context_after,omitempty"`
		}

		var matches []grepMatch
		truncated := false
		timedOut := false

		var globMatcher func(string) bool
		if globFilter != "" {
			globMatcher = func(name string) bool {
				matched, _ := filepath.Match(globFilter, name)
				return matched
			}
		}

		err = filepath.WalkDir(searchResolved, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil // Skip inaccessible entries
			}

			if time.Now().After(deadline) {
				timedOut = true
				return filepath.SkipAll
			}

			if truncated {
				return filepath.SkipAll
			}

			// Depth check
			if maxDepth >= 0 {
				rel, _ := filepath.Rel(searchResolved, path)
				depth := strings.Count(rel, string(os.PathSeparator))
				if d.IsDir() && depth > maxDepth {
					return filepath.SkipDir
				}
			}

			if d.IsDir() {
				return nil
			}

			// Validate path is within root
			if !isWithinRoot(path, root.RealPath) {
				return nil
			}

			// Glob filter
			if globMatcher != nil && !globMatcher(d.Name()) {
				return nil
			}

			// Skip binary files
			f, fErr := os.Open(path)
			if fErr != nil {
				return nil
			}
			defer f.Close()

			header := make([]byte, 8192)
			n, _ := f.Read(header)
			for i := range n {
				if header[i] == 0 {
					return nil // Binary file
				}
			}
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return nil
			}

			content, rErr := io.ReadAll(f)
			if rErr != nil {
				return nil
			}

			lines := strings.Split(string(content), "\n")
			relPath, _ := filepath.Rel(searchResolved, path)

			for lineIdx, line := range lines {
				if re.MatchString(line) {
					m := grepMatch{
						File:        relPath,
						LineNumber:  lineIdx + 1,
						LineContent: line,
					}

					if contextLines > 0 {
						startCtx := lineIdx - contextLines
						if startCtx < 0 {
							startCtx = 0
						}
						m.ContextBefore = lines[startCtx:lineIdx]

						endCtx := lineIdx + contextLines + 1
						if endCtx > len(lines) {
							endCtx = len(lines)
						}
						if lineIdx+1 < len(lines) {
							m.ContextAfter = lines[lineIdx+1 : endCtx]
						}
					}

					matches = append(matches, m)
					if len(matches) >= maxResults {
						truncated = true
						return filepath.SkipAll
					}
				}
			}

			return nil
		})

		if err != nil && !timedOut {
			logTool("grep", rootName, searchPath, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("search error: %v", err)), nil
		}

		logTool("grep", rootName, searchPath, time.Since(start), nil)
		return jsonResult(map[string]any{
			"matches":       matches,
			"total_matches": len(matches),
			"truncated":     truncated,
			"timed_out":     timedOut,
		})
	}
}

// Glob searches for files by name pattern.
func (s *Server) Glob() server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		rootName, err := reqString(req, "root")
		if err != nil {
			logTool("glob", "", "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		root, err := GetRoot(s.roots, rootName)
		if err != nil {
			logTool("glob", rootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}
		if err := CheckAllowlist(root, "glob"); err != nil {
			logTool("glob", rootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		pattern := req.GetString("pattern", "")
		regexStr := req.GetString("regex", "")

		if (pattern == "" && regexStr == "") || (pattern != "" && regexStr != "") {
			err := fmt.Errorf("exactly one of pattern or regex must be provided")
			logTool("glob", rootName, "", time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		searchPath := req.GetString("path", ".")
		maxResults := optInt(req, "max_results", 100)
		timeoutSec := optInt(req, "timeout_seconds", 300)
		maxDepth := optInt(req, "max_depth", -1)
		typeFilter := req.GetString("type_filter", "all")

		searchResolved, err := ValidatePath(root, searchPath)
		if err != nil {
			logTool("glob", rootName, searchPath, time.Since(start), err)
			return mcp.NewToolResultError(err.Error()), nil
		}

		var re *regexp.Regexp
		if regexStr != "" {
			re, err = regexp.Compile(regexStr)
			if err != nil {
				logTool("glob", rootName, "", time.Since(start), err)
				return mcp.NewToolResultError(fmt.Sprintf("invalid regex: %v", err)), nil
			}
		}

		deadline := time.Now().Add(time.Duration(timeoutSec) * time.Second)

		type globMatch struct {
			Path       string `json:"path"`
			Type       string `json:"type"`
			Size       int64  `json:"size"`
			ModifiedAt string `json:"modified_at"`
		}

		var matches []globMatch
		truncated := false
		timedOut := false

		err = filepath.WalkDir(searchResolved, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}

			if time.Now().After(deadline) {
				timedOut = true
				return filepath.SkipAll
			}

			if len(matches) >= maxResults {
				truncated = true
				return filepath.SkipAll
			}

			// Skip root dir itself
			if path == searchResolved {
				return nil
			}

			// Validate within root
			if !isWithinRoot(path, root.RealPath) {
				return nil
			}

			// Depth check
			if maxDepth >= 0 {
				rel, _ := filepath.Rel(searchResolved, path)
				depth := strings.Count(rel, string(os.PathSeparator))
				if d.IsDir() && depth > maxDepth {
					return filepath.SkipDir
				}
			}

			relPath, _ := filepath.Rel(searchResolved, path)

			// Type filter
			entryType := "file"
			if d.Type()&fs.ModeSymlink != 0 {
				entryType = "symlink"
			} else if d.IsDir() {
				entryType = "directory"
			}

			if typeFilter != "all" && entryType != typeFilter {
				return nil
			}

			// Pattern matching
			matched := false
			if pattern != "" {
				// Support ** by checking each path segment
				matched = matchDoublestar(pattern, relPath)
			} else if re != nil {
				matched = re.MatchString(relPath)
			}

			if !matched {
				return nil
			}

			info, iErr := d.Info()
			if iErr != nil {
				return nil
			}

			matches = append(matches, globMatch{
				Path:       relPath,
				Type:       entryType,
				Size:       info.Size(),
				ModifiedAt: info.ModTime().UTC().Format(time.RFC3339),
			})

			return nil
		})

		if err != nil && !timedOut {
			logTool("glob", rootName, searchPath, time.Since(start), err)
			return mcp.NewToolResultError(fmt.Sprintf("search error: %v", err)), nil
		}

		// Sort results alphabetically
		sort.Slice(matches, func(i, j int) bool { return matches[i].Path < matches[j].Path })

		logTool("glob", rootName, searchPath, time.Since(start), nil)
		return jsonResult(map[string]any{
			"matches":       matches,
			"total_matches": len(matches),
			"truncated":     truncated,
			"timed_out":     timedOut,
		})
	}
}

// matchDoublestar implements glob matching with ** support.
func matchDoublestar(pattern, path string) bool {
	// Handle ** patterns
	if strings.Contains(pattern, "**") {
		// Split pattern on **
		parts := strings.Split(pattern, "**")
		if len(parts) == 2 {
			prefix := strings.TrimSuffix(parts[0], "/")
			suffix := strings.TrimPrefix(parts[1], "/")

			// For a pattern like "**/*.go", match any .go file at any depth
			if prefix == "" {
				segments := strings.Split(path, string(os.PathSeparator))
				for i := range segments {
					subPath := strings.Join(segments[i:], string(os.PathSeparator))
					if matched, _ := filepath.Match(suffix, subPath); matched {
						return true
					}
				}
				// Also try matching just the filename
				if matched, _ := filepath.Match(suffix, filepath.Base(path)); matched {
					return true
				}
				return false
			}

			// For prefix/**/suffix patterns
			if !strings.HasPrefix(path, prefix+string(os.PathSeparator)) && path != prefix {
				return false
			}
			remainder := strings.TrimPrefix(path, prefix+string(os.PathSeparator))
			if suffix == "" {
				return true
			}
			segments := strings.Split(remainder, string(os.PathSeparator))
			for i := range segments {
				subPath := strings.Join(segments[i:], string(os.PathSeparator))
				if matched, _ := filepath.Match(suffix, subPath); matched {
					return true
				}
			}
			return false
		}
	}

	// Simple glob match (no **)
	matched, _ := filepath.Match(pattern, filepath.Base(path))
	return matched
}

// copyFile copies a single file.
func copyFile(src, dst string) (int64, error) {
	sf, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer sf.Close()

	df, err := os.Create(dst)
	if err != nil {
		return 0, err
	}
	defer df.Close()

	n, err := io.Copy(df, sf)
	if err != nil {
		return 0, err
	}

	// Preserve permissions
	if info, sErr := os.Stat(src); sErr == nil {
		if chErr := os.Chmod(dst, info.Mode()); chErr != nil {
			return 0, fmt.Errorf("chmod %s: %w", dst, chErr)
		}
	}

	return n, nil
}

// copyDir copies a directory recursively.
func copyDir(src, dst string, srcRoot, dstRoot *ResolvedRoot) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())

		// Validate paths
		if !isWithinRoot(srcPath, srcRoot.RealPath) {
			continue
		}

		if e.IsDir() {
			if err := copyDir(srcPath, dstPath, srcRoot, dstRoot); err != nil {
				return err
			}
		} else {
			if _, err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func formatSize(bytes int64) string {
	const (
		kb = 1024
		mb = kb * 1024
	)
	switch {
	case bytes >= mb:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
