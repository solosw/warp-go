package scanner

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"aimuxterm/snapshot"
)

// skipDirs are directories always skipped during scanning.
// Dotfiles/dirs are shown; only VCS, common dependency trees, and
// the app's own snapshot store are excluded.
var skipDirs = map[string]bool{
	".git": true, ".svn": true,
	"node_modules": true, "vendor": true, "__pycache__": true,
	".warp-snapshots": true,
}

// ScanResult holds the result of a workspace scan.
type ScanResult struct {
	Files []string `json:"files"` // relative paths of text files (snapshot-tracked)
	// OtherFiles are files present in the workspace but not snapshot-tracked:
	// binary files and files larger than maxTextSize. They are listed for
	// display purposes only and their content is never loaded.
	OtherFiles []string `json:"otherFiles"`
	// Directories includes visible directories so empty folders appear in the tree.
	Directories []string `json:"directories"`
}

// maxTextSize is the size limit above which a file is not snapshot-tracked.
const maxTextSize = 5 * 1024 * 1024

// Scan recursively scans a workspace directory, respecting .gitignore.
// Text files within the size limit go to Files; binary or oversized files go to
// OtherFiles so the UI can list every file without reading unreadable content.
func Scan(workspace string) (*ScanResult, error) {
	ignore := loadGitignore(workspace)
	var files []string
	var others []string
	var directories []string

	err := filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible files
		}
		relPath, _ := filepath.Rel(workspace, path)
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			if relPath != "." && !ignore.Match(relPath) {
				directories = append(directories, filepath.ToSlash(relPath))
			}
			return nil
		}
		if relPath == "" {
			return nil
		}
		if ignore.Match(relPath) {
			return nil
		}
		// Large files are displayed by name only. Avoid opening them just to
		// inspect their contents during the workspace scan.
		if info.Size() > maxTextSize || isBinaryPath(path) {
			others = append(others, relPath)
			return nil
		}
		files = append(files, relPath)
		return nil
	})

	return &ScanResult{Files: files, OtherFiles: others, Directories: directories}, err
}

// gitignore is a simple .gitignore rule matcher.
type Gitignore struct {
	patterns []pattern
}

// loadGitignore reads and parses the workspace .gitignore file.
func loadGitignore(workspace string) *Gitignore {
	data, err := os.ReadFile(filepath.Join(workspace, ".gitignore"))
	if err != nil {
		return &Gitignore{}
	}
	return ParseGitignore(string(data))
}

type pattern struct {
	negate  bool
	dirOnly bool
	glob    string
}

// ParseGitignore parses .gitignore content into a Gitignore matcher.
func ParseGitignore(content string) *Gitignore {
	gi := &Gitignore{}
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p := pattern{}
		if strings.HasPrefix(line, "!") {
			p.negate = true
			line = line[1:]
		}
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = line[:len(line)-1]
		}
		p.glob = line
		gi.patterns = append(gi.patterns, p)
	}
	return gi
}

func (gi *Gitignore) Match(path string) bool {
	if len(gi.patterns) == 0 {
		return false
	}
	path = filepath.ToSlash(path)
	ignored := false
	for _, p := range gi.patterns {
		matched := matchGlob(p.glob, path)
		if p.dirOnly {
			matched = matchDirGlob(p.glob, path)
		}
		if matched {
			ignored = !p.negate
		}
	}
	return ignored
}

func matchDirGlob(pattern, path string) bool {
	pattern = strings.Trim(pattern, "/")
	path = strings.Trim(path, "/")
	if pattern == "" {
		return false
	}
	if strings.Contains(pattern, "/") {
		return path == pattern || strings.HasPrefix(path, pattern+"/")
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == pattern {
			return true
		}
	}
	return false
}

func matchGlob(pattern, path string) bool {
	pattern = filepath.ToSlash(pattern)
	path = filepath.ToSlash(path)
	// Exact directory/file prefix patterns like data or bin/results
	if !strings.ContainsAny(pattern, "*?[") {
		pattern = strings.Trim(pattern, "/")
		return path == pattern || strings.HasPrefix(path, pattern+"/")
	}
	// Handle ** patterns
	if strings.Contains(pattern, "**") {
		parts := strings.Split(pattern, "**")
		rest := path
		for i, part := range parts {
			part = strings.Trim(part, "/")
			if part == "" {
				continue
			}
			idx := strings.Index(rest, part)
			if idx < 0 {
				return false
			}
			if i == 0 && !strings.HasPrefix(path, part) && !strings.HasPrefix(pattern, "**") {
				return false
			}
			rest = rest[idx+len(part):]
		}
		return true
	}
	// Simple filepath.Match for basic patterns
	matched, _ := filepath.Match(pattern, filepath.Base(path))
	if matched {
		return true
	}
	// Also try matching against full path
	matched, _ = filepath.Match(pattern, path)
	return matched
}

func isBinaryPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return !snapshot.IsTextFile(ext, buf[:n])
}
