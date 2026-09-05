package scanner

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestScanSeparatesTextAndNonTextFiles(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, "main.go", []byte("package main\n"))
	writeFile(t, root, "src/app.ts", []byte("export const a = 1\n"))
	// binary by extension
	writeFile(t, root, "logo.png", []byte("\x89PNG\r\n\x1a\n"))
	// binary by content (NUL byte) despite a text-ish extension
	writeFile(t, root, "data.txt", []byte("abc\x00def"))
	// oversized text file
	writeFile(t, root, "huge.log", make([]byte, maxTextSize+1))

	res, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	wantText := []string{filepath.FromSlash("main.go"), filepath.FromSlash("src/app.ts")}
	for _, w := range wantText {
		if !slices.Contains(res.Files, w) {
			t.Errorf("Files missing %q; got %v", w, res.Files)
		}
	}

	wantOther := []string{
		filepath.FromSlash("logo.png"),
		filepath.FromSlash("data.txt"),
		filepath.FromSlash("huge.log"),
	}
	for _, w := range wantOther {
		if !slices.Contains(res.OtherFiles, w) {
			t.Errorf("OtherFiles missing %q; got %v", w, res.OtherFiles)
		}
		if slices.Contains(res.Files, w) {
			t.Errorf("%q must not be snapshot-tracked in Files", w)
		}
	}
}

func TestScanKeepsGitignoredFilesOutOfBothLists(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".gitignore", []byte("secret.txt\nblobs/\n"))
	writeFile(t, root, "keep.go", []byte("package main\n"))
	writeFile(t, root, "secret.txt", []byte("token\n"))
	writeFile(t, root, "blobs/pic.png", []byte("\x89PNG\r\n\x1a\n"))

	res, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	all := slices.Concat(res.Files, res.OtherFiles)
	for _, unwanted := range []string{"secret.txt", filepath.FromSlash("blobs/pic.png")} {
		if slices.Contains(all, unwanted) {
			t.Errorf("gitignored %q should be excluded; got %v", unwanted, all)
		}
	}
	if !slices.Contains(res.Files, "keep.go") {
		t.Errorf("keep.go should be tracked; got %v", res.Files)
	}
}

func TestScanListsEmptyDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "empty", "nested"), 0755); err != nil {
		t.Fatal(err)
	}

	res, err := Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"empty", "empty/nested"} {
		if !slices.Contains(res.Directories, want) {
			t.Errorf("directories %v do not include %q", res.Directories, want)
		}
	}
}

func TestScanShowsDotfilesButSkipsVCSAndDeps(t *testing.T) {
	root := t.TempDir()

	writeFile(t, root, ".env", []byte("A=1\n"))
	writeFile(t, root, ".gitignore", []byte("ignored.txt\n"))
	writeFile(t, root, ".github/workflows/ci.yml", []byte("name: ci\n"))
	writeFile(t, root, ".vscode/settings.json", []byte("{}\n"))
	writeFile(t, root, ".git/config", []byte("[core]\n"))
	writeFile(t, root, ".svn/entries", []byte("12\n"))
	writeFile(t, root, "node_modules/pkg/index.js", []byte("module.exports=1\n"))
	writeFile(t, root, "vendor/lib.go", []byte("package lib\n"))
	writeFile(t, root, "__pycache__/x.pyc", []byte{0x00})
	writeFile(t, root, ".warp-snapshots/x", []byte("snap\n"))
	writeFile(t, root, "ignored.txt", []byte("nope\n"))

	res, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	all := slices.Concat(res.Files, res.OtherFiles)
	for _, want := range []string{
		".env",
		".gitignore",
		filepath.FromSlash(".github/workflows/ci.yml"),
		filepath.FromSlash(".vscode/settings.json"),
	} {
		if !slices.Contains(all, want) {
			t.Errorf("dot entry %q should be listed; got files=%v others=%v", want, res.Files, res.OtherFiles)
		}
	}
	for _, wantDir := range []string{".github", ".github/workflows", ".vscode"} {
		if !slices.Contains(res.Directories, wantDir) {
			t.Errorf("directories %v do not include %q", res.Directories, wantDir)
		}
	}
	for _, unwanted := range []string{
		filepath.FromSlash(".git/config"),
		filepath.FromSlash(".svn/entries"),
		filepath.FromSlash("node_modules/pkg/index.js"),
		filepath.FromSlash("vendor/lib.go"),
		filepath.FromSlash("__pycache__/x.pyc"),
		filepath.FromSlash(".warp-snapshots/x"),
		"ignored.txt",
	} {
		if slices.Contains(all, unwanted) {
			t.Errorf("%q should be skipped; got %v", unwanted, all)
		}
	}
}
