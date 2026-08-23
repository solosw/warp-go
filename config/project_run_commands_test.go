package config

import "testing"

func TestProjectRunCommandsArePerWorkspace(t *testing.T) {
	tmp := t.TempDir()
	store := &Store{dir: tmp}

	wsA := `C:\projects\a`
	wsB := `C:\projects\b`
	if err := store.SaveProjectRunCommands(wsA, []ProjectRunCommand{
		{Name: "dev", Command: "npm run dev"},
		{Name: "build", Command: "npm run build"},
	}); err != nil {
		t.Fatalf("save A failed: %v", err)
	}
	if err := store.SaveProjectRunCommands(wsB, []ProjectRunCommand{
		{Name: "serve", Command: "go run ."},
	}); err != nil {
		t.Fatalf("save B failed: %v", err)
	}

	aCmds, err := store.LoadProjectRunCommands(wsA)
	if err != nil {
		t.Fatalf("load A failed: %v", err)
	}
	if len(aCmds) != 2 || aCmds[0].Command != "npm run dev" {
		t.Fatalf("unexpected A commands: %#v", aCmds)
	}

	bCmds, err := store.LoadProjectRunCommands(wsB)
	if err != nil {
		t.Fatalf("load B failed: %v", err)
	}
	if len(bCmds) != 1 || bCmds[0].Name != "serve" {
		t.Fatalf("unexpected B commands: %#v", bCmds)
	}
}
