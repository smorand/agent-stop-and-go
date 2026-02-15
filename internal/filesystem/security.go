package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePath resolves a relative path within a root and validates it stays within bounds.
// It returns the resolved absolute path. For files that don't exist yet (write, create),
// it validates the nearest existing ancestor.
func ValidatePath(root *ResolvedRoot, relPath string) (string, error) {
	// Reject null bytes
	if strings.ContainsRune(relPath, 0) {
		return "", fmt.Errorf("path contains null bytes")
	}

	// Clean the relative path
	cleaned := filepath.Clean(relPath)
	if cleaned == "." || cleaned == "" {
		return root.RealPath, nil
	}

	// Join with root
	target := filepath.Join(root.RealPath, cleaned)

	// Try to resolve the full path (works if it exists)
	resolved, err := filepath.EvalSymlinks(target)
	if err == nil {
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("abs path: %w", err)
		}
		if !isWithinRoot(resolved, root.RealPath) {
			return "", fmt.Errorf("path resolves outside root boundary")
		}
		return resolved, nil
	}

	// Path doesn't exist — validate the nearest existing ancestor
	return validateAncestor(root, target)
}

// validateAncestor walks up from target to find the nearest existing directory,
// validates it's within root, then returns the target path.
func validateAncestor(root *ResolvedRoot, target string) (string, error) {
	dir := target
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent

		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil {
			continue // Keep walking up
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("abs path: %w", err)
		}
		if !isWithinRoot(resolved, root.RealPath) {
			return "", fmt.Errorf("path resolves outside root boundary")
		}

		// Ancestor is valid and within root. Compute the relative remainder
		// and return the full intended path.
		remainder, err := filepath.Rel(dir, target)
		if err != nil {
			return "", fmt.Errorf("compute relative path: %w", err)
		}
		return filepath.Join(resolved, remainder), nil
	}

	return "", fmt.Errorf("cannot resolve path: no valid ancestor found")
}

// isWithinRoot checks if resolved path is within (or equal to) the root path.
func isWithinRoot(resolved, rootPath string) bool {
	if resolved == rootPath {
		return true
	}
	return strings.HasPrefix(resolved, rootPath+string(os.PathSeparator))
}

// CheckAllowlist verifies the tool is allowed on the given root.
func CheckAllowlist(root *ResolvedRoot, toolName string) error {
	if !root.AllowedTools[toolName] {
		return fmt.Errorf("tool %s not allowed on root %s", toolName, root.Name)
	}
	return nil
}

// GetRoot looks up a root by name.
func GetRoot(roots map[string]*ResolvedRoot, name string) (*ResolvedRoot, error) {
	root, ok := roots[name]
	if !ok {
		return nil, fmt.Errorf("unknown root: %s", name)
	}
	return root, nil
}
