package detector

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestDetectNodeExpress(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "package.json", `{
		"scripts": {"start": "node server.js"},
		"dependencies": {"express": "^4.19.2"}
	}`)

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.Language != "node" {
		t.Errorf("Language = %q, want node", info.Language)
	}
	if info.Framework != "express" {
		t.Errorf("Framework = %q, want express", info.Framework)
	}
	if info.Port != 3000 {
		t.Errorf("Port = %d, want 3000", info.Port)
	}
	if info.Entrypoint != "npm start" {
		t.Errorf("Entrypoint = %q, want %q", info.Entrypoint, "npm start")
	}
}

func TestDetectGoModule(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/myapp\n\ngo 1.22\n")
	writeFile(t, dir, "main.go", "package main\nfunc main() {}\n")

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.Language != "go" {
		t.Errorf("Language = %q, want go", info.Language)
	}
	if info.ModuleOrPkg != "example.com/myapp" {
		t.Errorf("ModuleOrPkg = %q, want example.com/myapp", info.ModuleOrPkg)
	}
	if info.Entrypoint != "go run ." {
		t.Errorf("Entrypoint = %q, want %q", info.Entrypoint, "go run .")
	}
}

func TestDetectUnknownLanguageFailsClosed(t *testing.T) {
	dir := t.TempDir() // no marker files at all

	info, err := Detect(dir)
	if err == nil {
		t.Fatal("Detect() expected an error for an unrecognized repo, got nil")
	}
	if info.Confidence != "low" {
		t.Errorf("Confidence = %q, want low", info.Confidence)
	}
}

func TestDetectPythonFlask(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "flask==3.0.0\n")
	writeFile(t, dir, "app.py", "from flask import Flask\napp = Flask(__name__)\n")

	info, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}
	if info.Framework != "flask" {
		t.Errorf("Framework = %q, want flask", info.Framework)
	}
	if info.Port != 5000 {
		t.Errorf("Port = %d, want 5000", info.Port)
	}
}

func mkdirAll(t *testing.T, dir, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, rel), 0o755); err != nil {
		t.Fatalf("creating %s: %v", rel, err)
	}
}
