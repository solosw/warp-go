package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeWorkspaceRelPath(t *testing.T) {
	ok, err := sanitizeWorkspaceRelPath(`docs\logo.png`)
	if err != nil || ok != "docs/logo.png" {
		t.Fatalf("got %q %v", ok, err)
	}
	if _, err := sanitizeWorkspaceRelPath("../secret.png"); err == nil {
		t.Fatal("expected reject traversal")
	}
	if _, err := sanitizeWorkspaceRelPath("/etc/passwd"); err == nil {
		t.Fatal("expected reject absolute")
	}
	if _, err := sanitizeWorkspaceRelPath("C:/windows/x.png"); err == nil {
		t.Fatal("expected reject drive")
	}
}

func TestPreviewMIME(t *testing.T) {
	if mime, ok := previewMIME("a/b/photo.PNG"); !ok || mime != "image/png" {
		t.Fatalf("png mime=%q ok=%v", mime, ok)
	}
	if mime, ok := previewMIME("report.pdf"); !ok || mime != "application/pdf" {
		t.Fatalf("pdf mime=%q ok=%v", mime, ok)
	}
	if _, ok := previewMIME("main.go"); ok {
		t.Fatal("go should not be preview binary")
	}
}

func TestGetFilePreviewDataImage(t *testing.T) {
	root := t.TempDir()
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 1, 2, 3}
	if err := os.WriteFile(filepath.Join(root, "logo.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{workspace: root}
	url, err := a.GetFilePreviewData("logo.png")
	if err != nil {
		t.Fatal(err)
	}
	prefix := "data:image/png;base64,"
	if !strings.HasPrefix(url, prefix) {
		t.Fatalf("url=%q", url)
	}
	got, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, prefix))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(png) {
		t.Fatalf("decoded mismatch")
	}
}

func TestGetFilePreviewDataRejectsUnknown(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.bin"), []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{workspace: root}
	if _, err := a.GetFilePreviewData("a.bin"); err == nil {
		t.Fatal("expected reject")
	}
}
